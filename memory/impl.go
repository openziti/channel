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

package memory

import (
	"crypto/x509"
	"errors"
	"fmt"
	"github.com/michaelquigley/pfxlog"
	"github.com/openziti/channel/v4"
	"github.com/openziti/identity"
	"io"
	"net"
	"sync"
	"time"
)

type addr string

func (a addr) Network() string {
	return "memory"
}

func (a addr) String() string {
	return string(a)
}

type memoryImpl struct {
	tx           chan *channel.Message
	rx           chan *channel.Message
	id           *identity.TokenId
	connectionId string
	headers      map[int32][]byte
	closeLock    sync.Mutex
	closed       bool
}

func (self *memoryImpl) GetLocalAddr() net.Addr {
	return addr("local:" + self.connectionId)
}

func (self *memoryImpl) GetRemoteAddr() net.Addr {
	return addr("remote:" + self.connectionId)
}

func (self *memoryImpl) SetWriteTimeout(time.Duration) error {
	panic("SetWriteTimeout not implemented")
}

func (self *memoryImpl) SetWriteDeadline(deadline time.Time) error {
	panic("SetWriteDeadline not implemented")
}

func (self *memoryImpl) Rx() (*channel.Message, error) {
	if self.closed {
		return nil, errors.New("underlay closed")
	}

	m := <-self.rx
	if m == nil {
		return nil, io.EOF
	}

	return m, nil
}

func (self *memoryImpl) Tx(m *channel.Message) error {
	if self.closed {
		return errors.New("underlay closed")
	}
	defer func() {
		if r := recover(); r != nil {
			pfxlog.Logger().Errorf("send err (%v)", r)
		}
	}()

	self.tx <- m

	return nil
}

func (self *memoryImpl) Id() string {
	return self.id.Token
}

func (self *memoryImpl) Headers() map[int32][]byte {
	return self.headers
}

func (self *memoryImpl) LogicalName() string {
	return "memory"
}

func (self *memoryImpl) ConnectionId() string {
	return self.connectionId
}

func (self *memoryImpl) Certificates() []*x509.Certificate {
	return nil
}

func (self *memoryImpl) Label() string {
	return fmt.Sprintf("u{%s}->i{%s}", self.LogicalName(), self.ConnectionId())
}

func (self *memoryImpl) Close() error {
	self.closeLock.Lock()
	defer self.closeLock.Unlock()

	if !self.closed {
		self.closed = true
		close(self.tx)
	}
	return nil
}

func (self *memoryImpl) IsClosed() bool {
	return self.closed
}

func newMemoryImpl(tx, rx chan *channel.Message) *memoryImpl {
	return &memoryImpl{
		tx: tx,
		rx: rx,
	}
}

type MemoryContext struct {
	request  chan *memoryRequest
	response chan *memoryImpl
}

func NewMemoryContext() *MemoryContext {
	return &MemoryContext{
		request:  make(chan *memoryRequest),
		response: make(chan *memoryImpl),
	}
}

type memoryRequest struct {
	dialer *memoryDialer
	hello  *channel.Hello
}
