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

import "github.com/michaelquigley/pfxlog"

// HelloAcceptor takes ownership of an underlay whose hello has been received but not
// yet acknowledged. Implementations must call ackHello exactly once before using the
// underlay for application traffic, and must close the underlay if they do not accept
// it (including when ackHello returns an error).
//
// Deferring the ack lets an acceptor record state keyed off the underlay before the
// peer is released by the acknowledgement. MultiListener uses this to register a group
// before acking, so a racing subsequent underlay for the same group cannot reach the
// listener before the group is known.
type HelloAcceptor interface {
	AcceptUnderlay(underlay Underlay, ackHello func() error)
}

// HelloAcceptorF adapts a plain accept function into a HelloAcceptor that acknowledges
// the hello and then hands off the underlay: the original single-phase behavior, with
// no reservation before the ack.
type HelloAcceptorF func(underlay Underlay)

// AcceptUnderlay acknowledges the hello and, on success, passes the underlay to the
// wrapped function. If the acknowledgement fails the underlay is closed.
func (f HelloAcceptorF) AcceptUnderlay(underlay Underlay, ackHello func() error) {
	if err := ackHello(); err != nil {
		pfxlog.Logger().WithError(err).Error("error acknowledging hello")
		_ = underlay.Close()
		return
	}
	f(underlay)
}

// AsHelloAcceptor adapts a legacy UnderlayAcceptor (which receives an already-acknowledged
// underlay) into a HelloAcceptor by acknowledging the hello before handing the underlay
// off. Use it for single-underlay acceptors, which don't need to reserve state before the
// ack; multi-underlay acceptors should implement HelloAcceptor directly.
func AsHelloAcceptor(acceptor UnderlayAcceptor) HelloAcceptor {
	return HelloAcceptorF(func(underlay Underlay) {
		if err := acceptor.AcceptUnderlay(underlay); err != nil {
			pfxlog.Logger().WithError(err).Error("error handling incoming connection, closing connection")
			if closeErr := underlay.Close(); closeErr != nil {
				pfxlog.Logger().WithError(closeErr).Info("error closing connection")
			}
		}
	})
}

// TypeRoutingAcceptor is a HelloAcceptor that routes each underlay to a per-type
// HelloAcceptor based on its TypeHeader, falling back to DefaultAcceptor when the type
// is absent or unrecognized. It is the ack-aware analog of UnderlayDispatcher for use
// with NewClassicListenerWithAcceptor, letting multi-underlay acceptors reserve state
// before the hello is acknowledged.
type TypeRoutingAcceptor struct {
	Acceptors       map[string]HelloAcceptor
	DefaultAcceptor HelloAcceptor
}

// AcceptUnderlay routes the underlay to the acceptor registered for its TypeHeader, or to
// the default acceptor. If no acceptor applies the underlay is closed without acking.
func (self *TypeRoutingAcceptor) AcceptUnderlay(underlay Underlay, ackHello func() error) {
	acceptor := self.DefaultAcceptor
	if chanType, found := underlay.Headers()[TypeHeader]; found {
		if typed, ok := self.Acceptors[string(chanType)]; ok {
			acceptor = typed
		}
	}

	if acceptor == nil {
		pfxlog.Logger().Warn("incoming request didn't have a recognized type header, and no default acceptor defined. closing connection")
		if err := underlay.Close(); err != nil {
			pfxlog.Logger().WithError(err).Info("error closing connection")
		}
		return
	}

	acceptor.AcceptUnderlay(underlay, ackHello)
}
