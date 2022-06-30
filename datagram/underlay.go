//go:build prototype

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

package datagram

import (
	"bytes"
	"crypto/x509"
	"fmt"
	"github.com/openziti/channel"
	"github.com/openziti/identity"
	"github.com/openziti/foundation/v2/concurrenz"
	"github.com/openziti/transport/v2"
	"time"
)

type Underlay struct {
	id           *identity.TokenId
	connectionId string
	headers      map[int32][]byte
	peer         transport.Conn
	closed       concurrenz.AtomicBoolean
}

func NewUnderlay(id *identity.TokenId, peer transport.Conn) channel.Underlay {
	return &Underlay{
		id:   id,
		peer: peer,
	}
}

func (self *Underlay) Rx() (*channel.Message, error) {
	buf := make([]byte, 65000)
	n, err := self.peer.Read(buf)
	if err != nil {
		return nil, err
	}

	buf = buf[:n]

	reader := bytes.NewBuffer(buf)
	return channel.ReadV2(reader)
}

func (self *Underlay) Tx(m *channel.Message) error {
	data, err := channel.MarshalV2(m)
	if err != nil {
		return err
	}
	_, err = self.peer.Write(data)
	return err
}

func (self *Underlay) Id() *identity.TokenId {
	return self.id
}

func (self *Underlay) LogicalName() string {
	return "datagram"
}

func (self *Underlay) ConnectionId() string {
	return self.connectionId
}

func (self *Underlay) Certificates() []*x509.Certificate {
	return self.peer.PeerCertificates()
}

func (self *Underlay) Label() string {
	return fmt.Sprintf("u{%s}->i{%s}", self.LogicalName(), self.ConnectionId())
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
	return self.headers
}

func (self *Underlay) SetWriteTimeout(duration time.Duration) error {
	return self.peer.SetWriteDeadline(time.Now().Add(duration))
}
