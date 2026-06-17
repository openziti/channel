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
	"testing"

	"github.com/stretchr/testify/require"
)

func newTypedTestUnderlay(connId, underlayType string) *testUnderlay {
	return &testUnderlay{
		headers:      map[int32][]byte{TypeHeader: []byte(underlayType)},
		connectionId: connId,
	}
}

// recordingHelloAcceptor records the underlays it accepts and counts successful acks.
type recordingHelloAcceptor struct {
	accepted []Underlay
	acked    int
}

func (self *recordingHelloAcceptor) AcceptUnderlay(underlay Underlay, ackHello func() error) {
	if err := ackHello(); err == nil {
		self.acked++
	}
	self.accepted = append(self.accepted, underlay)
}

func Test_TypeRoutingAcceptor_RoutesByType(t *testing.T) {
	a := &recordingHelloAcceptor{}
	b := &recordingHelloAcceptor{}
	def := &recordingHelloAcceptor{}

	router := &TypeRoutingAcceptor{
		Acceptors:       map[string]HelloAcceptor{"a": a, "b": b},
		DefaultAcceptor: def,
	}

	router.AcceptUnderlay(newTypedTestUnderlay("1", "a"), ackOK)
	router.AcceptUnderlay(newTypedTestUnderlay("2", "b"), ackOK)
	router.AcceptUnderlay(newTypedTestUnderlay("3", "unknown"), ackOK)

	require.Len(t, a.accepted, 1, "type 'a' should route to acceptor a")
	require.Len(t, b.accepted, 1, "type 'b' should route to acceptor b")
	require.Len(t, def.accepted, 1, "unknown type should route to the default acceptor")
}

func Test_TypeRoutingAcceptor_NoAcceptorClosesWithoutAck(t *testing.T) {
	router := &TypeRoutingAcceptor{Acceptors: map[string]HelloAcceptor{}}

	underlay := newTypedTestUnderlay("1", "unknown")
	acked := false
	router.AcceptUnderlay(underlay, func() error { acked = true; return nil })

	require.True(t, underlay.closed, "underlay should be closed when no acceptor applies")
	require.False(t, acked, "hello should not be acked when no acceptor applies")
}

// legacyUnderlayAcceptor implements the (post-ack) UnderlayAcceptor interface.
type legacyUnderlayAcceptor struct {
	accepted []Underlay
	err      error
}

func (self *legacyUnderlayAcceptor) AcceptUnderlay(underlay Underlay) error {
	self.accepted = append(self.accepted, underlay)
	return self.err
}

func Test_AsHelloAcceptor_AcksThenDelegates(t *testing.T) {
	legacy := &legacyUnderlayAcceptor{}
	acceptor := AsHelloAcceptor(legacy)

	underlay := newTypedTestUnderlay("1", "x")
	acked := false
	acceptor.AcceptUnderlay(underlay, func() error { acked = true; return nil })

	require.True(t, acked, "hello should be acked before delegating")
	require.Len(t, legacy.accepted, 1, "underlay should be delegated to the legacy acceptor")
	require.False(t, underlay.closed, "underlay should not be closed on success")
}

func Test_AsHelloAcceptor_ClosesOnLegacyError(t *testing.T) {
	legacy := &legacyUnderlayAcceptor{err: errors.New("boom")}
	acceptor := AsHelloAcceptor(legacy)

	underlay := newTypedTestUnderlay("1", "x")
	acceptor.AcceptUnderlay(underlay, ackOK)

	require.True(t, underlay.closed, "underlay should be closed when the legacy acceptor errors")
}
