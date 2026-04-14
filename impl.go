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
	"bytes"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/michaelquigley/pfxlog"
	"github.com/openziti/foundation/v2/concurrenz"
	"github.com/openziti/foundation/v2/info"
	"github.com/openziti/foundation/v2/sequence"
)

const (
	flagClosed             = 0
	flagRxStarted          = 1
	flagInjectUnderlayType = 2

	// DefaultUnderlayType is used when an underlay's type header is missing or not in the valid types list.
	DefaultUnderlayType = "default"
)

var connectionSeq = sequence.NewSequence()

// NextConnectionId returns a new unique connection identifier.
func NextConnectionId() (string, error) {
	return connectionSeq.NextHash()
}

// Config holds all the parameters needed to create a new Channel.
type Config struct {
	LogicalName string
	Options     *Options
	Binder      func(ch *channelImpl) error
	Underlay    Underlay

	InjectUnderlayTypeIntoMessages bool

	// ValidUnderlayTypes lists the recognized underlay type strings for this channel.
	// Incoming underlays with types not in this list are mapped to DefaultUnderlayType.
	// If nil, any type string is accepted as-is.
	ValidUnderlayTypes []string

	Senders               Senders
	MessageSourceProvider MessageSourceProvider
	DialPolicy            DialPolicy
	Constraints           map[string]UnderlayConstraint
	MinTotalUnderlays     int

	// ConstraintStartupDelay delays the first constraint check after channel creation.
	// Useful when the initial underlay needs time to stabilize before additional
	// underlays are dialed.
	ConstraintStartupDelay time.Duration

	// UnderlayEventListeners are notified when underlays are added or removed.
	// Use this to react to underlay changes (e.g., tracking connection counts,
	// updating "has dedicated underlay" flags, calling change callbacks).
	UnderlayEventListeners []UnderlayEventListener
}

type senderContextImpl struct {
	sequence    *sequence.Sequence
	closeNotify chan struct{}
}

func (self *senderContextImpl) NextSequence() int32 {
	return int32(self.sequence.Next())
}

func (self *senderContextImpl) GetCloseNotify() chan struct{} {
	return self.closeNotify
}

// NewSenderContext creates a new SenderContext with its own sequence counter and close channel.
func NewSenderContext() SenderContext {
	return &senderContextImpl{
		sequence:    sequence.NewSequence(),
		closeNotify: make(chan struct{}),
	}
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
			pfxlog.Logger().Debugf("removed waiter for %v. ttl: %v, now: %v", key, w.ttlMs, now)
		}
		return true
	})
	atomic.AddInt32(&self.size, -deleteCount)
}

func (self *waiterMap) clear() {
	atomic.StoreInt32(&self.size, 0)
	self.m.Clear()
}

type channelImpl struct {
	// Note: if altering this struct, be sure to account for 64 bit alignment on 32 bit arm arch
	// https://pkg.go.dev/sync/atomic#pkg-note-BUG
	// https://github.com/golang/go/issues/36606
	lastRead int64

	ownerId          string
	channelId        string
	logicalName      string
	fallbackUnderlay atomic.Pointer[Underlay]

	options           *Options
	waiters           waiterMap
	flags             concurrenz.AtomicBitSet
	closeNotify       chan struct{}
	peekHandlers      []PeekHandler
	transformHandlers []TransformHandler
	receiveHandlers   map[int32]ReceiveHandlerF
	errorHandlers     []ErrorHandler
	closeHandlers     []CloseHandler
	userData          interface{}
	replyCounter      uint32
	groupSecret       []byte

	senders               Senders
	messageSourceProvider MessageSourceProvider
	dialPolicy            DialPolicy
	constraints           map[string]UnderlayConstraint
	minTotalUnderlays     int
	validUnderlayTypes    []string
	applyInProgress       atomic.Bool

	lock      sync.Mutex
	underlays *Underlays
}

// NewChannel creates a multi-underlay channel from the given configuration. The config must
// include Senders, a MessageSourceProvider, and an initial Underlay. An optional Binder is
// called to register handlers before the first underlay starts processing.
func NewChannel(config *Config) (Channel, error) {
	if config.Senders == nil {
		return nil, fmt.Errorf("no senders configured for channel %s", config.LogicalName)
	}

	if config.MessageSourceProvider == nil {
		return nil, fmt.Errorf("no message source provider configured for channel %s", config.LogicalName)
	}

	if config.Underlay == nil {
		return nil, errors.New("unable to initialize channel (initialization produced zero underlays)")
	}

	impl := &channelImpl{
		channelId:             config.Underlay.ConnectionId(),
		logicalName:           config.LogicalName,
		options:               config.Options,
		receiveHandlers:       map[int32]ReceiveHandlerF{},
		closeNotify:           config.Senders.GetCloseNotify(),
		senders:               config.Senders,
		messageSourceProvider: config.MessageSourceProvider,
		dialPolicy:            config.DialPolicy,
		constraints:           config.Constraints,
		minTotalUnderlays:     config.MinTotalUnderlays,
		validUnderlayTypes:    config.ValidUnderlayTypes,
		underlays:             NewUnderlays(),
	}

	impl.flags.Set(flagInjectUnderlayType, config.InjectUnderlayTypeIntoMessages)

	impl.ownerId = config.Underlay.Id()
	impl.fallbackUnderlay.Store(&config.Underlay)

	groupSecret := config.Underlay.Headers()[GroupSecretHeader]
	if len(groupSecret) == 0 {
		return nil, errors.New("no group secret header found for channel")
	}
	impl.groupSecret = groupSecret

	// Register the channel as an underlay event listener for constraint enforcement
	impl.underlays.AddListener(impl)

	for _, l := range config.UnderlayEventListeners {
		impl.underlays.AddListener(l)
	}

	if config.Binder != nil {
		if err := config.Binder(impl); err != nil {
			if closeErr := impl.Close(); closeErr != nil {
				pfxlog.ContextLogger(impl.Label()).WithError(closeErr).Warn("error closing channel after bind failure")
			}
			if closeErr := config.Underlay.Close(); closeErr != nil {
				if !errors.Is(closeErr, net.ErrClosed) {
					pfxlog.ContextLogger(impl.Label()).WithError(err).Warn("error closing underlay")
				}
			}
			return nil, err
		}
	}

	// Add and start the first underlay (this triggers UnderlayAdded)
	impl.underlays.Add(impl, config.Underlay)
	impl.startMultiplex(config.Underlay)

	// Spawn constraint goroutine to dial additional underlays as needed
	if config.ConstraintStartupDelay > 0 {
		time.AfterFunc(config.ConstraintStartupDelay, func() {
			impl.applyConstraints()
		})
	} else {
		go impl.applyConstraints()
	}

	return impl, nil
}

// NewSingleChannel dials the factory and creates a simple channel with a single underlay,
// single sender, and the given bind handler.
func NewSingleChannel(logicalName string, underlayFactory UnderlayFactory, bindHandler BindHandler, options *Options) (Channel, error) {
	timeout := time.Duration(0)
	if options != nil {
		timeout = options.ConnectTimeout
	}

	underlay, err := underlayFactory.Create(timeout)
	if err != nil {
		return nil, err
	}

	return NewSingleChannelWithUnderlay(logicalName, underlay, bindHandler, options)
}

// NewSingleChannelWithUnderlay creates a simple channel from an existing underlay, with a single
// sender and bind handler. Use this when you already have a connected underlay (e.g. from a listener).
func NewSingleChannelWithUnderlay(logicalName string, underlay Underlay, bindHandler BindHandler, options *Options) (Channel, error) {
	headers := underlay.Headers()
	if len(headers[GroupSecretHeader]) == 0 {
		secret := uuid.New()
		headers[GroupSecretHeader] = secret[:]
	}

	outQueueSize := DefaultOutQueueSize
	if options != nil {
		outQueueSize = options.OutQueueSize
	}

	senderCtx := NewSenderContext()
	msgChan := make(chan Sendable, outQueueSize)
	sender := NewSingleChSender(senderCtx, msgChan)

	senders := &singleSenders{
		SenderContext: senderCtx,
		sender:        sender,
	}

	msgSource := func(notifier *CloseNotifier) (Sendable, error) {
		select {
		case msg := <-msgChan:
			return msg, nil
		case <-senderCtx.GetCloseNotify():
			return nil, io.EOF
		case <-notifier.GetCloseNotify():
			return nil, io.EOF
		}
	}

	config := &Config{
		LogicalName:           logicalName,
		Options:               options,
		Binder:                MakeBinder(bindHandler),
		Underlay:              underlay,
		Senders:               senders,
		MessageSourceProvider: NewSimpleMessageSourceProvider(msgSource),
	}

	return NewChannel(config)
}

// singleSenders is the Senders implementation for single-underlay channels.
type singleSenders struct {
	SenderContext
	sender Sender
}

func (self *singleSenders) GetDefaultSender() Sender             { return self.sender }
func (self *singleSenders) HandleTxFailed(string, Sendable) bool { return false }

func (self *channelImpl) AcceptUnderlay(underlay Underlay) error {
	self.lock.Lock()
	defer self.lock.Unlock()

	groupSecret := underlay.Headers()[GroupSecretHeader]
	if !bytes.Equal(groupSecret, self.groupSecret) {
		if err := underlay.Close(); err != nil {
			pfxlog.ContextLogger(self.Label()).WithError(err).Error("error closing underlay")
		}
		return fmt.Errorf("new underlay for '%s' not accepted: incorrect group secret", self.ConnectionId())
	}

	if self.IsClosed() {
		if err := underlay.Close(); err != nil {
			pfxlog.ContextLogger(self.Label()).WithError(err).Error("error closing underlay")
		}
		return fmt.Errorf("new underlay for '%s' not accepted: channel is closed", self.ConnectionId())
	}

	if underlay.Id() != self.ownerId {
		pfxlog.ContextLogger(self.Label()).
			Warnf("new underlay has different id [%s] than channel owner [%s]", underlay.Id(), self.ownerId)
	}

	self.fallbackUnderlay.Store(&underlay)
	self.underlays.Add(self, underlay)

	self.startMultiplex(underlay)

	return nil
}

func (self *channelImpl) startMultiplex(underlay Underlay) {
	notifier := NewCloseNotifier()
	go self.rxer(underlay, notifier)
	go self.txer(underlay, notifier)
}

func (self *channelImpl) GetUnderlayCountsByType() map[string]int {
	return self.underlays.CountsByType()
}

func (self *channelImpl) CloseNotify() <-chan struct{} {
	return self.closeNotify
}

func (self *channelImpl) GetSenders() Senders {
	return self.senders
}

func (self *channelImpl) GetUnderlays() []Underlay {
	return self.underlays.GetAll()
}

func (self *channelImpl) Send(s Sendable) error {
	return self.senders.GetDefaultSender().Send(s)
}

func (self *channelImpl) TrySend(s Sendable) (bool, error) {
	return self.senders.GetDefaultSender().TrySend(s)
}

func (self *channelImpl) Id() string {
	return self.ownerId
}

func (self *channelImpl) LogicalName() string {
	return self.logicalName
}

func (self *channelImpl) SetLogicalName(logicalName string) {
	self.logicalName = logicalName
}

func (self *channelImpl) ConnectionId() string {
	return self.channelId
}

func (self *channelImpl) Certificates() []*x509.Certificate {
	return self.Underlay().Certificates()
}

func (self *channelImpl) Headers() map[int32][]byte {
	return self.Underlay().Headers()
}

func (self *channelImpl) Label() string {
	return fmt.Sprintf("ch{%s}->%s", self.LogicalName(), self.Underlay().Label())
}

func (self *channelImpl) GetUserData() interface{} {
	return self.userData
}

func (self *channelImpl) Close() error {
	self.lock.Lock()
	defer self.lock.Unlock()

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

		self.waiters.clear()

		var errs []error
		for _, u := range self.underlays.GetAll() {
			if err := u.Close(); err != nil {
				errs = append(errs, err)
			}
		}

		return errors.Join(errs...)
	}

	return nil
}

func (self *channelImpl) IsClosed() bool {
	return self.flags.IsSet(flagClosed)
}

func (self *channelImpl) Underlay() Underlay {
	return *self.fallbackUnderlay.Load()
}

func (self *channelImpl) rx(m *Message) {
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
			receiveHandler(m, self)
		} else if anyHandler, found := self.receiveHandlers[AnyContentType]; found {
			anyHandler(m, self)
		} else {
			log.Warnf("dropped message. type [%d], sequence [%v], replyFor [%v]", m.ContentType, m.sequence, m.ReplyFor())
		}
	}
}

func (self *channelImpl) tx(underlay Underlay, underlayType string, sendable Sendable, writeTimeout time.Duration) error {
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
		self.waiters.RemoveWaiter(m.sequence)

		for _, errorHandler := range self.errorHandlers {
			errorHandler.HandleError(err, self)
		}

		// if we were able to requeue it, don't cancel sendable
		if !self.senders.HandleTxFailed(underlayType, sendable) {
			sendListener.NotifyErr(err)
			sendListener.NotifyAfterWrite()
		}

		return err
	}

	for _, peekHandler := range self.peekHandlers {
		peekHandler.Tx(m, self)
	}

	sendListener.NotifyAfterWrite()

	return nil
}

func (self *channelImpl) closeUnderlay(underlay Underlay, notifier *CloseNotifier) {
	if err := underlay.Close(); err != nil {
		pfxlog.Logger().WithField("context", self.Label()).WithError(err).Error("error closing underlay")
	}

	notifier.NotifyClosed()
	self.underlays.Remove(self, underlay)

	if *self.fallbackUnderlay.Load() == underlay {
		if underlays := self.underlays.GetAll(); len(underlays) > 0 {
			lastUnderlay := underlays[len(underlays)-1]
			self.fallbackUnderlay.Store(&lastUnderlay)
		}
	}
}

func (self *channelImpl) GetTimeSinceLastRead() time.Duration {
	return time.Duration(info.NowInMilliseconds()-atomic.LoadInt64(&self.lastRead)) * time.Millisecond
}

func (self *channelImpl) txer(underlay Underlay, notifier *CloseNotifier) {
	defer self.closeUnderlay(underlay, notifier)

	log := pfxlog.ContextLogger(self.Label())

	var writeTimeout time.Duration
	if options := self.options; options != nil {
		writeTimeout = options.WriteTimeout
	}

	underlayType := self.getValidatedUnderlayType(underlay)
	messageSource := self.messageSourceProvider.GetMessageSource(underlayType)

	for {
		sendable, err := messageSource(notifier)
		if err != nil {
			return
		}

		if err = self.tx(underlay, underlayType, sendable, writeTimeout); err != nil {
			if self.IsClosed() {
				log.WithError(err).Debug("tx error")
			} else {
				log.WithError(err).Error("tx error")
			}
			return
		}
	}
}

func (self *channelImpl) rxer(underlay Underlay, notifier *CloseNotifier) {
	defer self.closeUnderlay(underlay, notifier)

	log := pfxlog.ContextLogger(self.Label())
	log.Debug("started")
	defer log.Debug("exited")

	underlayType := self.getValidatedUnderlayType(underlay)
	injectType := self.flags.IsSet(flagInjectUnderlayType)

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

		if injectType {
			m.Headers.PutStringHeader(UnderlayTypeHeader, underlayType)
		}
		self.rx(m)
	}
}

// UnderlayAdded implements UnderlayEventListener. Logs the event.
func (self *channelImpl) UnderlayAdded(ch Channel, underlay Underlay) {
	pfxlog.Logger().
		WithField("id", ch.Label()).
		WithField("underlays", ch.GetUnderlayCountsByType()).
		WithField("underlayType", GetUnderlayType(underlay)).
		Info("underlay added")
}

// UnderlayRemoved implements UnderlayEventListener. Checks constraints and triggers re-dial if needed.
func (self *channelImpl) UnderlayRemoved(ch Channel, underlay Underlay) {
	pfxlog.Logger().
		WithField("id", ch.Label()).
		WithField("underlays", ch.GetUnderlayCountsByType()).
		WithField("underlayType", GetUnderlayType(underlay)).
		Info("underlay removed")

	go self.applyConstraints()
}

func (self *channelImpl) applyConstraints() {
	if len(self.constraints) == 0 {
		return
	}

	if !self.applyInProgress.CompareAndSwap(false, true) {
		return
	}

	log := pfxlog.Logger().WithField("conn", self.Label())
	log.Debug("starting constraint check")

	defer func() {
		self.applyInProgress.Store(false)

		// Re-check after releasing the flag. If a removal happened while we were
		// running, the goroutine it spawned would have seen applyInProgress=true
		// and exited. Do one final check so we don't miss it.
		if self.dialPolicy != nil && !self.IsClosed() && !self.areConstraintDesiresSatisfied() {
			go self.applyConstraints()
		}
	}()

	if self.IsClosed() {
		return
	}

	if !self.checkConstraintsValid(true) {
		return
	}

	if self.dialPolicy == nil {
		return
	}

	for !self.IsClosed() {
		counts := self.GetUnderlayCountsByType()

		if !self.countsShowValidState(counts, true) {
			return
		}

		allSatisfied := true
		for underlayType, constraint := range self.constraints {
			log.WithField("underlayType", underlayType).
				WithField("numDesired", constraint.Desired).
				WithField("current", counts[underlayType]).
				Debug("checking constraint")
			if constraint.Desired > counts[underlayType] {
				log.WithField("underlayType", underlayType).
					Info("additional connections desired, dialing...")

				allSatisfied = false
				self.dialUnderlay(underlayType)
			}
		}

		if allSatisfied {
			log.Debug("constraints satisfied")
			return
		}
	}
}

// areConstraintDesiresSatisfied returns true if all underlay types have at least
// their desired number of underlays.
func (self *channelImpl) areConstraintDesiresSatisfied() bool {
	counts := self.GetUnderlayCountsByType()
	for underlayType, constraint := range self.constraints {
		if constraint.Desired > counts[underlayType] {
			return false
		}
	}
	return true
}

func (self *channelImpl) checkConstraintsValid(closeIfInvalid bool) bool {
	counts := self.GetUnderlayCountsByType()
	return self.countsShowValidState(counts, closeIfInvalid)
}

func (self *channelImpl) countsShowValidState(counts map[string]int, closeIfInvalid bool) bool {
	for underlayType, constraint := range self.constraints {
		if constraint.Min > counts[underlayType] {
			if closeIfInvalid {
				pfxlog.Logger().WithField("conn", self.LogicalName()).
					WithField("channelId", self.ConnectionId()).
					WithField("label", self.Label()).
					WithField("underlays", counts).
					Infof("not enough open underlays of type '%s', closing channel", underlayType)
				if err := self.Close(); err != nil {
					pfxlog.Logger().WithError(err).Error("error closing underlay")
				}
			}
			return false
		}
	}

	totalCount := 0
	for _, count := range counts {
		totalCount += count
	}

	if totalCount < self.minTotalUnderlays {
		if closeIfInvalid {
			pfxlog.Logger().WithField("conn", self.LogicalName()).
				WithField("channelId", self.ConnectionId()).
				WithField("label", self.Label()).
				WithField("underlays", counts).
				Info("not enough total open underlays, closing channel")
			if err := self.Close(); err != nil {
				pfxlog.Logger().WithError(err).Error("error closing channel")
			}
		}
		return false
	}

	return true
}

func (self *channelImpl) dialUnderlay(underlayType string) {
	log := pfxlog.ContextLogger(self.Label()).WithField("underlayType", underlayType)

	connectTimeout := DefaultConnectTimeout
	if self.options != nil && self.options.ConnectTimeout > 0 {
		connectTimeout = self.options.ConnectTimeout
	}

	underlay, err := self.dialPolicy.Dial(underlayType, self.channelId, self.groupSecret, connectTimeout, self.closeNotify)
	if err != nil {
		if self.IsClosed() {
			log.Debug("dial cancelled, channel closed")
		} else {
			log.WithError(err).Error("dial of new underlay failed")
		}
		return
	}

	if err = self.AcceptUnderlay(underlay); err != nil {
		log.WithError(err).Error("accepting dialed underlay failed")
	}
}

// getValidatedUnderlayType returns the underlay type, validated against the channel's
// valid types list. Unknown types are mapped to DefaultUnderlayType.
func (self *channelImpl) getValidatedUnderlayType(underlay Underlay) string {
	t := GetUnderlayType(underlay)
	if len(self.validUnderlayTypes) == 0 {
		return t
	}
	if slices.Contains(self.validUnderlayTypes, t) {
		return t
	}
	return DefaultUnderlayType
}

// GetUnderlayType returns the underlay type from the headers.
// If no type header is present, returns DefaultUnderlayType.
func GetUnderlayType(underlay Underlay) string {
	if t := string(underlay.Headers()[TypeHeader]); t != "" {
		return t
	}
	return DefaultUnderlayType
}

// NewCloseNotifier creates a new CloseNotifier.
func NewCloseNotifier() *CloseNotifier {
	return &CloseNotifier{
		c: make(chan struct{}),
	}
}

// CloseNotifier provides a one-shot close signal. Calling NotifyClosed closes the
// internal channel, unblocking any goroutines waiting on GetCloseNotify.
type CloseNotifier struct {
	c        chan struct{}
	notified atomic.Bool
}

func (self *CloseNotifier) NotifyClosed() {
	if self.notified.CompareAndSwap(false, true) {
		close(self.c)
	}
}

func (self *CloseNotifier) GetCloseNotify() <-chan struct{} {
	return self.c
}
