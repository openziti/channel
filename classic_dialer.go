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
	"github.com/openziti/identity"
	"github.com/openziti/transport/v2"
	"github.com/pkg/errors"
	"time"
)

type classicDialer struct {
	identity     *identity.TokenId
	endpoint     transport.Address
	localBinding string
	headers      map[int32][]byte
}

func NewClassicDialerWithBindAddress(identity *identity.TokenId, endpoint transport.Address, localBinding string, headers map[int32][]byte) UnderlayFactory {
	return &classicDialer{
		identity:     identity,
		endpoint:     endpoint,
		localBinding: localBinding,
		headers:      headers,
	}
}

func NewClassicDialer(identity *identity.TokenId, endpoint transport.Address, headers map[int32][]byte) UnderlayFactory {
	return NewClassicDialerWithBindAddress(identity, endpoint, "", headers)
}

func (dialer *classicDialer) Create(timeout time.Duration, tcfg transport.Configuration) (Underlay, error) {
	log := pfxlog.ContextLogger(dialer.endpoint.String())
	log.Debug("started")
	defer log.Debug("exited")

	version := uint32(2)
	tryCount := 0

	log.Debugf("Attempting to dial with bind: %s", dialer.localBinding)

	for {
		peer, err := dialer.endpoint.DialWithLocalBinding("classic", dialer.localBinding, dialer.identity, timeout, tcfg)
		if err != nil {
			return nil, err
		}

		impl := newClassicImpl(peer, version)
		if err := dialer.sendHello(impl); err != nil {
			if tryCount > 0 {
				return nil, err
			} else {
				log.WithError(err).Warnf("error initiating channel with hello")
			}
			tryCount++
			version, _ = GetRetryVersion(err)
			log.Warnf("Retrying dial with protocol version %v", version)
			continue
		}
		return impl, nil
	}
}

func (dialer *classicDialer) sendHello(impl *classicImpl) error {
	log := pfxlog.ContextLogger(impl.Label())
	defer log.Debug("exited")
	log.Debug("started")

	request := NewHello(dialer.identity.Token, dialer.headers)
	request.sequence = HelloSequence
	if err := impl.Tx(request); err != nil {
		_ = impl.peer.Close()
		return err
	}

	response, err := impl.Rx()
	if err != nil {
		if errors.Is(err, BadMagicNumberError) {
			return errors.Errorf("could not negotiate connection with %v, invalid header", impl.peer.RemoteAddr().String())
		}
		return err
	}
	if !response.IsReplyingTo(request.sequence) || response.ContentType != ContentTypeResultType {
		return fmt.Errorf("channel synchronization error, expected %v, got %v", request.sequence, response.ReplyFor())
	}
	result := UnmarshalResult(response)
	if !result.Success {
		return errors.New(result.Message)
	}
	impl.connectionId = string(response.Headers[ConnectionIdHeader])

	if id, ok := response.GetStringHeader(IdHeader); ok {
		impl.id = &identity.TokenId{Token: id}
	} else if certs := impl.Certificates(); len(certs) > 0 {
		impl.id = &identity.TokenId{Token: certs[0].Subject.CommonName}
	}

	impl.headers = response.Headers

	return nil
}
