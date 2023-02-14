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
	"fmt"
	"github.com/michaelquigley/pfxlog"
	"github.com/openziti/foundation/v2/goroutines"
	"github.com/openziti/identity"
	"github.com/openziti/transport/v2"
	"github.com/sirupsen/logrus"
	"io"
	"sync/atomic"
	"time"
)

type classicListener struct {
	identity       *identity.TokenId
	endpoint       transport.Address
	socket         io.Closer
	close          chan struct{}
	handlers       []ConnectionHandler
	created        chan Underlay
	connectOptions ConnectOptions
	tcfg           transport.Configuration
	headers        map[int32][]byte
	closed         atomic.Bool
	listenerPool   goroutines.Pool
}

func DefaultListenerConfig() ListenerConfig {
	return ListenerConfig{
		ConnectOptions: DefaultConnectOptions(),
	}
}

type ListenerConfig struct {
	ConnectOptions
	Headers          map[int32][]byte
	TransportConfig  transport.Configuration
	PoolConfigurator func(config *goroutines.PoolConfig)
}

func NewClassicListener(identity *identity.TokenId, endpoint transport.Address, config ListenerConfig) UnderlayListener {
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

	return &classicListener{
		identity:       identity,
		endpoint:       endpoint,
		close:          closeNotify,
		created:        make(chan Underlay),
		connectOptions: config.ConnectOptions,
		tcfg:           config.TransportConfig,
		headers:        config.Headers,
		listenerPool:   pool,
	}
}

func (listener *classicListener) Listen(handlers ...ConnectionHandler) error {
	listener.handlers = handlers
	socket, err := listener.endpoint.Listen("classic", listener.identity, listener.acceptConnection, listener.tcfg)
	if err != nil {
		return err
	}
	listener.socket = socket
	return nil
}

func (listener *classicListener) Close() error {
	if listener.closed.CompareAndSwap(false, true) {
		close(listener.close)
		if socket := listener.socket; socket != nil {
			if err := socket.Close(); err != nil {
				return err
			}
		}
		listener.socket = nil
	}
	return nil
}

func (listener *classicListener) Create(_ time.Duration, _ transport.Configuration) (Underlay, error) {
	select {
	case impl := <-listener.created:
		if impl != nil {
			return impl, nil
		}
	case <-listener.close:
	}
	return nil, ListenerClosedError
}

func (listener *classicListener) acceptConnection(peer transport.Conn) {
	log := pfxlog.ContextLogger(listener.endpoint.String())
	err := listener.listenerPool.Queue(func() {
		impl := newClassicImpl(peer, 2)

		var err error
		impl.connectionId, err = NextConnectionId()
		if err != nil {
			_ = peer.Close()
			log.Errorf("error getting connection id for [%s] (%v)", peer.Detail().Address, err)
			return
		}

		if err = peer.SetReadDeadline(time.Now().Add(listener.connectOptions.ConnectTimeout)); err != nil {
			log.Errorf("could not set read timeout for [%s] (%v)", peer.Detail().Address, err)
			_ = peer.Close()
			return
		}

		request, hello, err := listener.receiveHello(impl)
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

		for _, h := range listener.handlers {
			if err = h.HandleConnection(hello, peer.PeerCertificates()); err != nil {
				break
			}
		}

		if err != nil {
			log.Errorf("connection handler error for [%s] (%v)", peer.Detail().Address, err)
			_ = peer.Close()
			return
		}

		impl.id = &identity.TokenId{Token: hello.IdToken}
		impl.headers = hello.Headers

		if err := listener.ackHello(impl, request, true, ""); err == nil {
			select {
			case listener.created <- impl:
			case <-listener.close:
				log.Infof("channel closed, can't notify of new connection [%s] (%v)", peer.Detail().Address, err)
				return
			}
		} else {
			log.Errorf("error acknowledging hello for [%s] (%v)", peer.Detail().Address, err)
		}
	})
	if err != nil {
		log.WithError(err).Error("failed to queue connection accept")
	}
}

func (listener *classicListener) receiveHello(impl *classicImpl) (*Message, *Hello, error) {
	log := pfxlog.ContextLogger(impl.Label())
	log.Debug("started")
	defer log.Debug("exited")

	request, err := impl.rxHello()
	if err != nil {
		if err == BadMagicNumberError {
			WriteUnknownVersionResponse(impl.peer)
		}
		_ = impl.Close()
		return nil, nil, fmt.Errorf("receive error (%s)", err)
	}
	if request.ContentType != ContentTypeHelloType {
		_ = impl.Close()
		return nil, nil, fmt.Errorf("unexpected content type [%d]", request.ContentType)
	}
	hello := UnmarshalHello(request)
	return request, hello, nil
}

func (listener *classicListener) ackHello(impl *classicImpl, request *Message, success bool, message string) error {
	response := NewResult(success, message)

	for key, val := range listener.headers {
		response.Headers[key] = val
	}

	response.PutStringHeader(ConnectionIdHeader, impl.connectionId)
	if listener.identity != nil {
		response.PutStringHeader(IdHeader, listener.identity.Token)
	}
	response.sequence = HelloSequence

	response.ReplyTo(request)
	return impl.Tx(response)
}
