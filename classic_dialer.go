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
	"crypto/x509"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/michaelquigley/pfxlog"
	"github.com/openziti/identity"
	"github.com/openziti/transport/v2"
)

// HelloHeaderProvider adjusts the hello headers based on the peer's certificates. It is
// invoked after the transport connection is established (so the peer's certificates are
// available) but before the hello is sent, letting a dialer set hello headers that depend on
// the peer's verified identity. headers contains the headers already set on the hello
// (statically-configured and per-dial); the provider may add, modify or remove entries
// directly. Entries should be replaced rather than having their value slices edited in place,
// as values may be shared with the dialer's configuration. Returning an error aborts the dial,
// and that error is treated as non-retryable: the dialer does not open another connection to
// retry, since a provider that returns an error has made a deliberate decision about this
// specific peer.
type HelloHeaderProvider func(peerCertificates []*x509.Certificate, headers map[int32][]byte) error

// NonRetryableError marks an error as a deliberate, final dial failure that the dialer must not
// retry internally (in contrast to a protocol-version negotiation error, which is retried).
type NonRetryableError struct {
	err error
}

func (self *NonRetryableError) Error() string {
	if self.err == nil {
		return "non-retryable dial failure"
	}
	return self.err.Error()
}

func (self *NonRetryableError) Unwrap() error {
	return self.err
}

// IsNonRetryable reports whether err (or any error it wraps) is a NonRetryableError.
func IsNonRetryable(err error) bool {
	var nonRetryable *NonRetryableError
	return errors.As(err, &nonRetryable)
}

type classicDialer struct {
	identity            *identity.TokenId
	endpoint            transport.Address
	localBinding        string
	headers             map[int32][]byte
	underlayFactory     func(messageStrategy MessageStrategy, peer transport.Conn, version uint32) classicUnderlay
	messageStrategy     MessageStrategy
	transportConfig     transport.Configuration
	helloHeaderProvider HelloHeaderProvider
}

// DialerConfig holds configuration for creating a classic dialer.
type DialerConfig struct {
	Identity        *identity.TokenId
	Endpoint        transport.Address
	LocalBinding    string
	Headers         map[int32][]byte
	MessageStrategy MessageStrategy
	TransportConfig transport.Configuration
	// HelloHeaderProvider, if set, is invoked after the transport connection is established and
	// before the hello is sent, to compute additional hello headers from the peer's certificates.
	HelloHeaderProvider HelloHeaderProvider
}

// NewClassicDialer creates a DialUnderlayFactory that dials using the classic transport protocol.
func NewClassicDialer(cfg DialerConfig) DialUnderlayFactory {
	result := &classicDialer{
		identity:            cfg.Identity,
		endpoint:            cfg.Endpoint,
		localBinding:        cfg.LocalBinding,
		headers:             cfg.Headers,
		messageStrategy:     cfg.MessageStrategy,
		transportConfig:     cfg.TransportConfig,
		helloHeaderProvider: cfg.HelloHeaderProvider,
	}

	if cfg.Endpoint.Type() == "dtls" {
		result.underlayFactory = newDatagramUnderlay
	} else {
		result.underlayFactory = newClassicImpl
	}

	return result
}
func (self *classicDialer) Create(timeout time.Duration) (Underlay, error) {
	return self.CreateWithHeaders(timeout, nil)
}

func (self *classicDialer) CreateWithHeaders(timeout time.Duration, headers map[int32][]byte) (Underlay, error) {
	log := pfxlog.ContextLogger(self.endpoint.String())
	log.Debug("started")
	defer log.Debug("exited")

	if timeout == 0 {
		timeout = 15 * time.Second
	}

	deadline := time.Now().Add(timeout)

	version := uint32(2)
	tryCount := 0

	log.Debugf("Attempting to dial with bind: %s", self.localBinding)

	for time.Now().Before(deadline) {
		peer, err := self.endpoint.DialWithLocalBinding("classic", self.localBinding, self.identity, timeout, self.transportConfig)
		if err != nil {
			return nil, err
		}

		underlay := self.underlayFactory(self.messageStrategy, peer, version)
		if err = self.sendHello(underlay, deadline, headers); err != nil {
			if tryCount > 0 || IsNonRetryable(err) {
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

func (self *classicDialer) sendHello(underlay classicUnderlay, deadline time.Time, headers map[int32][]byte) (err error) {
	log := pfxlog.ContextLogger(underlay.Label())
	defer log.Debug("exited")
	log.Debug("started")

	// Close the underlay (and its transport FD) on any hello failure. Only a fully
	// successful handshake returns the underlay to the caller; every other path used to
	// return without closing, leaking the connection.
	defer func() {
		if err != nil {
			_ = underlay.Close()
		}
	}()

	peer := underlay.getPeer()

	if err = peer.SetDeadline(deadline); err != nil {
		return err
	}

	defer func() {
		if dlErr := peer.SetDeadline(time.Time{}); dlErr != nil { // clear write deadline
			log.WithError(dlErr).Error("unable to clear deadline")
		}
	}()

	request := NewHello(self.identity.Token, self.headers)
	maps.Copy(request.Headers, headers)

	// Let the caller adjust the hello headers based on the peer's (now-available)
	// certificates, e.g. to set headers that depend on the verified identity of the
	// peer we reached. The provider mutates the hello's headers directly.
	if self.helloHeaderProvider != nil {
		if err := self.helloHeaderProvider(peer.PeerCertificates(), request.Headers); err != nil {
			_ = underlay.Close()
			return &NonRetryableError{err: err}
		}
	}

	request.SetSequence(HelloSequence)
	if err = underlay.Tx(request); err != nil {
		return err
	}

	var response *Message
	response, err = underlay.Rx()
	if err != nil {
		if errors.Is(err, BadMagicNumberError) {
			return fmt.Errorf("could not negotiate connection with %v, invalid header", peer.RemoteAddr().String())
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

	// Use request.Headers (which includes any headers contributed by the HelloHeaderProvider)
	// rather than the pre-provider headers argument, so a provider-set ConnectionId drives the
	// local underlay's grouping as well as the outbound hello.
	connectionId := string(request.Headers[ConnectionIdHeader])
	if connectionId == "" {
		connectionId = string(response.Headers[ConnectionIdHeader])
	}
	id := ""

	if val, ok := response.GetStringHeader(IdHeader); ok {
		id = val
	} else if certs := underlay.Certificates(); len(certs) > 0 {
		id = certs[0].Subject.CommonName
	}

	// the type is set by the dialing side, not the listener. Use request.Headers (which includes any
	// headers contributed by the HelloHeaderProvider) rather than the pre-provider headers argument,
	// so a provider-set type drives the local underlay as well as the outbound hello.
	response.Headers[TypeHeader] = request.Headers[TypeHeader]
	underlay.init(id, connectionId, response.Headers)

	return nil
}
