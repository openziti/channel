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
	"time"
)

// An UnderlayAcceptor take an Underlay and generally turns it into a channel for a specific use.
// It can be used when handling multiple channel types on a single listener
type UnderlayAcceptor interface {
	// AcceptUnderlay takes ownership of u. Implementations must close u if they reject it,
	// since some callers hand it off and do not close it themselves. Callers that do close a
	// rejected underlay are relying on Underlay.Close being idempotent.
	AcceptUnderlay(u Underlay) error
}

// UnderlayDispatcherConfig holds configuration for an UnderlayDispatcher.
type UnderlayDispatcherConfig struct {
	Listener        UnderlayListener
	ConnectTimeout  time.Duration
	Acceptors       map[string]UnderlayAcceptor
	DefaultAcceptor UnderlayAcceptor
}

// An UnderlayDispatcher accept underlays from an underlay listener and hands them off to
// UnderlayAcceptor instances, based on the TypeHeader.
type UnderlayDispatcher struct {
	listener        UnderlayListener
	connectTimeout  time.Duration
	acceptors       map[string]UnderlayAcceptor
	defaultAcceptor UnderlayAcceptor
}

// NewUnderlayDispatcher creates a new UnderlayDispatcher from the given config.
func NewUnderlayDispatcher(config UnderlayDispatcherConfig) *UnderlayDispatcher {
	return &UnderlayDispatcher{
		listener:        config.Listener,
		connectTimeout:  config.ConnectTimeout,
		acceptors:       config.Acceptors,
		defaultAcceptor: config.DefaultAcceptor,
	}
}

// Run accepts underlays in a loop, dispatching each to the appropriate acceptor based on TypeHeader.
func (self *UnderlayDispatcher) Run() {
	log := For("channel.dispatcher")
	log.Info("started")
	defer log.Warn("exited")

	for {
		underlay, err := self.listener.Create(self.connectTimeout)
		if err != nil {
			log.Error("error accepting connection", "error", err)
			if err.Error() == "closed" {
				return
			}
			continue
		}
		chanType, found := underlay.Headers()[TypeHeader]
		var acceptor UnderlayAcceptor

		if !found {
			acceptor = self.defaultAcceptor
		} else {
			if acceptor, found = self.acceptors[string(chanType)]; !found {
				acceptor = self.defaultAcceptor
			}
		}

		closeUnderlay := false
		if acceptor == nil {
			log.Warn("incoming request didn't have a recognized type header, and no default acceptor defined. closing connection")
			closeUnderlay = true
		} else if err = acceptor.AcceptUnderlay(underlay); err != nil {
			log.Error("error handling incoming connection, closing connection", "error", err)
			closeUnderlay = true
		}

		if closeUnderlay {
			if err = underlay.Close(); err != nil {
				log.Info("error closing connection", "error", err)
			}
		}
	}
}
