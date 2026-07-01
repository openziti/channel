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

// MultiListener routes incoming underlays to existing channels or creates new ones.
// Grouped underlays are matched by connection ID; ungrouped ones are passed to the fallback.
type MultiListener struct {
	channels                 map[string]*registration
	lock                     sync.Mutex
	multiChannelFactory      Factory
	ungroupedChannelFallback UngroupedChannelFallback
	createNotifiers          map[string]chan struct{}
}

// AcceptUnderlay routes an incoming underlay to an existing channel or creates a new one.
// It implements HelloAcceptor: for a grouped first connection it registers the group
// (reserving its id) before acknowledging the hello, so the ack - which releases the
// dialer to dial subsequent underlays - cannot precede the group being known. A
// subsequent underlay therefore always finds either the channel or a create-in-progress
// notifier and attaches, rather than racing group creation and being rejected.
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

		// Channel methods must not be called while holding the listener lock. A channel may
		// invoke its close callback while holding the same lock used by IsClosed, and that
		// callback needs the listener lock to unregister the channel.
		self.lock.Lock()
		candidate, candidateExists := self.channels[chId]
		self.lock.Unlock()

		if candidateExists {
			candidateClosed := candidate.closed.Load()
			if !candidateClosed {
				candidateClosed = candidate.ch.IsClosed()
			}

			self.lock.Lock()
			current, stillExists := self.channels[chId]
			if !stillExists || current != candidate {
				// The registration changed while IsClosed ran. Retry rather than applying
				// the result to a newer channel registered under the same id.
				self.lock.Unlock()
				continue
			}
			if !candidateClosed && !candidate.closed.Load() {
				ch = candidate.ch
				channelExists = true
				done = true
				self.lock.Unlock()
				continue
			}

			// A stale closed channel is still registered for this id (it closed between
			// setting its closed flag and its close callback running). Evict it so this
			// underlay creates a fresh channel instead of being rejected indefinitely.
			delete(self.channels, chId)
		} else {
			self.lock.Lock()
			if _, nowExists := self.channels[chId]; nowExists {
				// A registration appeared after the unlocked lookup. Retry and validate it.
				self.lock.Unlock()
				continue
			}
		}

		// The listener lock is held here and the channel id is still unregistered.
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

	// The group is now registered (an existing channel, or our create-in-progress
	// notifier). Acknowledge the hello: this releases the dialer to dial subsequent
	// underlays, which will now find the group rather than racing its creation.
	if err := ackHello(); err != nil {
		log.WithError(err).Error("error acknowledging hello")
		if createLockNotifier != nil {
			self.lock.Lock()
			delete(self.createNotifiers, chId)
			close(createLockNotifier)
			self.lock.Unlock()
		}
		_ = underlay.Close()
		return
	}

	if createLockNotifier != nil {
		defer func() {
			self.lock.Lock()
			delete(self.createNotifiers, chId)
			close(createLockNotifier)
			self.lock.Unlock()
		}()
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
			// Set and inspect the channel before publishing the registration. The callback's
			// closed flag covers a close that races the IsClosed snapshot.
			newReg.ch = ch
			channelClosed := newReg.closed.Load()
			if !channelClosed {
				channelClosed = ch.IsClosed()
			}

			self.lock.Lock()
			if channelClosed || newReg.closed.Load() {
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

// CloseChannel removes the channel with the given ID from the listener's map.
func (self *MultiListener) CloseChannel(chId string) {
	self.lock.Lock()
	delete(self.channels, chId)
	self.lock.Unlock()
}

// closeRegistration removes reg from the listener's map, but only if it is still the
// registration for chId. The identity check prevents a closing channel's callback from
// evicting a newer channel that has already reconnected under the same id.
func (self *MultiListener) closeRegistration(chId string, reg *registration) {
	// Mark the token first so creation cannot publish it while this callback waits for the
	// listener lock. This state is also safe to inspect without calling back into Channel.
	reg.closed.Store(true)
	self.lock.Lock()
	if self.channels[chId] == reg {
		delete(self.channels, chId)
	}
	self.lock.Unlock()
}

// NewMultiListener creates a MultiListener with the given channel factory and ungrouped fallback.
func NewMultiListener(channelF Factory, fallback UngroupedChannelFallback) *MultiListener {
	result := &MultiListener{
		channels:                 make(map[string]*registration),
		multiChannelFactory:      channelF,
		ungroupedChannelFallback: fallback,
		createNotifiers:          make(map[string]chan struct{}),
	}
	return result
}
