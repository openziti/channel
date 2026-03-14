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
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_Tx_ExpiredContextNotifiesTimeout(t *testing.T) {
	ch := newTestChannelImpl()
	ch.senders = &testSenders{}

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()

	listener := &trackingSendListener{}
	s := &testTxSendable{
		msg:      NewMessage(100, nil),
		ctx:      cancelCtx,
		listener: listener,
	}

	err := ch.tx(&testUnderlay{}, "default", s, 0)
	require.NoError(t, err, "tx should return nil when context is expired (not a write error)")
	require.True(t, listener.errNotified, "should have called NotifyErr")
	require.True(t, IsTimeout(listener.lastErr), "error should be a TimeoutError")
	require.False(t, listener.beforeWriteCalled, "should not have called NotifyBeforeWrite")
}

func Test_Tx_WriteFailure_RequeueSuppressesNotify(t *testing.T) {
	ch := newTestChannelImpl()
	ch.senders = &testSenders{handleTxFailed: true}

	underlay := &failingTxUnderlay{err: errors.New("write failed")}
	listener := &trackingSendListener{}
	msg := NewMessage(100, nil)
	msg.SetSequence(1)

	s := &testTxSendable{
		msg:      msg,
		ctx:      context.Background(),
		listener: listener,
	}

	err := ch.tx(underlay, "default", s, 0)
	require.Error(t, err)
	require.False(t, listener.errNotified, "should NOT notify error when HandleTxFailed requeues")
	require.False(t, listener.afterWriteCalled, "should NOT notify after write when HandleTxFailed requeues")
}

func Test_Tx_WriteFailure_NoRequeueNotifiesError(t *testing.T) {
	ch := newTestChannelImpl()
	ch.senders = &testSenders{handleTxFailed: false}

	writeErr := errors.New("write failed")
	underlay := &failingTxUnderlay{err: writeErr}
	listener := &trackingSendListener{}
	msg := NewMessage(100, nil)
	msg.SetSequence(1)

	s := &testTxSendable{
		msg:      msg,
		ctx:      context.Background(),
		listener: listener,
	}

	err := ch.tx(underlay, "default", s, 0)
	require.Error(t, err)
	require.True(t, listener.errNotified, "should notify error when not requeued")
	require.True(t, listener.afterWriteCalled, "should notify after write when not requeued")
	require.Equal(t, writeErr, listener.lastErr)
}

func Test_Tx_NilMessageIsTracer(t *testing.T) {
	ch := newTestChannelImpl()
	ch.senders = &testSenders{}

	listener := &trackingSendListener{}
	s := &testTxSendable{
		msg:      nil,
		ctx:      context.Background(),
		listener: listener,
	}

	err := ch.tx(&testUnderlay{}, "default", s, 0)
	require.NoError(t, err)
	require.True(t, listener.beforeWriteCalled, "tracer should still get NotifyBeforeWrite")
}

// testTxSendable is a Sendable for tx tests with a trackable SendListener.
type testTxSendable struct {
	msg      *Message
	ctx      context.Context
	listener SendListener
	seq      int32
}

func (s *testTxSendable) Msg() *Message              { return s.msg }
func (s *testTxSendable) SetSequence(seq int32)       { s.seq = seq }
func (s *testTxSendable) Sequence() int32             { return s.seq }
func (s *testTxSendable) Context() context.Context    { return s.ctx }
func (s *testTxSendable) SendListener() SendListener  { return s.listener }
func (s *testTxSendable) ReplyReceiver() ReplyReceiver { return nil }

// trackingSendListener tracks which SendListener methods were called.
type trackingSendListener struct {
	beforeWriteCalled bool
	afterWriteCalled  bool
	errNotified       bool
	lastErr           error
}

func (l *trackingSendListener) NotifyQueued()      {}
func (l *trackingSendListener) NotifyBeforeWrite() { l.beforeWriteCalled = true }
func (l *trackingSendListener) NotifyAfterWrite()  { l.afterWriteCalled = true }
func (l *trackingSendListener) NotifyErr(err error) {
	l.errNotified = true
	l.lastErr = err
}

// testSenders is a minimal Senders for tx tests.
type testSenders struct {
	handleTxFailed bool
}

func (s *testSenders) NextSequence() int32                           { return 0 }
func (s *testSenders) GetCloseNotify() chan struct{}                 { return make(chan struct{}) }
func (s *testSenders) GetDefaultSender() Sender                     { return nil }
func (s *testSenders) HandleTxFailed(_ string, _ Sendable) bool     { return s.handleTxFailed }

// failingTxUnderlay is an underlay whose Tx always returns an error.
type failingTxUnderlay struct {
	testUnderlay
	err error
}

func (u *failingTxUnderlay) Tx(*Message) error { return u.err }
