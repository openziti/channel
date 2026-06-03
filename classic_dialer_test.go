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
	"net"
	"testing"
	"time"

	"github.com/openziti/identity"
	"github.com/openziti/transport/v2"
	"github.com/stretchr/testify/require"
)

// fakeHelloPeer is a minimal transport.Conn for sendHello: it only needs the deadline
// and remote-address calls the handshake makes.
type fakeHelloPeer struct {
	transport.Conn
}

func (fakeHelloPeer) SetDeadline(time.Time) error { return nil }
func (fakeHelloPeer) RemoteAddr() net.Addr        { return &net.TCPAddr{} }

// fakeHelloUnderlay is a minimal classicUnderlay for sendHello. It embeds Underlay so the
// type is satisfied; only the methods the handshake touches are implemented. Tx/Rx outcomes
// are configurable so each hello-failure path can be exercised.
type fakeHelloUnderlay struct {
	Underlay
	txErr  error
	rxErr  error
	closed bool
}

func (f *fakeHelloUnderlay) Label() string                { return "fake" }
func (f *fakeHelloUnderlay) getPeer() transport.Conn      { return fakeHelloPeer{} }
func (f *fakeHelloUnderlay) init(string, string, Headers) {}
func (f *fakeHelloUnderlay) rxHello() (*Message, error)   { return nil, nil }
func (f *fakeHelloUnderlay) Tx(*Message) error            { return f.txErr }
func (f *fakeHelloUnderlay) Rx() (*Message, error)        { return nil, f.rxErr }
func (f *fakeHelloUnderlay) Close() error                 { f.closed = true; return nil }

// Test_sendHello_ClosesUnderlayOnFailure is the regression test for the FD leak in #246:
// every hello-handshake failure must close the underlay (and its transport connection),
// not just the Tx-error path.
func Test_sendHello_ClosesUnderlayOnFailure(t *testing.T) {
	dialer := &classicDialer{
		identity: &identity.TokenId{Token: "test"},
		headers:  map[int32][]byte{},
	}

	t.Run("Tx failure closes underlay", func(t *testing.T) {
		u := &fakeHelloUnderlay{txErr: errors.New("tx boom")}
		err := dialer.sendHello(u, time.Now().Add(time.Second), nil)
		require.Error(t, err)
		require.True(t, u.closed, "underlay must be closed when Tx fails")
	})

	t.Run("Rx failure closes underlay", func(t *testing.T) {
		u := &fakeHelloUnderlay{rxErr: errors.New("rx boom")}
		err := dialer.sendHello(u, time.Now().Add(time.Second), nil)
		require.Error(t, err)
		require.True(t, u.closed, "underlay must be closed when Rx fails (was the leaking path)")
	})

	t.Run("BadMagicNumber failure closes underlay", func(t *testing.T) {
		u := &fakeHelloUnderlay{rxErr: BadMagicNumberError}
		err := dialer.sendHello(u, time.Now().Add(time.Second), nil)
		require.Error(t, err)
		require.True(t, u.closed, "underlay must be closed on invalid-header response")
	})
}
