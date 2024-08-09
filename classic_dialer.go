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
	"errors"
	"fmt"
	"github.com/michaelquigley/pfxlog"
	"github.com/openziti/identity"
	"github.com/openziti/transport/v2"
	"time"
)

type classicDialer struct {
	identity        *identity.TokenId
	endpoint        transport.Address
	localBinding    string
	headers         map[int32][]byte
	underlayFactory func(peer transport.Conn, deadline time.Time, version uint32) (Underlay, error)
}

func NewClassicDialerWithBindAddress(identity *identity.TokenId, endpoint transport.Address, localBinding string, headers map[int32][]byte) UnderlayFactory {
	result := &classicDialer{
		identity:     identity,
		endpoint:     endpoint,
		localBinding: localBinding,
		headers:      headers,
	}

	if endpoint.Type() == "dtls" {
		result.underlayFactory = result.createDatagramUnderlay
	} else {
		result.underlayFactory = result.createClassicUnderlay
	}

	return result
}

func NewClassicDialer(identity *identity.TokenId, endpoint transport.Address, headers map[int32][]byte) UnderlayFactory {
	return NewClassicDialerWithBindAddress(identity, endpoint, "", headers)
}

func (self *classicDialer) Create(timeout time.Duration, tcfg transport.Configuration) (Underlay, error) {
	log := pfxlog.ContextLogger(self.endpoint.String())
	log.Debug("started")
	defer log.Debug("exited")

	deadline := time.Now().Add(timeout)

	version := uint32(2)
	tryCount := 0

	log.Debugf("Attempting to dial with bind: %s", self.localBinding)

	for time.Now().After(deadline) {
		peer, err := self.endpoint.DialWithLocalBinding("classic", self.localBinding, self.identity, timeout, tcfg)
		if err != nil {
			return nil, err
		}

		var underlay Underlay
		if underlay, err = self.underlayFactory(peer, deadline, version); err != nil {
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
		return underlay, nil
	}
	return nil, errors.New("timeout waiting for dial")
}

func (self *classicDialer) createClassicUnderlay(peer transport.Conn, deadline time.Time, version uint32) (Underlay, error) {
	impl := newClassicImpl(peer, version)
	if err := self.sendHello(impl, deadline); err != nil {
		return nil, err
	}
	return impl, nil
}

func (self *classicDialer) sendHello(impl *classicImpl, deadline time.Time) error {
	log := pfxlog.ContextLogger(impl.Label())
	defer log.Debug("exited")
	log.Debug("started")

	if err := impl.peer.SetReadDeadline(deadline); err != nil {
		return err
	}

	defer func() {
		_ = impl.peer.SetReadDeadline(time.Time{})
	}()

	request := NewHello(self.identity.Token, self.headers)
	request.sequence = HelloSequence
	if err := impl.Tx(request); err != nil {
		_ = impl.peer.Close()
		return err
	}

	response, err := impl.Rx()
	if err != nil {
		if errors.Is(err, BadMagicNumberError) {
			return fmt.Errorf("could not negotiate connection with %v, invalid header", impl.peer.RemoteAddr().String())
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
	impl.headers = response.Headers

	if id, ok := response.GetStringHeader(IdHeader); ok {
		impl.id = id
	} else if certs := impl.Certificates(); len(certs) > 0 {
		impl.id = certs[0].Subject.CommonName
	}

	return nil
}

func (self *classicDialer) createDatagramUnderlay(peer transport.Conn, deadline time.Time, version uint32) (Underlay, error) {
	log := pfxlog.ContextLogger(self.endpoint.String())

	defer func() {
		if err := peer.SetDeadline(time.Time{}); err != nil { // clear write deadline
			log.WithError(err).Error("unable to clear write deadline")
		}
	}()

	impl := NewDatagramUnderlay(self.identity.Token, peer)
	if err := self.sendDatagramHello(impl, deadline); err != nil {
		return nil, err
	}
	return impl, nil
}

func (self *classicDialer) sendDatagramHello(impl *DatagramUnderlay, deadline time.Time) error {
	log := pfxlog.ContextLogger(impl.Label())
	defer log.Debug("exited")
	log.Debug("started")

	request := NewHello(self.identity.Token, self.headers)
	request.SetSequence(HelloSequence)
	if err := impl.Tx(request); err != nil {
		_ = impl.peer.Close()
		return err
	}

	if err := impl.peer.SetReadDeadline(deadline); err != nil {
		return err
	}

	defer func() {
		if err := impl.peer.SetReadDeadline(time.Time{}); err != nil { // clear write deadline
			log.WithError(err).Error("unable to clear read deadline")
		}
	}()

	response, err := impl.Rx()
	if err != nil {
		if errors.Is(err, BadMagicNumberError) {
			return fmt.Errorf("could not negotiate connection with %v, invalid header", impl.peer.RemoteAddr().String())
		}
		return err
	}
	if !response.IsReplyingTo(HelloSequence) || response.ContentType != ContentTypeResultType {
		return fmt.Errorf("channel synchronization error, expected %v, got %v", HelloSequence, response.ReplyFor())
	}
	result := UnmarshalResult(response)
	if !result.Success {
		return errors.New(result.Message)
	}
	impl.connectionId = string(response.Headers[ConnectionIdHeader])
	impl.headers = response.Headers

	if id, ok := response.GetStringHeader(IdHeader); ok {
		impl.id = id
	} else if certs := impl.Certificates(); len(certs) > 0 {
		impl.id = certs[0].Subject.CommonName
	}

	return nil
}
