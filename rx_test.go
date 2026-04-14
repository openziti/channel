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
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestChannelImpl() *channelImpl {
	underlay := &testUnderlay{headers: map[int32][]byte{}}
	var u Underlay = underlay

	ch := &channelImpl{
		logicalName:     "test",
		receiveHandlers: map[int32]ReceiveHandlerF{},
		underlays:       NewUnderlays(),
		closeNotify:     make(chan struct{}),
	}
	ch.fallbackUnderlay.Store(&u)
	return ch
}

func Test_Rx_DispatchesToContentTypeHandler(t *testing.T) {
	ch := newTestChannelImpl()

	var received *Message
	ch.receiveHandlers[100] = func(m *Message, _ Channel) {
		received = m
	}

	msg := NewMessage(100, []byte("hello"))
	ch.rx(msg)

	require.NotNil(t, received)
	require.Equal(t, int32(100), received.ContentType)
}

func Test_Rx_FallsBackToAnyContentType(t *testing.T) {
	ch := newTestChannelImpl()

	var received *Message
	ch.receiveHandlers[AnyContentType] = func(m *Message, _ Channel) {
		received = m
	}

	msg := NewMessage(999, []byte("catch-all"))
	ch.rx(msg)

	require.NotNil(t, received, "AnyContentType handler should catch unhandled messages")
	require.Equal(t, int32(999), received.ContentType)
}

func Test_Rx_DropsMessageWithNoHandler(t *testing.T) {
	ch := newTestChannelImpl()

	// No handlers registered — message should be dropped without panic
	msg := NewMessage(999, nil)
	require.NotPanics(t, func() {
		ch.rx(msg)
	})
}

func Test_Rx_ReplyMatchedToWaiter(t *testing.T) {
	ch := newTestChannelImpl()

	receiver := &testReplyReceiver{}

	original := NewMessage(100, nil)
	original.SetSequence(42)

	s := &testSendable{
		msg:           original,
		replyReceiver: receiver,
		ctx:           context.Background(),
	}
	ch.waiters.AddWaiter(s)

	reply := NewMessage(100, []byte("reply-body"))
	reply.ReplyTo(original)
	ch.rx(reply)

	require.NotNil(t, receiver.msg, "waiter should have received the reply")
	require.Equal(t, []byte("reply-body"), receiver.msg.Body)
}

func Test_Rx_ReplyWithNoWaiterFallsToContentHandler(t *testing.T) {
	ch := newTestChannelImpl()

	var received *Message
	ch.receiveHandlers[100] = func(m *Message, _ Channel) {
		received = m
	}

	original := NewMessage(100, nil)
	original.SetSequence(42)

	// No waiter registered for sequence 42
	reply := NewMessage(100, []byte("orphan-reply"))
	reply.ReplyTo(original)
	ch.rx(reply)

	require.NotNil(t, received, "reply with no waiter should fall through to content-type handler")
}

func Test_Rx_ReplyWithNoWaiterFallsToAnyHandler(t *testing.T) {
	ch := newTestChannelImpl()

	var received *Message
	ch.receiveHandlers[AnyContentType] = func(m *Message, _ Channel) {
		received = m
	}

	original := NewMessage(200, nil)
	original.SetSequence(7)

	reply := NewMessage(200, nil)
	reply.ReplyTo(original)
	ch.rx(reply)

	require.NotNil(t, received, "reply with no waiter and no content handler should fall through to AnyContentType")
}

func Test_Rx_ContentTypeHandlerTakesPriorityOverAny(t *testing.T) {
	ch := newTestChannelImpl()

	var specificCalled, anyCalled atomic.Bool

	ch.receiveHandlers[100] = func(m *Message, _ Channel) {
		specificCalled.Store(true)
	}
	ch.receiveHandlers[AnyContentType] = func(m *Message, _ Channel) {
		anyCalled.Store(true)
	}

	msg := NewMessage(100, nil)
	ch.rx(msg)

	require.True(t, specificCalled.Load())
	require.False(t, anyCalled.Load(), "specific handler should take priority over AnyContentType")
}
