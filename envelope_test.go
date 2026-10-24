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
	"crypto/x509"
	"math/rand/v2"
	"net"
	"testing"
	"time"

	"github.com/openziti/foundation/v2/sequence"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

const (
	queueWaitFillerType int32 = 1
	queueWaitTestType   int32 = 100
)

// newFullQueue returns a sender whose one-slot queue already holds a filler message.
func newFullQueue(t *testing.T) (SenderContext, chan Sendable, Sender) {
	ctx := NewSenderContext()
	msgC := make(chan Sendable, 1)
	sender := NewSingleChSender(ctx, msgC)

	sent, err := sender.TrySend(NewMessage(queueWaitFillerType, nil))
	require.NoError(t, err)
	require.True(t, sent)

	return ctx, msgC, sender
}

// requireWritten checks that both the single and the multi-underlay tx paths write s.
func requireWritten(t *testing.T, s Sendable) {
	single := &recordingUnderlay{}
	ch := &channelImpl{underlay: single}

	require.NoError(t, ch.tx(s, 0))
	require.Len(t, single.sent, 1, "queued message was not written by channelImpl")
	require.Equal(t, queueWaitTestType, single.sent[0].ContentType)

	multi := &recordingUnderlay{}
	var u Underlay = multi
	multiCh := &multiChannelImpl{}
	multiCh.fallbackUnderlay.Store(&u)

	require.NoError(t, multiCh.Tx(multi, s, 0))
	require.Len(t, multi.sent, 1, "queued message was not written by multiChannelImpl")
	require.Equal(t, queueWaitTestType, multi.sent[0].ContentType)
}

// recordingUnderlay is an Underlay that records the messages written to it.
type recordingUnderlay struct {
	sent []*Message
}

func (u *recordingUnderlay) Rx() (*Message, error)               { return nil, nil }
func (u *recordingUnderlay) Tx(m *Message) error                 { u.sent = append(u.sent, m); return nil }
func (u *recordingUnderlay) Id() string                          { return "test" }
func (u *recordingUnderlay) LogicalName() string                 { return "test" }
func (u *recordingUnderlay) ConnectionId() string                { return "test-conn" }
func (u *recordingUnderlay) Certificates() []*x509.Certificate   { return nil }
func (u *recordingUnderlay) Label() string                       { return "test-underlay" }
func (u *recordingUnderlay) Close() error                        { return nil }
func (u *recordingUnderlay) IsClosed() bool                      { return false }
func (u *recordingUnderlay) Headers() map[int32][]byte           { return nil }
func (u *recordingUnderlay) SetWriteTimeout(time.Duration) error { return nil }
func (u *recordingUnderlay) SetWriteDeadline(time.Time) error    { return nil }
func (u *recordingUnderlay) GetLocalAddr() net.Addr              { return nil }
func (u *recordingUnderlay) GetRemoteAddr() net.Addr             { return nil }

func Test_SendAndWaitForQueue_QueuesImmediatelyWhenSpaceAvailable(t *testing.T) {
	ctx := NewSenderContext()
	msgC := make(chan Sendable, 1)
	sender := NewSingleChSender(ctx, msgC)

	err := NewMessage(queueWaitTestType, nil).WithTimeout(20 * time.Millisecond).SendAndWaitForQueue(sender)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	requireWritten(t, <-msgC)
}

func Test_SendAndWaitForQueue_QueuedMessageOutlivesTimeout(t *testing.T) {
	_, msgC, sender := newFullQueue(t)

	result := make(chan error, 1)
	go func() {
		result <- NewMessage(queueWaitTestType, nil).WithTimeout(100 * time.Millisecond).SendAndWaitForQueue(sender)
	}()

	time.Sleep(20 * time.Millisecond)
	filler := <-msgC
	require.Equal(t, queueWaitFillerType, filler.Msg().ContentType)

	require.NoError(t, <-result)

	// drain well after the timeout: the tx loop must still write the message
	time.Sleep(200 * time.Millisecond)
	requireWritten(t, <-msgC)
}

func Test_SendAndWaitForQueue_TimesOutWhenQueueStaysFull(t *testing.T) {
	_, msgC, sender := newFullQueue(t)

	err := NewMessage(queueWaitTestType, nil).WithTimeout(20 * time.Millisecond).SendAndWaitForQueue(sender)

	require.True(t, IsTimeout(err), "expected a TimeoutError, got %v", err)
	require.Len(t, msgC, 1)
	require.Equal(t, queueWaitFillerType, (<-msgC).Msg().ContentType)
}

type testSender struct {
	sender Sender
	queue  chan Sendable
}

// newTestSenders returns each in-package Sender alongside its queue, which has one free slot.
func newTestSenders() map[string]testSender {
	msgC := make(chan Sendable, 1)
	outQueue := make(chan Sendable, 1)
	return map[string]testSender{
		"single ch sender": {sender: NewSingleChSender(NewSenderContext(), msgC), queue: msgC},
		"channel": {
			sender: &channelImpl{sequence: sequence.NewSequence(), outQueue: outQueue, closeNotify: make(chan struct{})},
			queue:  outQueue,
		},
	}
}

// endedContexts returns a context whose deadline has passed and one that has been cancelled.
func endedContexts(t *testing.T) map[string]context.Context {
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	t.Cleanup(cancelExpired)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	return map[string]context.Context{"expired": expired, "cancelled": cancelled}
}

func Test_Sender_EndedContextIsTimeout(t *testing.T) {
	for ctxName, ctx := range endedContexts(t) {
		for senderName, s := range newTestSenders() {
			t.Run(ctxName+"/"+senderName, func(t *testing.T) {
				err := s.sender.Send(NewMessage(queueWaitTestType, nil).WithContext(ctx).ToSendable())
				require.True(t, IsTimeout(err), "Send: expected a TimeoutError, got %v", err)
				require.ErrorIs(t, err, ctx.Err())

				queued, err := s.sender.TrySend(NewMessage(queueWaitTestType, nil).WithContext(ctx).ToSendable())
				require.False(t, queued)
				require.True(t, IsTimeout(err), "TrySend: expected a TimeoutError, got %v", err)
				require.ErrorIs(t, err, ctx.Err())

				require.Empty(t, s.queue)
			})
		}
	}
}

func Test_Sender_ErrorEnvelopeIsNotTimeout(t *testing.T) {
	envelopeErr := errors.New("failed to marshal")

	for name, s := range newTestSenders() {
		t.Run(name, func(t *testing.T) {
			err := s.sender.Send(NewErrorEnvelope(envelopeErr).ToSendable())
			require.Equal(t, envelopeErr, err)

			queued, err := s.sender.TrySend(NewErrorEnvelope(envelopeErr).ToSendable())
			require.False(t, queued)
			require.Equal(t, envelopeErr, err)

			require.Empty(t, s.queue)
		})
	}
}

func Test_TimeoutEnvelope_EndedBeforeSendIsTimeout(t *testing.T) {
	for ctxName, ctx := range endedContexts(t) {
		for senderName, s := range newTestSenders() {
			t.Run(ctxName+"/"+senderName, func(t *testing.T) {
				err := NewMessage(queueWaitTestType, nil).WithContext(ctx).SendAndWaitForQueue(s.sender)
				require.True(t, IsTimeout(err), "SendAndWaitForQueue: expected a TimeoutError, got %v", err)

				err = NewMessage(queueWaitTestType, nil).WithContext(ctx).SendAndWaitForWire(s.sender)
				require.True(t, IsTimeout(err), "SendAndWaitForWire: expected a TimeoutError, got %v", err)

				_, err = NewMessage(queueWaitTestType, nil).WithContext(ctx).SendForReply(s.sender)
				require.True(t, IsTimeout(err), "SendForReply: expected a TimeoutError, got %v", err)

				require.Empty(t, s.queue)
			})
		}
	}
}

func Test_SendAndWaitForQueue_ClosedSender(t *testing.T) {
	ctx := NewSenderContext()
	sender := NewSingleChSender(ctx, make(chan Sendable))
	close(ctx.GetCloseNotify())

	err := NewMessage(queueWaitTestType, nil).WithTimeout(time.Second).SendAndWaitForQueue(sender)

	require.ErrorIs(t, err, ClosedError{})
}

// Test_SendAndWaitForQueue_OutcomeMatchesQueue races the queue opening against the timeout. A nil result
// must leave a queued message that the tx loop writes; a timeout must leave nothing queued.
func Test_SendAndWaitForQueue_OutcomeMatchesQueue(t *testing.T) {
	const timeout = 2 * time.Millisecond
	var successes, timeouts int

	for i := 0; i < 500; i++ {
		_, msgC, sender := newFullQueue(t)

		drained := make(chan struct{})
		go func() {
			defer close(drained)
			time.Sleep(time.Duration(rand.Int64N(int64(2 * timeout))))
			<-msgC
		}()

		err := NewMessage(queueWaitTestType, nil).WithTimeout(timeout).SendAndWaitForQueue(sender)
		<-drained
		time.Sleep(2 * timeout)

		var queued Sendable
		select {
		case queued = <-msgC:
		default:
		}

		if err == nil {
			successes++
			require.NotNil(t, queued, "iteration %d: success reported but nothing queued", i)
			requireWritten(t, queued)
			continue
		}

		timeouts++
		require.True(t, IsTimeout(err), "iteration %d: expected a TimeoutError, got %v", i, err)
		require.Nil(t, queued, "iteration %d: timed out but the message was queued", i)
	}

	require.NotZero(t, successes, "the race never let a message in before the timeout")
	require.NotZero(t, timeouts, "the race never timed out")
}

// Test_WriteContext_NotInherited pins that only the queue-wait envelope supplies a write context. If
// Message or the plain envelope did, every type embedding them would inherit it and stop honouring its own
// Context override when written.
func Test_WriteContext_NotInherited(t *testing.T) {
	sendables := map[string]Sendable{
		"message":        NewMessage(queueWaitTestType, nil),
		"priority":       NewMessage(queueWaitTestType, nil).WithPriority(High).ToSendable(),
		"timeout":        NewMessage(queueWaitTestType, nil).WithTimeout(time.Second).ToSendable(),
		"context":        NewMessage(queueWaitTestType, nil).WithContext(context.Background()).ToSendable(),
		"wait for wire":  &sendWaitEnvelope{envelopeImpl: &envelopeImpl{}},
		"wait for reply": &replyEnvelope{envelopeImpl: &envelopeImpl{}},
	}

	for name, sendable := range sendables {
		_, ok := sendable.(writeContextProvider)
		require.False(t, ok, "%s must not provide a write context", name)
	}
}
