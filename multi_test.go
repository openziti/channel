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
	"fmt"
	"github.com/michaelquigley/pfxlog"
	"github.com/openziti/foundation/v2/goroutines"
	"github.com/openziti/identity"
	"github.com/openziti/transport/v2"
	"github.com/openziti/transport/v2/tcp"
	"github.com/stretchr/testify/require"
	"io"
	"math/rand"
	"sync/atomic"
	"testing"
	"time"
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

	bindHandlerF := func(priorityCh PriorityChannel, closeCallback func()) BindHandler {
		bindHandler := BindHandlerF(func(binding Binding) error {
			binding.AddReceiveHandlerF(100, func(m *Message, ch Channel) {
				// fmt.Printf("default msg received: %v\n", m.Body[0])
				if m.IsReply() {
					msgReplyCount.Add(1)
				} else {
					msgCount.Add(1)
					replyMsg := NewMessage(110, m.Body)
					replyMsg.ReplyTo(m)
					sendErr := replyMsg.WithTimeout(time.Second).SendAndWaitForWire(priorityCh.GetPrioritySender())
					handleAsyncErr(sendErr)
				}
			})

			binding.AddReceiveHandlerF(110, func(m *Message, ch Channel) {
				// fmt.Printf("priority msg received: %v\n", m.Body[0])
				if m.IsReply() {
					priorityReplyCount.Add(1)
				} else {
					priorityCount.Add(1)
					replyMsg := NewMessage(100, m.Body)
					replyMsg.ReplyTo(m)
					sendErr := replyMsg.WithTimeout(time.Second).SendAndWaitForWire(priorityCh.GetDefaultSender())
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
		return bindHandler
	}

	newMultiChannel := func(logicalName string, priorityChannel PriorityChannel, underlay Underlay, closeCallback func()) (MultiChannel, error) {
		multiConfig := MultiChannelConfig{
			LogicalName:     logicalName,
			Options:         DefaultOptions(),
			UnderlayHandler: priorityChannel,
			BindHandler:     bindHandlerF(priorityChannel, closeCallback),
			Underlay:        underlay,
		}
		return NewMultiChannel(&multiConfig)
	}

	multiListener := NewMultiListener(func(underlay Underlay, closeCallback func()) (MultiChannel, error) {
		wrapper := &TypeLoggingUnderlay{
			wrapped: underlay,
		}
		underlayHandler := NewListenerPriorityChannel(wrapper)
		return newMultiChannel("listener", underlayHandler, wrapper, closeCallback)
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
	}).(*classicDialer)

	headers := Headers{}
	headers.PutStringHeader(TypeHeader, "default")
	headers.PutBoolHeader(IsGroupedHeader, true)

	underlay, err := dialer.CreateWithHeaders(time.Second, headers)
	req.NoError(err)

	priorityCh := NewDialPriorityChannel(dialer, underlay)
	ch, err := newMultiChannel("dialer", priorityCh, underlay, nil)
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
		// fmt.Printf("iteration %d\n", i)
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

func newPriorityChannelBase(underlay Underlay) *priorityChannelBase {
	senderContext := NewSenderContext()

	defaultMsgChan := make(chan Sendable, 4)
	priorityMsgChan := make(chan Sendable, 4)
	retryMsgChan := make(chan Sendable, 4)

	result := &priorityChannelBase{
		SenderContext:   senderContext,
		id:              underlay.ConnectionId(),
		prioritySender:  NewSingleChSender(senderContext, priorityMsgChan),
		defaultSender:   NewSingleChSender(senderContext, defaultMsgChan),
		priorityMsgChan: priorityMsgChan,
		defaultMsgChan:  defaultMsgChan,
		retryMsgChan:    retryMsgChan,
	}
	return result
}

type priorityChannelBase struct {
	id string
	SenderContext
	prioritySender Sender
	defaultSender  Sender

	priorityMsgChan chan Sendable
	defaultMsgChan  chan Sendable
	retryMsgChan    chan Sendable
}

func (self *priorityChannelBase) GetDefaultSender() Sender {
	return self.defaultSender
}

func (self *priorityChannelBase) GetPrioritySender() Sender {
	return self.prioritySender
}

func (self *priorityChannelBase) GetNextMsgDefault(notifier *CloseNotifier) (Sendable, error) {
	select {
	case msg := <-self.defaultMsgChan:
		return msg, nil
	case msg := <-self.priorityMsgChan:
		return msg, nil
	case msg := <-self.retryMsgChan:
		return msg, nil
	case <-self.GetCloseNotify():
		return nil, io.EOF
	case <-notifier.GetCloseNotify():
		return nil, io.EOF
	}
}

func (self *priorityChannelBase) GetNextPriorityMsg(notifier *CloseNotifier) (Sendable, error) {
	select {
	case msg := <-self.priorityMsgChan:
		return msg, nil
	case msg := <-self.retryMsgChan:
		return msg, nil
	case <-self.GetCloseNotify():
		return nil, io.EOF
	case <-notifier.GetCloseNotify():
		return nil, io.EOF
	}
}

func (self *priorityChannelBase) GetMessageSource(underlay Underlay) MessageSourceF {
	if GetUnderlayType(underlay) == "priority" {
		return self.GetNextPriorityMsg
	}
	return self.GetNextMsgDefault
}

func (self *priorityChannelBase) HandleTxFailed(_ Underlay, sendable Sendable) bool {
	select {
	case self.retryMsgChan <- sendable:
		return true
	case self.defaultMsgChan <- sendable:
		return true
	default:
		return false
	}
}

func (self *priorityChannelBase) HandleUnderlayAccepted(ch MultiChannel, underlay Underlay) {
	pfxlog.Logger().
		WithField("id", ch.Label()).
		WithField("underlays", ch.GetUnderlayCountsByType()).
		WithField("underlayType", GetUnderlayType(underlay)).
		Info("underlay added")
}

func (self *priorityChannelBase) CloseRandom(ch MultiChannel) {
	mc := ch.(*multiChannelImpl)
	mc.lock.Lock()
	defer mc.lock.Unlock()
	underlays := mc.underlays.Value()
	idx := rand.Intn(len(underlays))
	underlay := underlays[idx]
	_ = underlay.Close()
}

func NewDialPriorityChannel(dialer *classicDialer, underlay Underlay) PriorityChannel {
	result := &dialPriorityChannel{
		priorityChannelBase: *newPriorityChannelBase(underlay),
		dialer:              dialer,
	}

	result.constraints.AddConstraint("default", 2, 1)
	result.constraints.AddConstraint("priority", 1, 0)

	return result
}

type PriorityChannel interface {
	UnderlayHandler
	GetPrioritySender() Sender
	CloseRandom(ch MultiChannel)
}

type dialPriorityChannel struct {
	priorityChannelBase
	dialer      *classicDialer
	constraints UnderlayConstraints
}

func (self *dialPriorityChannel) Start(channel MultiChannel) {
	self.constraints.Apply(channel, self)
}

func (self *dialPriorityChannel) HandleUnderlayClose(ch MultiChannel, underlay Underlay) {
	pfxlog.Logger().
		WithField("id", ch.Label()).
		WithField("underlays", ch.GetUnderlayCountsByType()).
		WithField("underlayType", GetUnderlayType(underlay)).
		WithField("underlayAddr", fmt.Sprintf("%p", underlay)).
		Info("underlay closed")
	self.constraints.Apply(ch, self)
}

func (self *dialPriorityChannel) DialFailed(_ MultiChannel, _ string, attempt int) {
	delay := 2 * time.Duration(attempt) * time.Second
	if delay > time.Minute {
		delay = time.Minute
	}
	time.Sleep(delay)
}

func (self *dialPriorityChannel) CreateGroupedUnderlay(groupId string, groupSecret []byte, underlayType string, timeout time.Duration) (Underlay, error) {
	underlay, err := self.dialer.CreateWithHeaders(timeout, map[int32][]byte{
		TypeHeader:         []byte(underlayType),
		ConnectionIdHeader: []byte(groupId),
		GroupSecretHeader:  groupSecret,
		IsGroupedHeader:    {1},
	})
	if err != nil {
		return nil, err
	}

	return &TypeLoggingUnderlay{
		wrapped: underlay,
	}, nil
}

func NewListenerPriorityChannel(underlay Underlay) PriorityChannel {
	result := &listenerPriorityChannel{
		priorityChannelBase: *newPriorityChannelBase(underlay),
	}

	result.constraints.AddConstraint("default", 2, 1)
	result.constraints.AddConstraint("priority", 1, 0)

	return result
}

type listenerPriorityChannel struct {
	priorityChannelBase
	constraints UnderlayConstraints
}

func (self *listenerPriorityChannel) Start(MultiChannel) {
}

func (self *listenerPriorityChannel) HandleUnderlayClose(channel MultiChannel, underlay Underlay) {
	pfxlog.Logger().WithField("underlays", channel.GetUnderlayCountsByType()).
		WithField("underlayType", GetUnderlayType(underlay)).Info("underlay closed")
	self.constraints.CheckStateValid(channel, true)
}
