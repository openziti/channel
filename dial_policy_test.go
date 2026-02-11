/*
	Copyright NetFoundry Inc.

	Licensed under the Apache License, Version 2.0 (the "License");
	you may not use this file except in compliance with the License.
	You may obtain a copy of the License at

	https://www.apache.org/licenses/LICENSE-2.0

	Unless required by applicable law or agreed to in writing, software
	distributed under the License is distributed on an "AS IS" BASIS,
	WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
	See the License for the specific language governing permissions and
	limitations under the License.
*/

package channel

import (
	"crypto/x509"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newTestPolicy() *BackoffDialPolicy {
	return &BackoffDialPolicy{
		Backoff: BackoffConfig{
			BaseDelay:         100 * time.Millisecond,
			MaxDelay:          time.Second,
			MinStableDuration: 50 * time.Millisecond,
		},
	}
}

func Test_BackoffDelay_ZeroWithNoFailures(t *testing.T) {
	p := newTestPolicy()
	require.Equal(t, time.Duration(0), p.getBackoffDelay())
}

func Test_BackoffDelay_ExponentialProgression(t *testing.T) {
	p := newTestPolicy()

	p.recordFailure()
	require.Equal(t, 100*time.Millisecond, p.getBackoffDelay()) // base * 2^0

	p.recordFailure()
	require.Equal(t, 200*time.Millisecond, p.getBackoffDelay()) // base * 2^1

	p.recordFailure()
	require.Equal(t, 400*time.Millisecond, p.getBackoffDelay()) // base * 2^2

	p.recordFailure()
	require.Equal(t, 800*time.Millisecond, p.getBackoffDelay()) // base * 2^3
}

func Test_BackoffDelay_CappedAtMaxDelay(t *testing.T) {
	p := newTestPolicy()

	// Push failures well past max
	for i := 0; i < 20; i++ {
		p.recordFailure()
	}
	require.Equal(t, time.Second, p.getBackoffDelay())
}

func Test_RecordFailure_ClearsLastSuccess(t *testing.T) {
	p := newTestPolicy()

	p.recordSuccess()
	require.False(t, p.lastSuccess.IsZero())

	p.recordFailure()
	require.True(t, p.lastSuccess.IsZero())
	require.Equal(t, uint32(1), p.ConsecutiveFailures())
}

func Test_RecordSuccess_DoesNotResetFailures(t *testing.T) {
	p := newTestPolicy()

	p.recordFailure()
	p.recordFailure()
	require.Equal(t, uint32(2), p.ConsecutiveFailures())

	p.recordSuccess()
	require.Equal(t, uint32(2), p.ConsecutiveFailures(), "failures should not reset until connection proves stable")
}

func Test_RecordStartDial_ShortLivedConnectionCountsAsFailure(t *testing.T) {
	p := newTestPolicy()

	p.recordSuccess()
	// Call recordStartDial immediately — connection was short-lived
	p.recordStartDial()
	require.Equal(t, uint32(1), p.ConsecutiveFailures())
}

func Test_RecordStartDial_StableConnectionResetsFailures(t *testing.T) {
	p := newTestPolicy()

	// Accumulate some failures
	p.recordFailure()
	p.recordFailure()
	p.recordFailure()
	require.Equal(t, uint32(3), p.ConsecutiveFailures())

	// Simulate a successful connection that lives long enough
	p.recordSuccess()
	time.Sleep(p.Backoff.MinStableDuration + 10*time.Millisecond)

	p.recordStartDial()
	require.Equal(t, uint32(0), p.ConsecutiveFailures())
	require.Equal(t, time.Duration(0), p.getBackoffDelay())
}

func Test_RecordStartDial_NoopWithoutPriorSuccess(t *testing.T) {
	p := newTestPolicy()

	p.recordFailure()
	p.recordFailure()

	// No success recorded, so recordStartDial should not change failure count
	p.recordStartDial()
	require.Equal(t, uint32(2), p.ConsecutiveFailures())
}

func Test_RecordStartDial_IdempotentOnSecondCall(t *testing.T) {
	p := newTestPolicy()

	p.recordSuccess()
	p.recordStartDial() // classifies the connection, clears lastSuccess
	require.Equal(t, uint32(1), p.ConsecutiveFailures())

	p.recordStartDial() // lastSuccess is zero, should be a no-op
	require.Equal(t, uint32(1), p.ConsecutiveFailures())
}

func Test_FullCycle_FailuresRecoverAfterStableConnection(t *testing.T) {
	p := newTestPolicy()

	// Several failures build up backoff
	p.recordFailure()
	p.recordFailure()
	p.recordFailure()
	require.Equal(t, uint32(3), p.ConsecutiveFailures())
	require.True(t, p.getBackoffDelay() > 0)

	// A dial succeeds
	p.recordSuccess()
	// Failures still elevated — not reset yet
	require.Equal(t, uint32(3), p.ConsecutiveFailures())

	// Connection lives long enough to be stable
	time.Sleep(p.Backoff.MinStableDuration + 10*time.Millisecond)

	// Next dial starts: evaluates previous connection as stable, resets failures
	p.recordStartDial()
	require.Equal(t, uint32(0), p.ConsecutiveFailures())
	require.Equal(t, time.Duration(0), p.getBackoffDelay())
}

func Test_FullCycle_ShortLivedConnectionEscalatesBackoff(t *testing.T) {
	p := newTestPolicy()

	// First dial fails
	p.recordFailure()
	require.Equal(t, uint32(1), p.ConsecutiveFailures())

	// Second dial succeeds but dies quickly
	p.recordSuccess()

	// Next dial starts immediately — short-lived
	p.recordStartDial()
	require.Equal(t, uint32(2), p.ConsecutiveFailures())

	// Another success that also dies quickly
	p.recordSuccess()
	p.recordStartDial()
	require.Equal(t, uint32(3), p.ConsecutiveFailures())
}

func Test_Dial_CancelDuringBackoff(t *testing.T) {
	p := newTestPolicy()
	p.Backoff.BaseDelay = time.Second // long enough that cancel fires first

	p.recordFailure()

	cancel := make(chan struct{})
	close(cancel) // already cancelled

	_, err := p.Dial("default", "conn-1", []byte("secret"), time.Second, cancel)
	require.Error(t, err)
	require.IsType(t, ClosedError{}, err)
}

func Test_Dial_CancelBeforeDial(t *testing.T) {
	p := newTestPolicy()
	// No failures, so no backoff delay — but cancel is already closed
	cancel := make(chan struct{})
	close(cancel)

	_, err := p.Dial("default", "conn-1", []byte("secret"), time.Second, cancel)
	require.Error(t, err)
	require.IsType(t, ClosedError{}, err)
}

func Test_Dial_FactoryFailureRecordsFailure(t *testing.T) {
	dialer := &failingDialerFactory{err: errors.New("connection refused")}
	p := NewBackoffDialPolicy(dialer)

	cancel := make(chan struct{})
	_, err := p.Dial("default", "conn-1", []byte("secret"), time.Second, cancel)
	require.Error(t, err)
	require.Equal(t, uint32(1), p.ConsecutiveFailures())
}

func Test_Dial_FactorySuccessRecordsSuccess(t *testing.T) {
	dialer := &succeedingDialerFactory{}
	p := NewBackoffDialPolicy(dialer)

	cancel := make(chan struct{})
	underlay, err := p.Dial("default", "conn-1", []byte("secret"), time.Second, cancel)
	require.NoError(t, err)
	require.NotNil(t, underlay)
	require.False(t, p.lastSuccess.IsZero())
}

func Test_Dial_PassesCorrectHeaders(t *testing.T) {
	dialer := &succeedingDialerFactory{}
	p := NewBackoffDialPolicy(dialer)

	cancel := make(chan struct{})
	_, err := p.Dial("priority", "conn-42", []byte("mysecret"), time.Second, cancel)
	require.NoError(t, err)

	require.Equal(t, "priority", string(dialer.lastHeaders[TypeHeader]))
	require.Equal(t, "conn-42", string(dialer.lastHeaders[ConnectionIdHeader]))
	require.Equal(t, []byte("mysecret"), dialer.lastHeaders[GroupSecretHeader])
	require.Equal(t, []byte{1}, dialer.lastHeaders[IsGroupedHeader])
}

// failingDialerFactory is a DialUnderlayFactory that always returns an error.
type failingDialerFactory struct {
	err error
}

func (f *failingDialerFactory) Create(time.Duration) (Underlay, error) {
	return nil, f.err
}

func (f *failingDialerFactory) CreateWithHeaders(time.Duration, map[int32][]byte) (Underlay, error) {
	return nil, f.err
}

// succeedingDialerFactory is a DialUnderlayFactory that returns a minimal mock underlay.
type succeedingDialerFactory struct {
	lastHeaders map[int32][]byte
}

func (f *succeedingDialerFactory) Create(time.Duration) (Underlay, error) {
	return &testUnderlay{}, nil
}

func (f *succeedingDialerFactory) CreateWithHeaders(_ time.Duration, headers map[int32][]byte) (Underlay, error) {
	f.lastHeaders = headers
	return &testUnderlay{headers: headers}, nil
}

// testUnderlay is a minimal Underlay implementation for unit tests.
type testUnderlay struct {
	headers      map[int32][]byte
	connectionId string
	closed       bool
}

func (t *testUnderlay) Rx() (*Message, error)               { return nil, nil }
func (t *testUnderlay) Tx(*Message) error                   { return nil }
func (t *testUnderlay) Id() string                          { return "test" }
func (t *testUnderlay) LogicalName() string                 { return "test" }
func (t *testUnderlay) Certificates() []*x509.Certificate   { return nil }
func (t *testUnderlay) Label() string                       { return "test-underlay" }
func (t *testUnderlay) IsClosed() bool                      { return t.closed }
func (t *testUnderlay) Headers() map[int32][]byte           { return t.headers }
func (t *testUnderlay) SetWriteTimeout(time.Duration) error { return nil }
func (t *testUnderlay) SetWriteDeadline(time.Time) error    { return nil }
func (t *testUnderlay) GetLocalAddr() net.Addr              { return nil }
func (t *testUnderlay) GetRemoteAddr() net.Addr             { return nil }

func (t *testUnderlay) ConnectionId() string {
	if t.connectionId != "" {
		return t.connectionId
	}
	return "test-conn"
}

func (t *testUnderlay) Close() error {
	t.closed = true
	return nil
}
