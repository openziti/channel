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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newGroupedTestUnderlay(connId string, isFirst bool) *testUnderlay {
	headers := map[int32][]byte{
		TypeHeader: []byte("default"),
	}
	Headers(headers).PutBoolHeader(IsGroupedHeader, true)
	if isFirst {
		Headers(headers).PutBoolHeader(IsFirstGroupConnection, true)
	}
	return &testUnderlay{
		headers:      headers,
		connectionId: connId,
	}
}

func newUngroupedTestUnderlay(connId string) *testUnderlay {
	return &testUnderlay{
		headers:      map[int32][]byte{},
		connectionId: connId,
	}
}

func Test_MultiListener_FirstGroupedCreatesChannel(t *testing.T) {
	var factoryCalled atomic.Bool

	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		factoryCalled.Store(true)
		return &testChannel{connId: underlay.ConnectionId()}, nil
	}, func(underlay Underlay) error {
		return errors.New("ungrouped not expected")
	})

	ml.AcceptUnderlay(newGroupedTestUnderlay("conn-1", true))

	require.True(t, factoryCalled.Load())

	ml.lock.Lock()
	_, exists := ml.channels["conn-1"]
	ml.lock.Unlock()
	require.True(t, exists)
}

func Test_MultiListener_NonFirstWithNoExistingChannelClosesUnderlay(t *testing.T) {
	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		t.Fatal("factory should not be called")
		return nil, nil
	}, func(underlay Underlay) error {
		return errors.New("ungrouped not expected")
	})

	underlay := newGroupedTestUnderlay("conn-1", false)
	ml.AcceptUnderlay(underlay)

	require.True(t, underlay.closed, "underlay should be closed when not first and no existing channel")
}

func Test_MultiListener_UngroupedDelegatesToFallback(t *testing.T) {
	var fallbackCalled atomic.Bool

	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		t.Fatal("factory should not be called for ungrouped")
		return nil, nil
	}, func(underlay Underlay) error {
		fallbackCalled.Store(true)
		return nil
	})

	ml.AcceptUnderlay(newUngroupedTestUnderlay("conn-1"))

	require.True(t, fallbackCalled.Load())
}

func Test_MultiListener_UngroupedFallbackErrorClosesUnderlay(t *testing.T) {
	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		return nil, nil
	}, func(underlay Underlay) error {
		return errors.New("fallback failed")
	})

	underlay := newUngroupedTestUnderlay("conn-1")
	ml.AcceptUnderlay(underlay)

	require.True(t, underlay.closed, "underlay should be closed when fallback errors")
}

func Test_MultiListener_FactoryNilNilReturnsError(t *testing.T) {
	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		return nil, nil
	}, func(underlay Underlay) error {
		return nil
	})

	underlay := newGroupedTestUnderlay("conn-1", true)
	ml.AcceptUnderlay(underlay)

	require.True(t, underlay.closed, "underlay should be closed when factory returns nil/nil")

	ml.lock.Lock()
	_, exists := ml.channels["conn-1"]
	ml.lock.Unlock()
	require.False(t, exists, "channel should not be stored when factory returns nil/nil")
}

func Test_MultiListener_SecondUnderlayRoutedToExistingChannel(t *testing.T) {
	ch := &testChannel{connId: "conn-1"}

	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		return ch, nil
	}, func(underlay Underlay) error {
		return nil
	})

	ml.AcceptUnderlay(newGroupedTestUnderlay("conn-1", true))

	ml.AcceptUnderlay(newGroupedTestUnderlay("conn-1", false))

	require.Equal(t, 1, ch.acceptCount, "second underlay should be accepted by existing channel")
}

func Test_MultiListener_ConcurrentFirstUnderlaySameId(t *testing.T) {
	var factoryCallCount atomic.Int32
	factoryGate := make(chan struct{})

	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		factoryCallCount.Add(1)
		<-factoryGate
		return &testChannel{connId: underlay.ConnectionId()}, nil
	}, func(underlay Underlay) error {
		return nil
	})

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		ml.AcceptUnderlay(newGroupedTestUnderlay("conn-1", true))
	}()

	go func() {
		defer wg.Done()
		ml.AcceptUnderlay(newGroupedTestUnderlay("conn-1", true))
	}()

	// Wait for factory to be called once, then release
	go func() {
		for factoryCallCount.Load() < 1 {
			time.Sleep(time.Millisecond)
		}
		close(factoryGate)
	}()

	wg.Wait()

	require.Equal(t, int32(1), factoryCallCount.Load(),
		"factory should only be called once for concurrent first underlays with same ID")
}

func Test_MultiListener_CloseChannelRemovesFromMap(t *testing.T) {
	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		return &testChannel{connId: underlay.ConnectionId()}, nil
	}, func(underlay Underlay) error {
		return nil
	})

	ml.AcceptUnderlay(newGroupedTestUnderlay("conn-1", true))

	ml.lock.Lock()
	_, exists := ml.channels["conn-1"]
	ml.lock.Unlock()
	require.True(t, exists)

	ml.CloseChannel("conn-1")

	ml.lock.Lock()
	_, exists = ml.channels["conn-1"]
	ml.lock.Unlock()
	require.False(t, exists)
}

// testChannel is a minimal Channel implementation for MultiListener tests.
type testChannel struct {
	connId      string
	acceptCount int
}

func (c *testChannel) AcceptUnderlay(Underlay) error            { c.acceptCount++; return nil }
func (c *testChannel) Id() string                               { return c.connId }
func (c *testChannel) LogicalName() string                      { return "test" }
func (c *testChannel) SetLogicalName(string)                    {}
func (c *testChannel) ConnectionId() string                     { return c.connId }
func (c *testChannel) Certificates() []*x509.Certificate        { return nil }
func (c *testChannel) Label() string                            { return "test-ch" }
func (c *testChannel) Send(Sendable) error                      { return nil }
func (c *testChannel) TrySend(Sendable) (bool, error)           { return true, nil }
func (c *testChannel) CloseNotify() <-chan struct{}              { return nil }
func (c *testChannel) Close() error                             { return nil }
func (c *testChannel) IsClosed() bool                           { return false }
func (c *testChannel) Underlay() Underlay                       { return nil }
func (c *testChannel) Headers() map[int32][]byte                { return nil }
func (c *testChannel) GetTimeSinceLastRead() time.Duration      { return 0 }
func (c *testChannel) GetUserData() any                         { return nil }
func (c *testChannel) GetSenders() Senders                      { return nil }
func (c *testChannel) GetUnderlays() []Underlay                 { return nil }
func (c *testChannel) GetUnderlayCountsByType() map[string]int  { return nil }
