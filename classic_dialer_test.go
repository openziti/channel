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
	"net"
	"testing"
	"time"

	"github.com/openziti/identity"
	"github.com/openziti/transport/v2"
	"github.com/openziti/transport/v2/tcp"
	"github.com/stretchr/testify/require"
)

// Test_ClassicListener_AcceptsOtherValueUnderReplyForKeyInHello dials a classic listener with a hello
// carrying a one-byte value under the ReplyFor header key, as protocols that use that key for their
// own hello headers send. The listener must accept the hello and pass the header through unchanged.
func Test_ClassicListener_AcceptsOtherValueUnderReplyForKeyInHello(t *testing.T) {
	transport.AddAddressParser(tcp.AddressParser{})
	req := require.New(t)

	listenAddr, err := transport.ParseAddress("tcp:127.0.0.1:0")
	req.NoError(err)

	accepted := make(chan Underlay, 1)
	listener, err := NewClassicListenerF(&identity.TokenId{Token: "test-server"}, listenAddr,
		ListenerConfig{ConnectOptions: DefaultConnectOptions()}, func(underlay Underlay) { accepted <- underlay })
	req.NoError(err)
	defer func() { _ = listener.Close() }()

	socket, ok := listener.(*classicListener).socket.(net.Listener)
	req.True(ok, "the tcp transport should listen on a net.Listener")
	dialAddr, err := transport.ParseAddress("tcp:" + socket.Addr().String())
	req.NoError(err)

	dialer := NewClassicDialer(DialerConfig{
		Identity: &identity.TokenId{Token: "test-client"},
		Endpoint: dialAddr,
	})
	underlay, err := dialer.CreateWithHeaders(2*time.Second, map[int32][]byte{ReplyForHeader: {1}})
	req.NoError(err)
	defer func() { _ = underlay.Close() }()

	select {
	case listenerUnderlay := <-accepted:
		defer func() { _ = listenerUnderlay.Close() }()
		req.Equal([]byte{1}, listenerUnderlay.Headers()[ReplyForHeader])
	case <-time.After(2 * time.Second):
		req.Fail("listener did not accept the underlay")
	}
}
