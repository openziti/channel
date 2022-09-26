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
	"fmt"
	"github.com/michaelquigley/pfxlog"
	"github.com/openziti/channel/v2"
	"github.com/openziti/identity"
	"github.com/openziti/transport/v2"
	"github.com/pkg/errors"
	"time"
)

type listener struct {
	identity *identity.TokenId
	peer     transport.Conn
	headers  map[int32][]byte
}

func NewListener(identity *identity.TokenId, peer transport.Conn, headers map[int32][]byte) channel.UnderlayFactory {
	return &listener{
		identity: identity,
		peer:     peer,
		headers:  headers,
	}
}

// TODO: need to restructure so we start after receiving hello and responding, but can also
//       respond to additional hellos after we're up and running, since initial response
//       may have gotten lost. Could add hello receive handler here
func (self *listener) Create(timeout time.Duration, _ transport.Configuration) (channel.Underlay, error) {
	log := pfxlog.Logger()

	impl := &Underlay{
		id:   self.identity,
		peer: self.peer,
	}

	connectionId, err := channel.NextConnectionId()
	if err != nil {
		return nil, errors.Wrap(err, "error getting connection id")
	}
	impl.connectionId = connectionId

	if timeout > 0 {
		defer func() {
			if err = self.peer.SetDeadline(time.Time{}); err != nil {
				log.WithError(err).Error("unable to clear deadline on conn after create")
			}
		}()

		if err = self.peer.SetDeadline(time.Now().Add(timeout)); err != nil {
			return nil, errors.Wrap(err, "could not set deadline on conn")
		}
	}

	request, hello, err := self.receiveHello(impl)
	if err != nil {
		return nil, errors.Wrap(err, "error receiving hello")
	}

	impl.id = &identity.TokenId{Token: hello.IdToken}
	impl.headers = hello.Headers

	if err = self.ackHello(impl, request, true, ""); err != nil {
		return nil, errors.Wrap(err, "unable to acknowledge hello")
	}

	return impl, nil
}

func (self *listener) receiveHello(impl *Underlay) (*channel.Message, *channel.Hello, error) {
	log := pfxlog.ContextLogger(impl.Label())
	log.Debug("started")
	defer log.Debug("exited")

	request, err := impl.Rx()
	if err != nil {
		if err == channel.UnknownVersionError {
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

func (self *listener) ackHello(impl *Underlay, request *channel.Message, success bool, message string) error {
	response := channel.NewResult(success, message)

	for key, val := range self.headers {
		response.Headers[key] = val
	}

	response.Headers[channel.ConnectionIdHeader] = []byte(impl.connectionId)
	response.SetSequence(channel.HelloSequence)

	response.ReplyTo(request)
	return impl.Tx(response)
}
