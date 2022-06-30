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
	"errors"
	"fmt"
	"github.com/michaelquigley/pfxlog"
	"github.com/openziti/channel"
	"github.com/openziti/identity"
	"github.com/openziti/transport/v2"
	"time"
)

type dialer struct {
	id      *identity.TokenId
	peer    transport.Conn
	headers map[int32][]byte
}

func NewDatagramDialer(id *identity.TokenId, peer transport.Conn, headers map[int32][]byte) channel.UnderlayFactory {
	return &dialer{
		id:      id,
		peer:    peer,
		headers: headers,
	}
}

func (self *dialer) Create(timeout time.Duration, _ transport.Configuration) (channel.Underlay, error) {
	log := pfxlog.Logger()
	log.Debug("started")
	defer log.Debug("exited")

	if timeout < 10*time.Millisecond {
		return nil, errors.New("timeout must be at least 10ms")
	}

	version := uint32(2)

	defer func() {
		if err := self.peer.SetDeadline(time.Time{}); err != nil { // clear write deadline
			log.WithError(err).Error("unable to clear write deadline")
		}
	}()

	impl := &Underlay{
		id:   self.id,
		peer: self.peer,
	}

	deadline := time.Now().Add(timeout)

	for deadline.After(time.Now()) {
		if err := self.sendHello(impl); err != nil {
			if retryVersion, _ := channel.GetRetryVersion(err); retryVersion != version {
				version = retryVersion
			}

			log.Warnf("Retrying dial with protocol version %v", version)
			continue
		}
		impl.id = self.id
		return impl, nil
	}

	return nil, errors.New("connect timeout")
}

func (self *dialer) sendHello(impl *Underlay) error {
	log := pfxlog.ContextLogger(impl.Label())
	defer log.Debug("exited")
	log.Debug("started")

	request := channel.NewHello(self.id.Token, self.headers)
	request.SetSequence(channel.HelloSequence)
	if err := impl.Tx(request); err != nil {
		_ = impl.peer.Close()
		return err
	}

	if err := impl.peer.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		return err
	}

	defer func() {
		if err := self.peer.SetReadDeadline(time.Time{}); err != nil { // clear write deadline
			log.WithError(err).Error("unable to clear read deadline")
		}
	}()

	response, err := impl.Rx()
	if err != nil {
		return err
	}
	if !response.IsReplyingTo(channel.HelloSequence) || response.ContentType != channel.ContentTypeResultType {
		return fmt.Errorf("channel synchronization error, expected %v, got %v", channel.HelloSequence, response.ReplyFor())
	}
	result := channel.UnmarshalResult(response)
	if !result.Success {
		return errors.New(result.Message)
	}
	impl.connectionId = string(response.Headers[channel.ConnectionIdHeader])
	impl.headers = response.Headers

	return nil
}
