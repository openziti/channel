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

package websockets

import (
	"bytes"
	"crypto/x509"
	"github.com/gorilla/websocket"
	"github.com/openziti/channel"
	"github.com/openziti/foundation/v2/concurrenz"
	"github.com/openziti/identity"
	"github.com/openziti/transport/v2"
	"github.com/pkg/errors"
	"net"
	"time"
)

type Underlay struct {
	id     *identity.TokenId
	peer   *websocket.Conn
	closed concurrenz.AtomicBoolean
	certs  []*x509.Certificate
}

func NewUnderlayFactory(id *identity.TokenId, peer *websocket.Conn, certs []*x509.Certificate) channel.UnderlayFactory {
	return &Underlay{
		id:    id,
		peer:  peer,
		certs: certs,
	}
}

func (impl *Underlay) GetLocalAddr() net.Addr {
	return impl.peer.LocalAddr()
}

func (impl *Underlay) GetRemoteAddr() net.Addr {
	return impl.peer.RemoteAddr()
}
func (self *Underlay) Create(time.Duration, transport.Configuration) (channel.Underlay, error) {
	return self, nil
}

func (self *Underlay) Rx() (*channel.Message, error) {
	t, data, err := self.peer.ReadMessage()
	if err != nil {
		return nil, err
	}

	if t != websocket.BinaryMessage {
		return nil, errors.Errorf("expected binary message type, got %v", t)
	}

	buf := bytes.NewBuffer(data)
	return channel.ReadV2(buf)
}

func (self *Underlay) Tx(m *channel.Message) error {
	data, err := channel.MarshalV2(m)
	if err != nil {
		return err
	}
	return self.peer.WriteMessage(websocket.BinaryMessage, data)
}

func (self *Underlay) Id() *identity.TokenId {
	return self.id
}

func (self *Underlay) LogicalName() string {
	return "ws"
}

func (self *Underlay) ConnectionId() string {
	return self.id.Token
}

func (self *Underlay) Certificates() []*x509.Certificate {
	return self.certs
}

func (self *Underlay) Label() string {
	return "ws"
}

func (self *Underlay) Close() error {
	if self.closed.CompareAndSwap(false, true) {
		return self.peer.Close()
	}
	return nil
}

func (self *Underlay) IsClosed() bool {
	return self.closed.Get()
}

func (self *Underlay) Headers() map[int32][]byte {
	return nil
}

func (self *Underlay) SetWriteTimeout(duration time.Duration) error {
	return self.peer.SetWriteDeadline(time.Now().Add(duration))
}
