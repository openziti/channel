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
	"errors"
	"fmt"
	"math/rand"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openziti/foundation/v2/goroutines"
	"github.com/openziti/identity"
	"github.com/openziti/transport/v2"
	"github.com/openziti/transport/v2/tcp"
	"github.com/stretchr/testify/require"
)

func Test_MultiUnderlayChannels(t *testing.T) {
	transport.AddAddressParser(tcp.AddressParser{})
	req := require.New(t)
	listenAddr, err := transport.ParseAddress("tcp:0.0.0.0:6767")
	req.NoError(err)

	dialAddr, err := transport.ParseAddress("tcp:127.0.0.1:6767")
	req.NoError(err)

	var msgCount atomic.Int32
	var priorityCount atomic.Int32
	var msgReplyCount atomic.Int32
	var priorityReplyCount atomic.Int32

	errC := make(chan error, 10)

	handleAsyncErr := func(err error) {
		if err != nil {
			select {
			case errC <- err:
			default:
			}
		}
	}

	bindHandlerF := func(closeCallback func()) TypedBindHandler[PriorityChannel] {
		return TypedBindHandlerF[PriorityChannel](func(binding TypedBinding[PriorityChannel]) error {
			binding.AddTypedReceiveHandlerF(100, func(m *Message, ch Channel, senders PriorityChannel) {
				// fmt.Printf("default msg received: %v\n", m.Body[0])
				if m.IsReply() {
					msgReplyCount.Add(1)
				} else {
					msgCount.Add(1)
					replyMsg := NewMessage(110, m.Body)
					replyMsg.ReplyTo(m)
					sendErr := replyMsg.WithTimeout(time.Second).SendAndWaitForWire(senders.GetPrioritySender())
					handleAsyncErr(sendErr)
				}
			})

			binding.AddTypedReceiveHandlerF(110, func(m *Message, ch Channel, senders PriorityChannel) {
				// fmt.Printf("priority msg received: %v\n", m.Body[0])
				if m.IsReply() {
					priorityReplyCount.Add(1)
				} else {
					priorityCount.Add(1)
					replyMsg := NewMessage(100, m.Body)
					replyMsg.ReplyTo(m)
					sendErr := replyMsg.WithTimeout(time.Second).SendAndWaitForWire(senders.GetDefaultSender())
					handleAsyncErr(sendErr)
				}
			})

			if closeCallback != nil {
				binding.AddCloseHandler(CloseHandlerF(func(ch Channel) {
					closeCallback()
				}))
			}

			return nil
		})
	}

	newChannel := func(logicalName string, priorityChannel PriorityChannel, underlay Underlay, closeCallback func()) (Channel, error) {
		multiConfig := Config{
			LogicalName:           logicalName,
			Options:               DefaultOptions(),
			Binder:                MakeTypedBinder[PriorityChannel](priorityChannel, bindHandlerF(closeCallback)),
			Underlay:              underlay,
			Senders:               priorityChannel,
			MessageSourceProvider: priorityChannel.GetMessageSourceProvider(),
			DialPolicy:            priorityChannel.GetDialPolicy(),
			Constraints:           priorityChannel.GetConstraints(),
		}
		return NewChannel(&multiConfig)
	}

	multiListener := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		wrapper := &TypeLoggingUnderlay{
			wrapped: underlay,
		}
		handler := NewListenerPriorityChannel()
		return newChannel("listener", handler, wrapper, closeCallback)
	}, func(underlay Underlay) error {
		return errors.New("this implementation only accepts grouped channel")
	})

	listenerConfig := ListenerConfig{
		ConnectOptions: DefaultConnectOptions(),
		PoolConfigurator: func(config *goroutines.PoolConfig) {
			config.MinWorkers = 1
			config.MaxWorkers = 1
		},
	}

	acceptWrapper := func(underlay Underlay) {
		wrapper := &TypeLoggingUnderlay{
			wrapped: underlay,
		}
		multiListener.AcceptUnderlay(wrapper)
	}
	listener, err := NewClassicListenerF(&identity.TokenId{Token: "test-server"}, listenAddr, listenerConfig, acceptWrapper)
	req.NoError(err)
	defer func() { _ = listener.Close() }()

	dialer := NewClassicDialer(DialerConfig{
		Identity: &identity.TokenId{Token: "test-client"},
		Endpoint: dialAddr,
	})

	headers := Headers{}
	headers.PutStringHeader(TypeHeader, "default")
	headers.PutBoolHeader(IsGroupedHeader, true)
	headers.PutBoolHeader(IsFirstGroupConnection, true)

	underlay, err := dialer.CreateWithHeaders(time.Second, headers)
	req.NoError(err)

	priorityCh := NewDialPriorityChannel(dialer)
	ch, err := newChannel("dialer", priorityCh, underlay, nil)
	req.NoError(err)
	defer func() { _ = ch.Close() }()

	closeNotify := make(chan struct{})
	defer close(closeNotify)

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				priorityCh.CloseRandom(ch)
			case <-closeNotify:
				return
			}
		}
	}()

	for i := 0; i < 3_000; i++ {
		fmt.Printf("iteration %d\n", i)
		msg := NewMessage(100, []byte{byte(i)})
		req.NoError(msg.WithTimeout(time.Second).SendAndWaitForWire(priorityCh.GetDefaultSender()))
		time.Sleep(time.Millisecond)
	}

	var asyncErr error
	select {
	case asyncErr = <-errC:
	default:
	}
	req.NoError(asyncErr, "no async errors should have occurred")
}

func newPriorityChannelBase() *priorityChannelBase {
	ctx := NewSenderContext()

	defaultQ := NewMessageQueue(ctx, 4)
	priorityQ := NewMessageQueue(ctx, 4)
	retryQ := NewMessageQueue(ctx, 4)

	closeNotify := ctx.GetCloseNotify()

	msgSourceProvider := NewSimpleMessageSourceProvider(MakeThreeQueueMessageSource(closeNotify, defaultQ.C, priorityQ.C, retryQ.C))
	msgSourceProvider.AddSource("priority", MakeTwoQueueMessageSource(closeNotify, priorityQ.C, retryQ.C))

	return &priorityChannelBase{
		SenderContext:         ctx,
		defaultQueue:          defaultQ,
		priorityQueue:         priorityQ,
		retryQueue:            retryQ,
		handleTxFailed:        RetryToQueues(retryQ, defaultQ),
		messageSourceProvider: msgSourceProvider,
	}
}

// priorityChannelBase implements Senders and provides message source routing.
type priorityChannelBase struct {
	SenderContext
	defaultQueue          *MessageQueue
	priorityQueue         *MessageQueue
	retryQueue            *MessageQueue
	handleTxFailed        func(string, Sendable) bool
	messageSourceProvider *SimpleMessageSourceProvider
}

func (self *priorityChannelBase) GetMessageSourceProvider() MessageSourceProvider {
	return self.messageSourceProvider
}

func (self *priorityChannelBase) GetDefaultSender() Sender {
	return self.defaultQueue.Sender
}

func (self *priorityChannelBase) GetPrioritySender() Sender {
	return self.priorityQueue.Sender
}

func (self *priorityChannelBase) HandleTxFailed(underlayType string, sendable Sendable) bool {
	return self.handleTxFailed(underlayType, sendable)
}

func (self *priorityChannelBase) CloseRandom(ch Channel) {
	mc := ch.(*channelImpl)
	mc.lock.Lock()
	defer mc.lock.Unlock()
	underlays := mc.underlays.GetAll()
	if len(underlays) > 0 {
		idx := rand.Intn(len(underlays))
		underlay := underlays[idx]
		_ = underlay.Close()
	}
}

// PriorityChannel is the test interface for priority-aware multi-underlay channels.
type PriorityChannel interface {
	Senders
	GetPrioritySender() Sender
	GetMessageSourceProvider() MessageSourceProvider
	GetDialPolicy() DialPolicy
	GetConstraints() map[string]UnderlayConstraint
	CloseRandom(ch Channel)
}

func NewDialPriorityChannel(dialer DialUnderlayFactory) PriorityChannel {
	base := newPriorityChannelBase()

	backoffConfig := DefaultBackoffConfig
	backoffConfig.MinStableDuration = 0 // test closes underlays randomly, don't penalize short-lived connections

	return &dialPriorityChannel{
		priorityChannelBase: base,
		dialPolicy:          NewBackoffDialPolicyWithConfig(dialer, backoffConfig),
		constraints: map[string]UnderlayConstraint{
			"default":  {Desired: 2, Min: 1},
			"priority": {Desired: 1, Min: 0},
		},
	}
}

type dialPriorityChannel struct {
	*priorityChannelBase
	dialPolicy  DialPolicy
	constraints map[string]UnderlayConstraint
}

func (self *dialPriorityChannel) GetDialPolicy() DialPolicy {
	return self.dialPolicy
}

func (self *dialPriorityChannel) GetConstraints() map[string]UnderlayConstraint {
	return self.constraints
}

func NewListenerPriorityChannel() PriorityChannel {
	return &listenerPriorityChannel{
		priorityChannelBase: newPriorityChannelBase(),
		constraints: map[string]UnderlayConstraint{
			"default":  {Desired: 2, Min: 1},
			"priority": {Desired: 1, Min: 0},
		},
	}
}

type listenerPriorityChannel struct {
	*priorityChannelBase
	constraints map[string]UnderlayConstraint
}

func (self *listenerPriorityChannel) GetDialPolicy() DialPolicy {
	return nil
}

func (self *listenerPriorityChannel) GetConstraints() map[string]UnderlayConstraint {
	return self.constraints
}
