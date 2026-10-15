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
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"time"

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
	Binder      Binder
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

	// MinTotalUnderlays is the minimum number of underlays (across all types) the channel
	// must keep open; it closes when the total drops below this. A positive value also makes
	// the channel multi-underlay-capable, so it can be used on its own (without per-type
	// constraints or a dial policy) for a channel that accepts additional underlays and
	// closes only when its last one is lost.
	MinTotalUnderlays int

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
	log := For("channel.impl")
	self.m.Range(func(key, value interface{}) bool {
		w, ok := value.(*waiter)
		if !ok {
			// Reaped without logging w: it is nil here, so reporting its ttl would panic.
			self.m.Delete(key)
			deleteCount++
			log.Debug("removed waiter of unexpected type", "key", key)
		} else if w.ttlMs < now {
			self.m.Delete(key)
			deleteCount++
			log.Debug("removed waiter", "key", key, "ttl", w.ttlMs, "now", now)
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
	replyCounter      atomic.Uint32
	groupSecret       []byte

	senders               Senders
	messageSourceProvider MessageSourceProvider
	dialPolicy            DialPolicy
	constraints           map[string]UnderlayConstraint
	minTotalUnderlays     int
	validUnderlayTypes    []string
	applyInProgress       atomic.Bool

	// log is this channel's logger, used both for its lifecycle events and for
	// its instance-scoped internal logging. It is resolved once at creation from
	// Options.Logger / the SetLoggerFor resolver, so an embedder's per-channel
	// logger governs all of this channel's logs (not just its events) and logging
	// needs no synchronization or package-global access.
	log *slog.Logger

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

	// Resolve this channel's logger before registering listeners or binding: a
	// bind handler can reach the channel via Binding.GetChannel() and call
	// AcceptUnderlay, which fires UnderlayAdded and reads impl.log. Caching it
	// here keeps logging free of locks and package-global access.
	impl.log = resolveEventLogger(impl.options, impl.logicalName, getLoggerFor())

	// The group secret matches reconnecting or additional underlays to this channel, so it is
	// only required for channels that can grow: those with a dial policy (which dials more
	// underlays), constraints (which desire more), or a minimum total underlay count (which
	// accepts more). This mirrors isMultiUnderlayCapable: a simple single-underlay channel never
	// dials or accepts additional underlays, so it needs no secret. Without this, a secretless
	// multi-underlay-capable channel would admit any secretless underlay, since bytes.Equal of two
	// empty secrets is true. Headers() may be nil, which indexes safely to a nil slice.
	impl.groupSecret = config.Underlay.Headers()[GroupSecretHeader]
	if len(impl.groupSecret) == 0 && (config.DialPolicy != nil || len(config.Constraints) > 0 || config.MinTotalUnderlays > 0) {
		return nil, errors.New("no group secret header found for multi-underlay channel")
	}

	// Register the channel as an underlay event listener for constraint enforcement
	impl.underlays.AddListener(impl)

	for _, l := range config.UnderlayEventListeners {
		impl.underlays.AddListener(l)
	}

	if err := config.Binder.bind(impl); err != nil {
		if closeErr := impl.Close(); closeErr != nil {
			impl.log.With("context", impl.Label()).Warn("error closing channel after bind failure", "error", closeErr)
		}
		if closeErr := config.Underlay.Close(); closeErr != nil {
			if !errors.Is(closeErr, net.ErrClosed) {
				impl.log.With("context", impl.Label()).Warn("error closing underlay", "error", closeErr)
			}
		}
		return nil, err
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

// isMultiUnderlayCapable reports whether this channel can grow beyond its
// initial underlay. Channels with a dial policy (which dials more underlays),
// constraints (which require or desire specific underlay types), or a minimum
// total underlay count accept or dial additional underlays; a simple channel
// with none of these never does.
func (self *channelImpl) isMultiUnderlayCapable() bool {
	return self.dialPolicy != nil || len(self.constraints) > 0 || self.minTotalUnderlays > 0
}

func (self *channelImpl) AcceptUnderlay(underlay Underlay) error {
	self.lock.Lock()
	defer self.lock.Unlock()

	// A simple channel never accepts additional underlays. Reject before the
	// secret check: a secretless simple channel has an empty groupSecret, and
	// bytes.Equal of two empty secrets would otherwise admit another secretless
	// underlay.
	if !self.isMultiUnderlayCapable() {
		if err := underlay.Close(); err != nil {
			self.log.With("context", self.Label()).Error("error closing underlay", "error", err)
		}
		return fmt.Errorf("new underlay for '%s' not accepted: channel does not accept additional underlays", self.ConnectionId())
	}

	groupSecret := underlay.Headers()[GroupSecretHeader]
	if !bytes.Equal(groupSecret, self.groupSecret) {
		if err := underlay.Close(); err != nil {
			self.log.With("context", self.Label()).Error("error closing underlay", "error", err)
		}
		return fmt.Errorf("new underlay for '%s' not accepted: incorrect group secret", self.ConnectionId())
	}

	if self.IsClosed() {
		if err := underlay.Close(); err != nil {
			self.log.With("context", self.Label()).Error("error closing underlay", "error", err)
		}
		return fmt.Errorf("new underlay for '%s' not accepted: channel is closed", self.ConnectionId())
	}

	if underlay.Id() != self.ownerId {
		self.log.With("context", self.Label()).
			Warn("new underlay has different id than channel owner", "id", underlay.Id(), "ownerId", self.ownerId)
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
		self.log.With("context", self.Label()).Debug("closing channel")

		close(self.closeNotify)

		for _, peekHandler := range self.peekHandlers {
			peekHandler.Close(self)
		}

		if len(self.closeHandlers) > 0 {
			for _, closeHandler := range self.closeHandlers {
				closeHandler.HandleClose(self)
			}
		} else {
			self.log.With("context", self.Label()).Debug("no close handlers")
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
		if self.replyCounter.Add(1)%100 == 0 && self.waiters.Size() > 1000 {
			self.waiters.reapExpired(now)
		}
		replyFor := m.ReplyFor()
		if replyReceiver := self.waiters.RemoveWaiter(replyFor); replyReceiver != nil {
			// Guarded rather than logged unconditionally: this is once per reply
			// message, and Label() is two Sprintfs before the level is consulted.
			if log := self.log; log.Enabled(context.Background(), LevelTrace) {
				log.With("context", self.Label()).Log(context.Background(), LevelTrace, "waiter found for message", "type", m.ContentType, "sequence", m.sequence, "replyFor", replyFor)
			}
			replyReceiver.AcceptReply(m)
			handled = true
		} else {
			self.log.With("context", self.Label()).Debug("no waiter for message", "type", m.ContentType, "sequence", m.sequence, "replyFor", replyFor)
		}
	}

	if !handled {
		if receiveHandler, found := self.receiveHandlers[m.ContentType]; found {
			receiveHandler(m, self)
		} else if anyHandler, found := self.receiveHandlers[AnyContentType]; found {
			anyHandler(m, self)
		} else {
			self.log.With("context", self.Label()).Warn("dropped message", "type", m.ContentType, "sequence", m.sequence, "replyFor", m.ReplyFor())
		}
	}
}

func (self *channelImpl) tx(underlay Underlay, underlayType string, sendable Sendable, writeTimeout time.Duration) error {
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
			self.log.With("context", self.Label()).Error("unable to set write timeout", "error", err)
			sendListener.NotifyErr(err)
			return err
		}
	}

	err = underlay.Tx(m)

	if err != nil {
		self.log.With("context", self.Label()).Error("write error", "error", err)
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
		self.log.With("context", self.Label()).Error("error closing underlay", "error", err)
	}

	notifier.NotifyClosed()
	self.underlays.Remove(self, underlay)

	self.lock.Lock()
	if *self.fallbackUnderlay.Load() == underlay {
		if underlays := self.underlays.GetAll(); len(underlays) > 0 {
			lastUnderlay := underlays[len(underlays)-1]
			self.fallbackUnderlay.Store(&lastUnderlay)
		}
	}
	self.lock.Unlock()
}

func (self *channelImpl) GetTimeSinceLastRead() time.Duration {
	return time.Duration(info.NowInMilliseconds()-atomic.LoadInt64(&self.lastRead)) * time.Millisecond
}

func (self *channelImpl) txer(underlay Underlay, notifier *CloseNotifier) {
	defer self.closeUnderlay(underlay, notifier)

	log := self.log.With("context", self.Label())

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
				log.Debug("tx error", "error", err)
			} else {
				log.Error("tx error", "error", err)
			}
			return
		}
	}
}

func (self *channelImpl) rxer(underlay Underlay, notifier *CloseNotifier) {
	defer self.closeUnderlay(underlay, notifier)

	log := self.log.With("context", self.Label())
	log.Debug("started")
	defer log.Debug("exited")

	underlayType := self.getValidatedUnderlayType(underlay)
	injectType := self.flags.IsSet(flagInjectUnderlayType)

	for {
		m, err := underlay.Rx()
		if err != nil {
			if err == io.EOF {
				log.Debug("EOF", "error", err)
			} else if self.IsClosed() {
				log.Debug("rx error", "error", err)
			} else {
				log.Error("rx error", "error", err)
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
func (self *channelImpl) UnderlayAdded(ch Channel, underlay Underlay, event UnderlayEvent) {
	self.log.Info("underlay added",
		"id", ch.Label(),
		"underlays", event.Count,
		"underlayType", GetUnderlayType(underlay),
	)
}

// UnderlayRemoved implements UnderlayEventListener. Reports the underlay's lifetime to the
// dial policy for stability accounting, then checks constraints and triggers re-dial if needed.
func (self *channelImpl) UnderlayRemoved(ch Channel, underlay Underlay, event UnderlayEvent) {
	self.log.Info("underlay removed",
		"id", ch.Label(),
		"underlays", event.Count,
		"underlayType", GetUnderlayType(underlay),
		"lifetime", event.Lifetime,
	)

	if self.dialPolicy != nil {
		// event.Lifetime is measured when the underlay left the set. Sampling the clock here would
		// add however long this notification waited its turn to every reading the policy sees.
		self.dialPolicy.UnderlayClosed(self.getValidatedUnderlayType(underlay), event.Lifetime)
	}

	if !self.isMultiUnderlayCapable() {
		// A simple channel (no constraints, no dial policy) cannot recover from
		// underlay loss. If no underlays remain, close the channel.
		if event.Count == 0 {
			if err := self.Close(); err != nil {
				self.log.Error("error closing channel after last underlay removed", "error", err)
			}
		}
		return
	}

	go self.applyConstraints()
}

func (self *channelImpl) applyConstraints() {
	// Nothing to enforce without per-type constraints or a minimum total. A channel
	// with only minTotalUnderlays still needs this: countsShowValidState closes it
	// when the total drops below the minimum.
	if len(self.constraints) == 0 && self.minTotalUnderlays == 0 {
		return
	}

	if self.IsClosed() {
		return
	}

	// Check min-validity (and close if below minimum) BEFORE taking the in-progress
	// guard. A required-underlay loss must close the channel promptly even when another
	// constraint fill or dial backoff is already running; otherwise the close would be
	// deferred until that in-progress dial returns (up to the full backoff delay).
	// Closing here also cancels any in-flight dial, since it closes closeNotify.
	if !self.checkConstraintsValid(true) {
		return
	}

	if !self.applyInProgress.CompareAndSwap(false, true) {
		return
	}

	log := self.log.With("conn", self.Label())
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
			log.With("underlayType", underlayType,
				"numDesired", constraint.Desired,
				"current", counts[underlayType]).
				Debug("checking constraint")
			if constraint.Desired > counts[underlayType] {
				log.With("underlayType", underlayType).
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
				self.log.
					With("conn", self.LogicalName(),
						"channelId", self.ConnectionId(),
						"label", self.Label(),
						"underlays", counts,
						"underlayType", underlayType).
					Info("not enough open underlays of type, closing channel")
				if err := self.Close(); err != nil {
					self.log.Error("error closing underlay", "error", err)
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
			self.log.
				With("conn", self.LogicalName(),
					"channelId", self.ConnectionId(),
					"label", self.Label(),
					"underlays", counts).
				Info("not enough total open underlays, closing channel")
			if err := self.Close(); err != nil {
				self.log.Error("error closing channel", "error", err)
			}
		}
		return false
	}

	return true
}

func (self *channelImpl) dialUnderlay(underlayType string) {
	log := self.log.With("context", self.Label()).With("underlayType", underlayType)

	connectTimeout := DefaultConnectTimeout
	if self.options != nil && self.options.ConnectTimeout > 0 {
		connectTimeout = self.options.ConnectTimeout
	}

	// isFirst is true when no underlays remain, i.e. this dial re-establishes the group after
	// full loss. The initial underlay is supplied to NewChannel rather than dialed here, so a
	// first-connection dial only occurs on reconnect.
	isFirst := len(self.underlays.GetAll()) == 0

	underlay, err := self.dialPolicy.Dial(underlayType, self.channelId, self.groupSecret, isFirst, connectTimeout, self.closeNotify)
	if err != nil {
		if self.IsClosed() {
			log.Debug("dial cancelled, channel closed")
		} else {
			log.Error("dial of new underlay failed", "error", err)
		}
		return
	}

	if err = self.AcceptUnderlay(underlay); err != nil {
		log.Error("accepting dialed underlay failed", "error", err)
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
