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
	"fmt"
	"github.com/google/uuid"
	"github.com/michaelquigley/pfxlog"
	"github.com/openziti/foundation/v2/concurrenz"
	"github.com/openziti/foundation/v2/info"
	"github.com/openziti/foundation/v2/sequence"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type MultiChannelConfig struct {
	LogicalName     string
	DefaultSender   Sender
	Options         *Options
	UnderlayHandler UnderlayHandler
	BindHandler     BindHandler
	Underlay        Underlay
}

type multiChannelImpl struct {
	// Note: if altering this struct, be sure to account for 64 bit alignment on 32 bit arm arch
	// https://pkg.go.dev/sync/atomic#pkg-note-BUG
	// https://github.com/golang/go/issues/36606
	lastRead int64

	ownerId     string
	channelId   string
	logicalName string
	certs       concurrenz.AtomicValue[[]*x509.Certificate]

	options           *Options
	sequence          *sequence.Sequence
	waiters           waiterMap
	flags             concurrenz.AtomicBitSet
	closeNotify       chan struct{}
	peekHandlers      []PeekHandler
	transformHandlers []TransformHandler
	receiveHandlers   map[int32]ReceiveHandler
	errorHandlers     []ErrorHandler
	closeHandlers     []CloseHandler
	underlayHandler   UnderlayHandler
	userData          interface{}
	replyCounter      uint32

	defaultSender Sender

	lock      sync.Mutex
	underlays concurrenz.CopyOnWriteSlice[Underlay]
}

func NewMultiChannel(config *MultiChannelConfig) (MultiChannel, error) {
	if config.UnderlayHandler == nil {
		return nil, fmt.Errorf("no underlay handler configured for multi channel %s", config.LogicalName)
	}

	if config.Underlay == nil {
		return nil, errors.New("unable to initialize multi channel (initialization produced zero underlays)")
	}

	impl := &multiChannelImpl{
		channelId:       uuid.New().String(),
		logicalName:     config.LogicalName,
		options:         config.Options,
		sequence:        sequence.NewSequence(),
		receiveHandlers: map[int32]ReceiveHandler{},
		closeNotify:     make(chan struct{}),
		defaultSender:   config.DefaultSender,
		underlayHandler: config.UnderlayHandler,
	}

	impl.ownerId = config.Underlay.Id()
	impl.certs.Store(config.Underlay.Certificates())

	if err := bind(config.BindHandler, impl); err != nil {
		for _, u := range impl.underlays.Value() {
			if closeErr := u.Close(); closeErr != nil {
				if !errors.Is(closeErr, net.ErrClosed) {
					pfxlog.ContextLogger(impl.Label()).WithError(err).Warn("error closing underlay")
				}
			}
		}
		return nil, err
	}

	go impl.Rxer(config.Underlay)
	go impl.Txer(config.Underlay)

	return impl, nil
}

func (self *multiChannelImpl) AcceptUnderlay(underlay Underlay) bool {
	self.lock.Lock()
	defer self.lock.Unlock()
	if !self.IsClosed() {
		self.certs.Store(underlay.Certificates())
		self.underlays.Append(underlay)

		go self.Rxer(underlay)
		go self.Txer(underlay)
		return true
	}
	return false
}

func (self *multiChannelImpl) GetUnderlayCountsByType() map[string]int {
	result := map[string]int{}
	for _, u := range self.underlays.Value() {
		underlayType := GetUnderlayType(u)
		result[underlayType]++
	}
	return result
}

func (self *multiChannelImpl) Send(s Sendable) error {
	return self.defaultSender.Send(s)
}

func (self *multiChannelImpl) TrySend(s Sendable) (bool, error) {
	return self.defaultSender.TrySend(s)
}

func (self *multiChannelImpl) Id() string {
	return self.ownerId
}

func (self *multiChannelImpl) LogicalName() string {
	return self.logicalName
}

func (self *multiChannelImpl) SetLogicalName(logicalName string) {
	self.logicalName = logicalName
}

func (self *multiChannelImpl) ConnectionId() string {
	return self.channelId
}

func (self *multiChannelImpl) Certificates() []*x509.Certificate {
	return self.certs.Load()
}

func (self *multiChannelImpl) Label() string {
	if u := self.Underlay(); u != nil {
		return fmt.Sprintf("ch{%s}->%s", self.LogicalName(), u.Label())
	} else {
		return fmt.Sprintf("ch{%s}->{}", self.LogicalName())
	}
}

func (self *multiChannelImpl) GetOptions() *Options {
	return self.options
}

func (self *multiChannelImpl) GetChannel() Channel {
	return self
}

func (self *multiChannelImpl) Bind(h BindHandler) error {
	return h.BindChannel(self)
}

func (self *multiChannelImpl) AddPeekHandler(h PeekHandler) {
	self.peekHandlers = append(self.peekHandlers, h)
}

func (self *multiChannelImpl) AddTransformHandler(h TransformHandler) {
	self.transformHandlers = append(self.transformHandlers, h)
}

func (self *multiChannelImpl) AddTypedReceiveHandler(h TypedReceiveHandler) {
	self.receiveHandlers[h.ContentType()] = h
}

func (self *multiChannelImpl) AddReceiveHandler(contentType int32, h ReceiveHandler) {
	self.receiveHandlers[contentType] = h
}

func (self *multiChannelImpl) AddReceiveHandlerF(contentType int32, h ReceiveHandlerF) {
	self.AddReceiveHandler(contentType, h)
}

func (self *multiChannelImpl) AddErrorHandler(h ErrorHandler) {
	self.errorHandlers = append(self.errorHandlers, h)
}

func (self *multiChannelImpl) AddCloseHandler(h CloseHandler) {
	self.closeHandlers = append(self.closeHandlers, h)
}

func (self *multiChannelImpl) SetUserData(data interface{}) {
	self.userData = data
}

func (self *multiChannelImpl) GetUserData() interface{} {
	return self.userData
}

func (self *multiChannelImpl) Close() error {
	if self.flags.CompareAndSet(flagClosed, false, true) {
		pfxlog.ContextLogger(self.Label()).Debug("closing channel")

		close(self.closeNotify)

		for _, peekHandler := range self.peekHandlers {
			peekHandler.Close(self)
		}

		if len(self.closeHandlers) > 0 {
			for _, closeHandler := range self.closeHandlers {
				closeHandler.HandleClose(self)
			}
		} else {
			pfxlog.ContextLogger(self.Label()).Debug("no close handlers")
		}

		var errs []error
		for _, u := range self.underlays.Value() {
			if err := u.Close(); err != nil {
				errs = append(errs, err)
			}
		}

		return errors.Join(errs...)
	}

	return nil
}

func (self *multiChannelImpl) IsClosed() bool {
	return self.flags.IsSet(flagClosed)
}

func (self *multiChannelImpl) Underlay() Underlay {
	if underlays := self.underlays.Value(); len(underlays) > 0 {
		return underlays[0]
	}
	return nil
}

func (self *multiChannelImpl) Rx(m *Message) {
	log := pfxlog.ContextLogger(self.Label())

	now := info.NowInMilliseconds()
	atomic.StoreInt64(&self.lastRead, now)

	for _, transformHandler := range self.transformHandlers {
		transformHandler.Rx(m, self)
	}

	for _, peekHandler := range self.peekHandlers {
		peekHandler.Rx(m, self)
	}

	handled := false
	if m.IsReply() {
		self.replyCounter++
		if self.replyCounter%100 == 0 && self.waiters.Size() > 1000 {
			self.waiters.reapExpired(now)
		}
		replyFor := m.ReplyFor()
		if replyReceiver := self.waiters.RemoveWaiter(replyFor); replyReceiver != nil {
			log.Tracef("waiter found for message. type [%v], sequence [%v], replyFor [%v]", m.ContentType, m.sequence, replyFor)
			replyReceiver.AcceptReply(m)
			handled = true
		} else {
			log.Debugf("no waiter for message. type [%v], sequence [%v], replyFor [%v]", m.ContentType, m.sequence, replyFor)
		}
	}

	if !handled {
		if receiveHandler, found := self.receiveHandlers[m.ContentType]; found {
			receiveHandler.HandleReceive(m, self)

		} else if anyHandler, found := self.receiveHandlers[AnyContentType]; found {
			anyHandler.HandleReceive(m, self)
		} else {
			log.Warnf("dropped message. type [%d], sequence [%v], replyFor [%v]", m.ContentType, m.sequence, m.ReplyFor())
		}
	}
}

func (self *multiChannelImpl) Tx(underlay Underlay, sendable Sendable, writeTimeout time.Duration) error {
	log := pfxlog.ContextLogger(self.Label())

	sendListener := sendable.SendListener()
	m := sendable.Msg()

	if err := sendable.Context().Err(); err != nil {
		sendListener.NotifyErr(TimeoutError{err})
		return nil
	}

	sendListener.NotifyBeforeWrite()

	if m == nil { // allow nil message in Sendable so we can send tracers to check time from send to write
		return nil
	}

	for _, transformHandler := range self.transformHandlers {
		transformHandler.Tx(m, self)
	}

	self.waiters.AddWaiter(sendable)

	var err error
	if writeTimeout > 0 {
		if err = underlay.SetWriteTimeout(writeTimeout); err != nil {
			log.WithError(err).Errorf("unable to set write timeout")
			sendListener.NotifyErr(err)
			return err
		}
	}

	err = underlay.Tx(m)

	if err != nil {
		log.WithError(err).Errorf("write error")
		sendListener.NotifyErr(err)

		for _, errorHandler := range self.errorHandlers {
			errorHandler.HandleError(err, self)
		}

		sendListener.NotifyAfterWrite()

		return err
	}

	for _, peekHandler := range self.peekHandlers {
		peekHandler.Tx(m, self)
	}

	sendListener.NotifyAfterWrite()

	return nil
}

func (self *multiChannelImpl) CloseUnderlay(underlay Underlay) {
	self.lock.Lock()
	if err := underlay.Close(); err != nil {
		pfxlog.Logger().WithField("context", self.Label()).WithError(err).Error("error closing underlay")
	}

	underlayRemoved := false
	self.underlays.DeleteIf(func(element Underlay) bool {
		if underlay == element {
			underlayRemoved = true
			return true
		}
		return false
	})
	self.lock.Unlock()

	if underlayRemoved {
		self.underlayHandler.HandleClose(self, underlay)
	}
}

func (self *multiChannelImpl) GetTimeSinceLastRead() time.Duration {
	return time.Duration(info.NowInMilliseconds()-atomic.LoadInt64(&self.lastRead)) * time.Millisecond
}

func (self *multiChannelImpl) Txer(underlay Underlay) {
	defer self.CloseUnderlay(underlay)

	var writeTimeout time.Duration
	if options := self.GetOptions(); options != nil {
		writeTimeout = options.WriteTimeout
	}

	messageSource := self.underlayHandler.GetMessageSource(underlay)

	for {
		sendable, err := messageSource(self.closeNotify)
		if err != nil {
			return
		}

		if err = self.Tx(underlay, sendable, writeTimeout); err != nil {
			self.underlayHandler.TxFailed(underlay, sendable)
			return
		}
	}
}

func (self *multiChannelImpl) Rxer(underlay Underlay) {
	defer self.CloseUnderlay(underlay)

	log := pfxlog.ContextLogger(self.Label())
	log.Debug("started")
	defer log.Debug("exited")

	defer func() {
		if r := recover(); r != nil {
			panic(r)
		}
		_ = self.Close()
	}()

	for {
		m, err := underlay.Rx()
		if err != nil {
			if err == io.EOF {
				log.WithError(err).Debug("EOF")
			} else if self.IsClosed() {
				log.WithError(err).Debug("rx error")
			} else {
				log.WithError(err).Error("rx error")
			}
			return
		}

		self.Rx(m)
	}
}

func (self *multiChannelImpl) DefaultDial(factory GroupedUnderlayFactory, underlayType string) (Underlay, error) {
	for {
		if self.IsClosed() {
			return nil, errors.New("unable to dial, channel is closed")
		}

		dialTimeout := self.GetOptions().ConnectTimeout
		if dialTimeout == 0 {
			dialTimeout = DefaultConnectTimeout
		}

		underlay, err := factory.Create(self.ConnectionId(), underlayType, dialTimeout)
		if err == nil {
			return underlay, nil
		}
	}
}

func GetUnderlayType(underlay Underlay) string {
	return string(underlay.Headers()[TypeHeader])
}
