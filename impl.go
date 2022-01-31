/*
	Copyright NetFoundry, Inc.

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
	"container/heap"
	"crypto/x509"
	"fmt"
	"github.com/michaelquigley/pfxlog"
	"github.com/openziti/foundation/identity/identity"
	"github.com/openziti/foundation/transport"
	"github.com/openziti/foundation/util/concurrenz"
	"github.com/openziti/foundation/util/info"
	"github.com/openziti/foundation/util/sequence"
	"github.com/pkg/errors"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

const (
	flagClosed    = 0
	flagRxStarted = 1
)

var connectionSeq = sequence.NewSequence()

func NextConnectionId() (string, error) {
	return connectionSeq.NextHash()
}

type channelImpl struct {
	logicalName       string
	underlay          Underlay
	options           *Options
	sequence          *sequence.Sequence
	outQueue          chan Sendable
	outPriority       *priorityHeap
	waiters           waiterMap
	flags             concurrenz.AtomicBitSet
	closeNotify       chan struct{}
	peekHandlers      []PeekHandler
	transformHandlers []TransformHandler
	receiveHandlers   map[int32]ReceiveHandler
	errorHandlers     []ErrorHandler
	closeHandlers     []CloseHandler
	userData          interface{}
	lastRead          int64
}

func NewChannel(logicalName string, underlayFactory UnderlayFactory, bindHandler BindHandler, options *Options) (Channel, error) {
	return NewChannelWithTransportConfiguration(logicalName, underlayFactory, bindHandler, options, nil)
}

func NewChannelWithTransportConfiguration(logicalName string, underlayFactory UnderlayFactory, bindHandler BindHandler, options *Options, tcfg transport.Configuration) (Channel, error) {
	impl := &channelImpl{
		logicalName:     logicalName,
		options:         options,
		sequence:        sequence.NewSequence(),
		outQueue:        make(chan Sendable, 4),
		outPriority:     &priorityHeap{},
		receiveHandlers: map[int32]ReceiveHandler{},
		closeNotify:     make(chan struct{}),
	}

	heap.Init(impl.outPriority)
	impl.AddTypedReceiveHandler(&pingHandler{})

	timeout := time.Duration(0)
	if options != nil {
		timeout = time.Duration(options.ConnectTimeoutMs) * time.Millisecond
	}

	underlay, err := underlayFactory.Create(timeout, tcfg)
	if err != nil {
		return nil, err
	}
	impl.underlay = underlay

	if err := bind(bindHandler, impl); err != nil {
		return nil, err
	}

	impl.startMultiplex()

	return impl, nil
}

func AcceptNextChannel(logicalName string, underlayFactory UnderlayFactory, bindHandler BindHandler, options *Options, tcfg transport.Configuration) error {
	underlay, err := underlayFactory.Create(options.ConnectTimeout(), tcfg)
	if err != nil {
		return err
	}
	go acceptAsync(logicalName, underlay, bindHandler, options)
	return nil
}

func acceptAsync(logicalName string, underlay Underlay, bindHandler BindHandler, options *Options) {
	impl := &channelImpl{
		underlay:        underlay,
		logicalName:     logicalName,
		options:         options,
		sequence:        sequence.NewSequence(),
		outQueue:        make(chan Sendable, 4),
		outPriority:     &priorityHeap{},
		receiveHandlers: map[int32]ReceiveHandler{},
		closeNotify:     make(chan struct{}),
	}

	heap.Init(impl.outPriority)
	impl.AddTypedReceiveHandler(&pingHandler{})

	//goland:noinspection GoNilness
	if err := bind(bindHandler, impl); err != nil {
		pfxlog.Logger().WithError(err).Errorf("failure accepting channel %v with underlay %v", impl.Label(), underlay.Label())
		return
	}

	impl.startMultiplex()
}

func (channel *channelImpl) Id() *identity.TokenId {
	return channel.underlay.Id()
}

func (channel *channelImpl) LogicalName() string {
	return channel.logicalName
}

func (channel *channelImpl) SetLogicalName(logicalName string) {
	channel.logicalName = logicalName
}

func (channel *channelImpl) ConnectionId() string {
	return channel.underlay.ConnectionId()
}

func (channel *channelImpl) Certificates() []*x509.Certificate {
	return channel.underlay.Certificates()
}

func (channel *channelImpl) Label() string {
	if channel.underlay != nil {
		return fmt.Sprintf("ch{%s}->%s", channel.LogicalName(), channel.underlay.Label())
	} else {
		return fmt.Sprintf("ch{%s}->{}", channel.LogicalName())
	}
}

func (channel *channelImpl) GetChannel() Channel {
	return channel
}

func (channel *channelImpl) Bind(h BindHandler) error {
	return h.BindChannel(channel)
}

func (channel *channelImpl) AddPeekHandler(h PeekHandler) {
	channel.peekHandlers = append(channel.peekHandlers, h)
}

func (channel *channelImpl) AddTransformHandler(h TransformHandler) {
	channel.transformHandlers = append(channel.transformHandlers, h)
}

func (channel *channelImpl) AddTypedReceiveHandler(h TypedReceiveHandler) {
	channel.receiveHandlers[h.ContentType()] = h
}

func (channel *channelImpl) AddReceiveHandler(contentType int32, h ReceiveHandler) {
	channel.receiveHandlers[contentType] = h
}

func (channel *channelImpl) AddReceiveHandlerF(contentType int32, h ReceiveHandlerF) {
	channel.AddReceiveHandler(contentType, h)
}

func (channel *channelImpl) AddErrorHandler(h ErrorHandler) {
	channel.errorHandlers = append(channel.errorHandlers, h)
}

func (channel *channelImpl) AddCloseHandler(h CloseHandler) {
	channel.closeHandlers = append(channel.closeHandlers, h)
}

func (channel *channelImpl) SetUserData(data interface{}) {
	channel.userData = data
}

func (channel *channelImpl) GetUserData() interface{} {
	return channel.userData
}

func (channel *channelImpl) Close() error {
	if channel.flags.CompareAndSet(flagClosed, false, true) {
		pfxlog.ContextLogger(channel.Label()).Debug("closing channel")

		close(channel.closeNotify)

		for _, peekHandler := range channel.peekHandlers {
			peekHandler.Close(channel)
		}

		if len(channel.closeHandlers) > 0 {
			for _, closeHandler := range channel.closeHandlers {
				closeHandler.HandleClose(channel)
			}
		} else {
			pfxlog.ContextLogger(channel.Label()).Debug("no close handlers")
		}

		return channel.underlay.Close()
	}

	return nil
}

func (channel *channelImpl) IsClosed() bool {
	return channel.flags.IsSet(flagClosed)
}

func (channel *channelImpl) Send(s Sendable) error {
	if err := s.Context().Err(); err != nil {
		return err
	}

	s.SetSequence(int32(channel.sequence.Next()))

	select {
	case <-s.Context().Done():
		if err := s.Context().Err(); err != nil {
			return TimeoutError{errors.Wrap(err, "timeout waiting for message reply")}
		}
		return errors.New("timeout waiting to put message in send queue")
	case <-channel.closeNotify:
		return errors.New("channel closed")
	case channel.outQueue <- s:
	}
	return nil
}

func (channel *channelImpl) Underlay() Underlay {
	return channel.underlay
}

func (channel *channelImpl) startMultiplex() {
	for _, peekHandler := range channel.peekHandlers {
		peekHandler.Connect(channel, "")
	}

	if channel.options == nil || !channel.options.DelayRxStart {
		go channel.rxer()
	}
	go channel.txer()
}

func (channel *channelImpl) StartRx() {
	go channel.rxer()
}

func (channel *channelImpl) rxer() {
	if !channel.flags.CompareAndSet(flagRxStarted, false, true) {
		return
	}

	log := pfxlog.ContextLogger(channel.Label())
	log.Debug("started")
	defer log.Debug("exited")

	defer func() {
		if r := recover(); r != nil {
			panic(r)
		}
		_ = channel.Close()
	}()

	defer channel.waiters.clear()

	var replyCounter uint32

	for {
		m, err := channel.underlay.Rx()
		if err != nil {
			if err == io.EOF {
				log.Debug("EOF")
			} else {
				log.Errorf("rx error (%s)", err)
			}
			return
		}

		now := info.NowInMilliseconds()
		atomic.StoreInt64(&channel.lastRead, now)

		for _, transformHandler := range channel.transformHandlers {
			transformHandler.Rx(m, channel)
		}

		for _, peekHandler := range channel.peekHandlers {
			peekHandler.Rx(m, channel)
		}

		handled := false
		if m.IsReply() {
			replyCounter++
			if replyCounter%100 == 0 && channel.waiters.Size() > 1000 {
				channel.waiters.reapExpired(now)
			}
			replyFor := m.ReplyFor()
			if replyReceiver := channel.waiters.RemoveWaiter(replyFor); replyReceiver != nil {
				log.Tracef("waiter found for message. type [%v], sequence [%v], replyFor [%v]", m.ContentType, m.sequence, replyFor)
				replyReceiver.AcceptReply(m)
				handled = true
			} else {
				log.Debugf("no waiter for message. type [%v], sequence [%v], replyFor [%v]", m.ContentType, m.sequence, replyFor)
			}
		}

		if !handled {
			if receiveHandler, found := channel.receiveHandlers[m.ContentType]; found {
				receiveHandler.HandleReceive(m, channel)

			} else if anyHandler, found := channel.receiveHandlers[AnyContentType]; found {
				anyHandler.HandleReceive(m, channel)

			} else {
				log.Warnf("dropped message [%d]", m.ContentType)
			}
		}
	}
}

func (channel *channelImpl) txer() {
	log := pfxlog.ContextLogger(channel.Label())
	defer log.Debug("exited")
	log.Debug("started")

	defer func() { _ = channel.Close() }()

	var writeTimeout time.Duration
	if channel.options != nil {
		writeTimeout = channel.options.WriteTimeout
	}

	for {
		done := false
		selecting := true

		count := 0

		pm, ok := <-channel.outQueue
		if ok {
			heap.Push(channel.outPriority, pm)
			count++
		} else {
			done = true
			selecting = false
		}

		for selecting && count < 64 {
			select {
			case pm, ok := <-channel.outQueue:
				if ok {
					heap.Push(channel.outPriority, pm)
					count++
				} else {
					done = true
					selecting = false
				}
			default:
				selecting = false
			}
		}

		for channel.outPriority.Len() > 0 {
			sendable := heap.Pop(channel.outPriority).(Sendable)
			sendListener := sendable.SendListener()
			m := sendable.Msg()

			if err := sendable.Context().Err(); err != nil {
				sendListener.NotifyErr(TimeoutError{err})
				continue
			}

			sendListener.NotifyBeforeWrite()

			if m == nil { // allow nil message in Sendable so we can send tracers to check time from send to write
				continue
			}

			for _, transformHandler := range channel.transformHandlers {
				transformHandler.Tx(m, channel)
			}

			channel.waiters.AddWaiter(sendable)

			var err error
			if writeTimeout > 0 {
				if err = channel.underlay.SetWriteTimeout(writeTimeout); err != nil {
					log.WithError(err).Errorf("unable to set write timeout")
					sendListener.NotifyErr(err)
					done = true
				}
			}

			if !done {
				err = channel.underlay.Tx(m)
				if err != nil {
					log.WithError(err).Errorf("write error")
					sendListener.NotifyErr(err)
					done = true
				} else {
					for _, peekHandler := range channel.peekHandlers {
						peekHandler.Tx(m, channel)
					}
				}
			}

			if err != nil {
				for _, errorHandler := range channel.errorHandlers {
					errorHandler.HandleError(err, channel)
				}
			}

			sendListener.NotifyAfterWrite()
		}

		if done {
			return
		}
	}
}

func (ch *channelImpl) GetTimeSinceLastRead() time.Duration {
	return time.Duration(info.NowInMilliseconds()-atomic.LoadInt64(&ch.lastRead)) * time.Millisecond
}

type waiter struct {
	replyReceiver ReplyReceiver
	ttlMs         int64
}

type waiterMap struct {
	m    sync.Map
	size int32
}

func (self *waiterMap) Size() int32 {
	return atomic.LoadInt32(&self.size)
}

func (self *waiterMap) AddWaiter(sendable Sendable) {
	if replyReceiver := sendable.ReplyReceiver(); replyReceiver != nil {
		w := &waiter{
			replyReceiver: replyReceiver,
		}

		if deadline, hasDeadline := sendable.Context().Deadline(); hasDeadline {
			w.ttlMs = deadline.UnixMilli()
		} else {
			w.ttlMs = info.NowInMilliseconds() + 30_000
		}

		self.m.Store(sendable.Msg().Sequence(), w)
		atomic.AddInt32(&self.size, 1)
	}
}

func (self *waiterMap) RemoveWaiter(seq int32) ReplyReceiver {
	if result, found := self.m.LoadAndDelete(seq); found {
		w := result.(*waiter)
		atomic.AddInt32(&self.size, -1)
		return w.replyReceiver
	}
	return nil
}

func (self *waiterMap) reapExpired(now int64) {
	var deleteCount int32
	self.m.Range(func(key, value interface{}) bool {
		if w, ok := value.(*waiter); !ok || w.ttlMs < now {
			self.m.Delete(key)
			deleteCount++
		}
		return true
	})
	atomic.AddInt32(&self.size, -deleteCount)
}

func (self *waiterMap) clear() {
	self.m.Range(func(k, v interface{}) bool {
		self.m.Delete(k)
		return true
	})
}

func bind(bindHandler BindHandler, binding Binding) error {
	if bindHandler == nil {
		return nil
	}

	if err := bindHandler.BindChannel(binding); err != nil {
		if closeErr := binding.GetChannel().Close(); closeErr != nil {
			pfxlog.ContextLogger(binding.GetChannel().Label()).WithError(err).Warn("error closing channel after bind failure")
		}
		return err
	}

	return nil
}
