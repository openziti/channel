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
	"sync"
	"sync/atomic"
	"time"

	"github.com/michaelquigley/pfxlog"
)

// Factory creates a new multi-underlay Channel from the first incoming underlay.
// The closeCallback should be called when the channel is closed to remove it from the listener.
//
// On success the returned Channel owns the underlay: the listener will not close it, so the
// Channel must close it when the Channel itself closes. On error the listener closes the
// underlay. A Factory that closes the underlay on its own error path is still safe, since
// Underlay.Close is idempotent.
type Factory func(underlay Underlay, closeCallback func()) (Channel, error)

// UngroupedChannelFallback handles incoming underlays that are not part of a grouped connection.
type UngroupedChannelFallback func(underlay Underlay) error

// registration is the listener's record of a channel registered under a connection id.
// Its pointer identity, rather than the Channel value, is what determines whether a close
// callback still refers to the currently registered channel. Using the pointer avoids
// comparing Channel interface values, which are not guaranteed to be comparable and would
// use value rather than instance equality even when they are.
type registration struct {
	ch     Channel
	closed atomic.Bool
}

// Admitter reports whether a new grouped channel may be created for an incoming underlay, returning an
// error to refuse it. It is the only point at which an application can decline a channel without
// consequence, because it runs before the hello is acknowledged: the dialer's create then fails and it
// starts over with a new group. Declining later, by failing the Factory, cannot be communicated to a
// dialer that the acknowledgement has already released.
//
// It is consulted only for an underlay that is about to create a channel. An underlay attaching to an
// established or in-flight group is never re-admitted, so a group cannot be refused partway through
// assembly, and a refusal never costs an established channel one of its underlays. Ungrouped underlays
// go to the UngroupedChannelFallback and are not admitted at all.
//
// It is consulted per group, not per peer: a channel that loses all its underlays reconnects as a new
// group with a new id, which is admitted again while the old channel may still be closing. An admitter
// enforcing a per-peer limit has to tolerate that overlap or it will refuse legitimate reconnects.
//
// The returned error's text is sent to the dialer as a negative hello result, along with its
// RejectClass if it is a RejectedError, so a refusal is distinguishable from a network failure. An
// application that needs to count or alert on refusals should do so in its Admitter, which knows the
// reason; nothing here aggregates them.
//
// It is called on the accept path with the group's id reserved, so it should be a cheap decision
// (admission or capacity), not work.
type Admitter func(underlay Underlay) error

// MultiListener routes incoming underlays to existing channels or creates new ones.
// Grouped underlays are matched by connection ID; ungrouped ones are passed to the fallback.
type MultiListener struct {
	channels                 map[string]*registration
	lock                     sync.Mutex
	multiChannelFactory      Factory
	ungroupedChannelFallback UngroupedChannelFallback
	createNotifiers          map[string]chan struct{}
	admitter                 Admitter
}

// MultiListenerConfig configures a MultiListener. Factory and UngroupedChannelFallback are required.
// Admitter is optional; with none set, every group is admitted.
type MultiListenerConfig struct {
	Factory                  Factory
	UngroupedChannelFallback UngroupedChannelFallback
	Admitter                 Admitter
}

// AcceptUnderlay routes an incoming underlay to an existing channel or creates a new one.
// It implements HelloAcceptor: for a grouped first connection it registers the group
// (reserving its id) before acknowledging the hello, so the ack - which releases the
// dialer to dial subsequent underlays - cannot precede the group being known. A
// subsequent underlay therefore finds either the channel or a create-in-progress
// notifier and attaches, rather than racing group creation and being rejected.
//
// That holds as long as the channel is created. If the Factory fails, the group is never
// registered and the reservation is released, while the dialer has already been acknowledged
// and will dial the group's remaining underlays; those find nothing and are closed. An
// application that may decline a channel should therefore do so from an Admitter, which is
// consulted before the ack.
func (self *MultiListener) AcceptUnderlay(underlay Underlay, ackHello func() error) {
	isGrouped, _ := Headers(underlay.Headers()).GetBoolHeader(IsGroupedHeader)

	log := pfxlog.Logger().
		WithField("underlayId", underlay.ConnectionId()).
		WithField("underlayType", GetUnderlayType(underlay)).
		WithField("isGrouped", isGrouped)

	if !isGrouped {
		if err := ackHello(); err != nil {
			log.WithError(err).Error("error acknowledging hello")
			_ = underlay.Close()
			return
		}
		if err := self.ungroupedChannelFallback(underlay); err != nil {
			log.WithError(err).Error("failed to create channel")
			if closeErr := underlay.Close(); closeErr != nil {
				log.WithError(closeErr).Error("error closing underlay")
			}
		}
		return
	}

	chId := underlay.ConnectionId()
	isFirst, _ := Headers(underlay.Headers()).GetBoolHeader(IsFirstGroupConnection)

	var ch Channel
	channelExists := false
	var createLockNotifier chan struct{}

	done := false
	for !done {
		var waitFor chan struct{}
		evictedStale := false

		// IsClosed is called under the listener lock, which Channel implementations must
		// support: the interface requires it to be a non-blocking read.
		self.lock.Lock()
		if reg, exists := self.channels[chId]; exists {
			if !reg.closed.Load() && !reg.ch.IsClosed() {
				ch = reg.ch
				channelExists = true
				done = true
				self.lock.Unlock()
				continue
			}

			// A stale closed channel is still registered for this id (it closed between
			// setting its closed flag and its close callback running). Evict it so this
			// underlay creates a fresh channel instead of being rejected indefinitely.
			delete(self.channels, chId)
			evictedStale = true
		}

		// The listener lock is held here and the channel id is now unregistered.
		var createLockExists bool
		waitFor, createLockExists = self.createNotifiers[chId]
		if !createLockExists {
			if !isFirst {
				// No channel and no create in progress for a non-first underlay: its group
				// is gone (or this is a stale/old-iteration underlay). Close without acking
				// so the dialer's create fails promptly rather than seeing a short-lived,
				// acked-then-closed underlay. This cannot happen for a live reconnect: the
				// group's first connection registers the notifier below before its own ack
				// releases the dialer to dial these subsequent underlays.
				self.lock.Unlock()
				if evictedStale {
					log.Info("evicted stale closed channel")
				}
				log.Info("no existing channel found for non-first underlay, closing connection")
				if err := underlay.Close(); err != nil {
					log.WithError(err).Error("error closing underlay")
				}
				return
			}
			createLockNotifier = make(chan struct{})
			self.createNotifiers[chId] = createLockNotifier
			done = true
		}
		self.lock.Unlock()
		if evictedStale {
			log.Info("evicted stale closed channel")
		}
		if waitFor != nil {
			select {
			case <-waitFor:
			case <-time.After(time.Second):
				// if we time out waiting for the channel to be created, there's something wrong,
				// close the underlay and hope it comes in with a new id
				log.Warn("timed out waiting for concurrent channel create on same id")
				if err := underlay.Close(); err != nil {
					log.WithError(err).Error("error closing underlay")
				}
				return
			}
		}
	}

	// Holding the reservation but not yet acknowledged is the last point at which a refusal is free:
	// releasing the reservation and closing the underlay leaves nothing behind, and the dialer's create
	// fails and starts over with a new group. A non-nil createLockNotifier means this underlay is the one
	// creating the channel, so an underlay attaching to an existing or in-flight group is never admitted.
	// Consulted outside the lock, since it calls out to the application; concurrent underlays for the same
	// group wait on the reservation meanwhile, as they do for the Factory.
	if createLockNotifier != nil && self.admitter != nil {
		if err := self.admitter(underlay); err != nil {
			log.WithError(err).Debug("channel not admitted, rejecting hello")
			self.releaseReservation(chId, createLockNotifier)
			if rejectErr := RejectHello(underlay, err); rejectErr != nil {
				log.WithError(rejectErr).Error("error rejecting underlay")
			}
			return
		}
	}

	// The group is now registered (an existing channel, or our create-in-progress
	// notifier). Acknowledge the hello: this releases the dialer to dial subsequent
	// underlays, which will now find the group rather than racing its creation.
	if err := ackHello(); err != nil {
		log.WithError(err).Error("error acknowledging hello")
		if createLockNotifier != nil {
			self.releaseReservation(chId, createLockNotifier)
		}
		_ = underlay.Close()
		return
	}

	if createLockNotifier != nil {
		defer self.releaseReservation(chId, createLockNotifier)
	}

	if channelExists {
		log.Info("found existing channel for underlay")
		if err := ch.AcceptUnderlay(underlay); err != nil {
			log.WithError(err).Error("error accepting underlay")
		}
	} else {
		log.Info("no existing channel found for underlay")
		var err error
		// newReg identifies this specific channel in the map. The close callback captures it
		// and evicts only this registration, so a stale callback can never remove a newer
		// channel that reconnected under the same id.
		newReg := &registration{}
		ch, err = self.multiChannelFactory(underlay, func() {
			self.closeRegistration(chId, newReg)
		})

		if ch == nil && err == nil {
			err = errors.New("multi-channel factory returned nil")
		}

		if err != nil {
			log.WithError(err).Error("failed to create multi-underlay channel")
			if closeErr := underlay.Close(); closeErr != nil {
				log.WithError(closeErr).Error("error closing underlay")
			}
		} else {
			// Populate the registration before publishing it, so a lookup can never observe
			// one with a nil channel.
			newReg.ch = ch

			self.lock.Lock()
			if newReg.closed.Load() || ch.IsClosed() {
				// The channel closed during creation (e.g. its only underlay dropped), and its
				// close callback already ran before we could register it. Registering it now
				// would leave a dead channel in the map that nothing removes, rejecting every
				// future reconnect for this id. Skip it; the dialer will redial and create a
				// fresh channel.
				self.lock.Unlock()
				log.Info("channel closed during creation, not registering")
			} else {
				self.channels[chId] = newReg
				self.lock.Unlock()
			}
		}
	}
}

// releaseReservation removes the group's create-in-progress reservation and wakes any underlays
// waiting on it, whether the create succeeded, failed, or was never attempted.
func (self *MultiListener) releaseReservation(chId string, notifier chan struct{}) {
	self.lock.Lock()
	delete(self.createNotifiers, chId)
	close(notifier)
	self.lock.Unlock()
}

// closeRegistration removes reg from the listener's map, but only if it is still the
// registration for chId. The identity check prevents a closing channel's callback from
// evicting a newer channel that has already reconnected under the same id.
func (self *MultiListener) closeRegistration(chId string, reg *registration) {
	// Mark the registration before taking the lock, so creation cannot publish it while this
	// callback waits for the lock and then find it already unregistered.
	reg.closed.Store(true)
	self.lock.Lock()
	if self.channels[chId] == reg {
		delete(self.channels, chId)
	}
	self.lock.Unlock()
}

// NewMultiListener creates a MultiListener with the given channel factory and ungrouped fallback.
// Use NewMultiListenerWithConfig to configure anything beyond those two.
func NewMultiListener(channelF Factory, fallback UngroupedChannelFallback) *MultiListener {
	return NewMultiListenerWithConfig(MultiListenerConfig{
		Factory:                  channelF,
		UngroupedChannelFallback: fallback,
	})
}

// NewMultiListenerWithConfig creates a MultiListener from a config, so that everything it depends on is
// fixed at construction and read-only thereafter, and so later options can be added without changing this
// signature. It panics if a required field is missing, since the alternative is a nil dereference in an
// accept goroutine the first time a matching underlay arrives, long after the wiring mistake was made.
func NewMultiListenerWithConfig(config MultiListenerConfig) *MultiListener {
	if config.Factory == nil {
		panic(errors.New("MultiListenerConfig.Factory is required"))
	}

	if config.UngroupedChannelFallback == nil {
		panic(errors.New("MultiListenerConfig.UngroupedChannelFallback is required"))
	}

	return &MultiListener{
		channels:                 make(map[string]*registration),
		multiChannelFactory:      config.Factory,
		ungroupedChannelFallback: config.UngroupedChannelFallback,
		createNotifiers:          make(map[string]chan struct{}),
		admitter:                 config.Admitter,
	}
}
