/*
	Copyright 2019 Netfoundry, Inc.

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

package channel2

import (
	"github.com/netfoundry/ziti-foundation/identity/identity"
	"github.com/netfoundry/ziti-foundation/transport"
	"errors"
	"fmt"
	"github.com/michaelquigley/pfxlog"
	"io"
)

type classicListener struct {
	identity *identity.TokenId
	endpoint transport.Address
	socket   io.Closer
	close    chan struct{}
	handlers []ConnectionHandler
	created  chan Underlay
}

func NewClassicListener(identity *identity.TokenId, endpoint transport.Address) UnderlayListener {
	return &classicListener{
		identity: identity,
		endpoint: endpoint,
		close:    make(chan struct{}),
		created:  make(chan Underlay),
	}
}

func (listener *classicListener) Listen(handlers ...ConnectionHandler) error {
	incoming := make(chan transport.Connection)
	socket, err := listener.endpoint.Listen("classic", listener.identity, incoming)
	if err != nil {
		return err
	}
	listener.socket = socket
	listener.handlers = handlers
	go listener.listener(incoming)

	return nil
}

func (listener *classicListener) Close() error {
	close(listener.close)
	close(listener.created)
	if err := listener.socket.Close(); err != nil {
		return err
	}
	listener.socket = nil
	return nil
}

func (listener *classicListener) Create() (Underlay, error) {
	if listener.created == nil {
		return nil, errors.New("closed")
	}
	impl := <-listener.created
	if impl == nil {
		return nil, errors.New("closed")
	}
	return impl, nil
}

func (listener *classicListener) listener(incoming chan transport.Connection) {
	log := pfxlog.ContextLogger(listener.endpoint.String())
	log.Debug("started")
	defer log.Debug("exited")

	for {
		select {
		case peer := <-incoming:
			impl := newClassicImpl(peer, 2)
			if connectionId, err := globalRegistry.newConnectionId(); err == nil {
				impl.connectionId = connectionId
				request, hello, err := listener.receiveHello(impl)
				if err == nil {
					for _, h := range listener.handlers {
						log.Infof("hello: %v, peer: %v, handler: %v", hello, peer, h)
						if err := h.HandleConnection(hello, peer.PeerCertificates()); err != nil {
							log.Errorf("connection handler error (%s)", err)
							if err := listener.ackHello(impl, request, false, err.Error()); err != nil {
								log.Errorf("error acknowledging hello (%s)", err)
							}
							break
						}
					}

					impl.id = &identity.TokenId{Token: hello.IdToken}
					impl.headers = hello.Headers

					if err := listener.ackHello(impl, request, true, ""); err == nil {
						listener.created <- impl
					} else {
						log.Errorf("error acknowledging hello (%s)", err)
					}

				} else {
					log.Errorf("error receiving hello (%s)", err)
				}
			} else {
				log.Errorf("error getting connection id (%s)", err)
			}

		case <-listener.close:
			return
		}
	}
}

func (listener *classicListener) receiveHello(impl *classicImpl) (*Message, *Hello, error) {
	log := pfxlog.ContextLogger(impl.Label())
	log.Debug("started")
	defer log.Debug("exited")

	request, err := impl.rxHello()
	if err != nil {
		if err == UnknownVersionError {
			writeUnknownVersionResponse(impl.peer.Writer())
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
	response.Headers[ConnectionIdHeader] = []byte(impl.connectionId)
	response.sequence = HelloSequence
	response.ReplyTo(request)
	return impl.Tx(response)
}
