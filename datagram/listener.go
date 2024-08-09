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
	"fmt"
	"github.com/michaelquigley/pfxlog"
	"github.com/openziti/channel/v2"
	"github.com/openziti/foundation/v2/goroutines"
	"github.com/openziti/identity"
	"github.com/openziti/transport/v2"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"io"
	"sync/atomic"
	"time"
)

type listener struct {
	identity       *identity.TokenId
	endpoint       transport.Address
	socket         io.Closer
	close          chan struct{}
	handlers       []channel.ConnectionHandler
	acceptF        func(underlay channel.Underlay)
	created        chan channel.Underlay
	connectOptions channel.ConnectOptions
	tcfg           transport.Configuration
	headers        map[int32][]byte
	closed         atomic.Bool
	listenerPool   goroutines.Pool
}

func newListener(identity *identity.TokenId, endpoint transport.Address, config channel.ListenerConfig) *listener {
	closeNotify := make(chan struct{})

	poolConfig := goroutines.PoolConfig{
		QueueSize:  uint32(config.ConnectOptions.MaxQueuedConnects),
		MinWorkers: 1,
		MaxWorkers: uint32(config.ConnectOptions.MaxOutstandingConnects),
		IdleTime:   10 * time.Second,
		PanicHandler: func(err interface{}) {
			pfxlog.Logger().
				WithField("id", identity.Token).
				WithField("endpoint", endpoint.String()).
				WithField(logrus.ErrorKey, err).Error("panic during channel accept")
		},
	}

	if config.PoolConfigurator != nil {
		config.PoolConfigurator(&poolConfig)
	}

	poolConfig.CloseNotify = closeNotify

	pool, err := goroutines.NewPool(poolConfig)
	if err != nil {
		logrus.WithError(err).Error("failed to initial channel listener pool")
		panic(err)
	}

	return &listener{
		identity:       identity,
		endpoint:       endpoint,
		close:          closeNotify,
		connectOptions: config.ConnectOptions,
		tcfg:           config.TransportConfig,
		headers:        config.Headers,
		listenerPool:   pool,
		handlers:       config.ConnectionHandlers,
	}
}

func NewListenerF(identity *identity.TokenId, endpoint transport.Address, config channel.ListenerConfig, f func(underlay channel.Underlay)) (io.Closer, error) {
	l := newListener(identity, endpoint, config)
	l.acceptF = f
	if err := l.Listen(); err != nil {
		return nil, err
	}
	return l, nil
}

func NewListener(identity *identity.TokenId, endpoint transport.Address, config channel.ListenerConfig) channel.UnderlayListener {
	l := newListener(identity, endpoint, config)
	l.created = make(chan channel.Underlay)
	l.acceptF = func(underlay channel.Underlay) {
		select {
		case l.created <- underlay:
		case <-l.close:
			pfxlog.Logger().WithField("underlay", underlay.Label()).Info("channel closed, can't notify of new connection")
			return
		}
	}
	return l
}

func (self *listener) Listen(handlers ...channel.ConnectionHandler) error {
	self.handlers = append(self.handlers, handlers...)
	socket, err := self.endpoint.Listen("datagram", self.identity, self.acceptConnection, self.tcfg)
	if err != nil {
		return err
	}
	self.socket = socket
	return nil
}

func (self *listener) Close() error {
	if self.closed.CompareAndSwap(false, true) {
		close(self.close)
		if socket := self.socket; socket != nil {
			if err := socket.Close(); err != nil {
				return err
			}
		}
		self.socket = nil
	}
	return nil
}

func (self *listener) Create(_ time.Duration, _ transport.Configuration) (channel.Underlay, error) {
	if self.created == nil {
		return nil, errors.New("this listener was not set up for Create to be called, programming error")
	}

	select {
	case impl := <-self.created:
		if impl != nil {
			return impl, nil
		}
	case <-self.close:
	}
	return nil, channel.ListenerClosedError
}

func (self *listener) acceptConnection(peer transport.Conn) {
	log := pfxlog.ContextLogger(self.endpoint.String())
	err := self.listenerPool.Queue(func() {
		impl := &channel.DatagramUnderlay{
			id:   self.identity.Token,
			peer: peer,
		}

		var err error
		impl.connectionId, err = channel.NextConnectionId()
		if err != nil {
			_ = peer.Close()
			log.Errorf("error getting connection id for [%s] (%v)", peer.Detail().Address, err)
			return
		}

		if err = peer.SetReadDeadline(time.Now().Add(self.connectOptions.ConnectTimeout)); err != nil {
			log.Errorf("could not set read timeout for [%s] (%v)", peer.Detail().Address, err)
			_ = peer.Close()
			return
		}

		request, hello, err := self.receiveHello(impl)
		if err != nil {
			_ = peer.Close()
			log.Errorf("error receiving hello from [%s] (%v)", peer.Detail().Address, err)
			return
		}

		if err = peer.SetReadDeadline(time.Time{}); err != nil {
			log.Errorf("could not clear read timeout for [%s] (%v)", peer.Detail().Address, err)
			_ = peer.Close()
			return
		}

		for _, h := range self.handlers {
			if err = h.HandleConnection(hello, peer.PeerCertificates()); err != nil {
				break
			}
		}

		if err != nil {
			log.Errorf("connection handler error for [%s] (%v)", peer.Detail().Address, err)
			_ = peer.Close()
			return
		}

		impl.id = hello.IdToken
		impl.headers = hello.Headers

		if err := self.ackHello(impl, request, true, ""); err == nil {
			self.acceptF(impl)
		} else {
			log.Errorf("error acknowledging hello for [%s] (%v)", peer.Detail().Address, err)
			_ = peer.Close()
		}
	})
	if err != nil {
		log.WithError(err).Error("failed to queue connection accept")
	}
}

func (self *listener) receiveHello(impl *channel.DatagramUnderlay) (*channel.Message, *channel.Hello, error) {
	log := pfxlog.ContextLogger(impl.Label())
	log.Debug("started")
	defer log.Debug("exited")

	request, err := impl.Rx()
	if err != nil {
		if errors.Is(err, channel.BadMagicNumberError) {
			channel.WriteUnknownVersionResponse(impl.peer)
		}
		_ = impl.Close()
		return nil, nil, fmt.Errorf("receive error (%s)", err)
	}
	if request.ContentType != channel.ContentTypeHelloType {
		_ = impl.Close()
		return nil, nil, fmt.Errorf("unexpected content type [%d]", request.ContentType)
	}
	hello := channel.UnmarshalHello(request)
	return request, hello, nil
}

func (self *listener) ackHello(impl *channel.DatagramUnderlay, request *channel.Message, success bool, message string) error {
	response := channel.NewResult(success, message)

	for key, val := range self.headers {
		response.Headers[key] = val
	}

	response.Headers[channel.ConnectionIdHeader] = []byte(impl.connectionId)
	if self.identity != nil {
		response.PutStringHeader(channel.IdHeader, self.identity.Token)
	}
	response.SetSequence(channel.HelloSequence)

	response.ReplyTo(request)
	return impl.Tx(response)
}
