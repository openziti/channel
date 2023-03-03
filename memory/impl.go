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
	"github.com/openziti/channel/v2"
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

func (impl *memoryImpl) GetLocalAddr() net.Addr {
	return addr("local:" + impl.connectionId)
}

func (impl *memoryImpl) GetRemoteAddr() net.Addr {
	return addr("remote:" + impl.connectionId)
}

func (impl *memoryImpl) SetWriteTimeout(time.Duration) error {
	panic("SetWriteTimeout not implemented")
}

func (self *memoryImpl) SetWriteDeadline(deadline time.Time) error {
	panic("SetWriteDeadline not implemented")
}

func (impl *memoryImpl) Rx() (*channel.Message, error) {
	if impl.closed {
		return nil, errors.New("underlay closed")
	}

	m := <-impl.rx
	if m == nil {
		return nil, io.EOF
	}

	return m, nil
}

func (impl *memoryImpl) Tx(m *channel.Message) error {
	if impl.closed {
		return errors.New("underlay closed")
	}
	defer func() {
		if r := recover(); r != nil {
			pfxlog.Logger().Errorf("send err (%v)", r)
		}
	}()

	impl.tx <- m

	return nil
}

func (impl *memoryImpl) Id() string {
	return impl.id.Token
}

func (impl *memoryImpl) Headers() map[int32][]byte {
	return impl.headers
}

func (impl *memoryImpl) LogicalName() string {
	return "memory"
}

func (impl *memoryImpl) ConnectionId() string {
	return impl.connectionId
}

func (impl *memoryImpl) Certificates() []*x509.Certificate {
	return nil
}

func (impl *memoryImpl) Label() string {
	return fmt.Sprintf("u{%s}->i{%s}", impl.LogicalName(), impl.ConnectionId())
}

func (impl *memoryImpl) Close() error {
	impl.closeLock.Lock()
	defer impl.closeLock.Unlock()

	if !impl.closed {
		impl.closed = true
		close(impl.tx)
	}
	return nil
}

func (impl *memoryImpl) IsClosed() bool {
	return impl.closed
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
