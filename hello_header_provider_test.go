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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openziti/identity"
	"github.com/openziti/transport/v2"
	"github.com/openziti/transport/v2/tcp"
	transporttls "github.com/openziti/transport/v2/tls"
	"github.com/stretchr/testify/require"
)

const (
	helloHeaderProviderTestHeader = 19876
	helloHeaderProviderBaseHeader = 19877
)

// Test_HelloHeaderProvider_HeadersReachListener verifies that headers set by a
// HelloHeaderProvider reach the listener via the hello, and that the provider receives
// the current hello headers (statically-configured and per-dial) so it can derive new
// values from existing ones.
func Test_HelloHeaderProvider_HeadersReachListener(t *testing.T) {
	transport.AddAddressParser(tcp.AddressParser{})
	req := require.New(t)

	listenAddr, err := transport.ParseAddress("tcp:0.0.0.0:6768")
	req.NoError(err)
	dialAddr, err := transport.ParseAddress("tcp:127.0.0.1:6768")
	req.NoError(err)

	receivedHeaders := make(chan map[int32][]byte, 1)
	acceptF := func(underlay Underlay) {
		select {
		case receivedHeaders <- underlay.Headers():
		default:
		}
	}

	listener, err := NewClassicListenerF(&identity.TokenId{Token: "test-server"}, listenAddr,
		ListenerConfig{ConnectOptions: DefaultConnectOptions()}, acceptF)
	req.NoError(err)
	defer func() { _ = listener.Close() }()

	var providerCalled atomic.Bool
	const providerConnId = "provider-conn-id"
	const providerType = "provider-type"
	const dialConnId = "dial-conn-id"
	dialer := NewClassicDialer(DialerConfig{
		Identity: &identity.TokenId{Token: "test-client"},
		Endpoint: dialAddr,
		Headers: map[int32][]byte{
			helloHeaderProviderBaseHeader: []byte("base"),
		},
		HelloHeaderProvider: func(peerCertificates []*x509.Certificate, headers map[int32][]byte) error {
			providerCalled.Store(true)

			// the provider sees both statically-configured and per-dial headers
			if string(headers[helloHeaderProviderBaseHeader]) != "base" {
				return errors.New("statically-configured header not visible to provider")
			}
			if string(headers[ConnectionIdHeader]) != dialConnId {
				return errors.New("per-dial header not visible to provider")
			}

			// derive a new value from an existing header
			headers[helloHeaderProviderBaseHeader] = []byte(string(headers[helloHeaderProviderBaseHeader]) + "+appended")
			headers[helloHeaderProviderTestHeader] = []byte("injected")
			headers[ConnectionIdHeader] = []byte(providerConnId)
			headers[TypeHeader] = []byte(providerType)
			return nil
		},
	})

	underlay, err := dialer.CreateWithHeaders(2*time.Second, map[int32][]byte{
		ConnectionIdHeader: []byte(dialConnId),
	})
	req.NoError(err)
	defer func() { _ = underlay.Close() }()

	req.True(providerCalled.Load(), "HelloHeaderProvider should be invoked during the dial")
	req.Equal(providerConnId, underlay.ConnectionId(),
		"a provider-set ConnectionId should drive the local underlay's grouping, not just the hello")
	req.Equal(providerType, GetUnderlayType(underlay),
		"a provider-set type should drive the local underlay's type, not just the hello")

	select {
	case h := <-receivedHeaders:
		req.Equal([]byte("injected"), h[helloHeaderProviderTestHeader],
			"header injected by the provider should reach the listener via the hello")
		req.Equal([]byte("base+appended"), h[helloHeaderProviderBaseHeader],
			"a provider-modified header derived from an existing one should reach the listener")
	case <-time.After(2 * time.Second):
		req.Fail("listener did not receive an underlay")
	}
}

// Test_HelloHeaderProvider_PeerCertificatesOverTls verifies that the provider is invoked with
// the peer's verified certificates when dialing over TLS, and that a header derived from those
// certificates reaches the listener.
func Test_HelloHeaderProvider_PeerCertificatesOverTls(t *testing.T) {
	transport.AddAddressParser(transporttls.AddressParser{})
	req := require.New(t)

	serverId, clientId := newTestTlsIdentities(req)

	listenAddr, err := transport.ParseAddress("tls:0.0.0.0:6770")
	req.NoError(err)
	dialAddr, err := transport.ParseAddress("tls:127.0.0.1:6770")
	req.NoError(err)

	type acceptResult struct {
		headers map[int32][]byte
		certs   []*x509.Certificate
	}
	accepted := make(chan acceptResult, 1)
	acceptF := func(underlay Underlay) {
		select {
		case accepted <- acceptResult{headers: underlay.Headers(), certs: underlay.Certificates()}:
		default:
		}
	}

	listener, err := NewClassicListenerF(serverId, listenAddr,
		ListenerConfig{ConnectOptions: DefaultConnectOptions()}, acceptF)
	req.NoError(err)
	defer func() { _ = listener.Close() }()

	dialer := NewClassicDialer(DialerConfig{
		Identity: clientId,
		Endpoint: dialAddr,
		HelloHeaderProvider: func(peerCertificates []*x509.Certificate, headers map[int32][]byte) error {
			if len(peerCertificates) == 0 {
				return errors.New("no peer certificates available to provider")
			}
			// derive a header from the peer's verified identity
			headers[helloHeaderProviderTestHeader] = []byte("peer:" + peerCertificates[0].Subject.CommonName)
			return nil
		},
	})

	underlay, err := dialer.CreateWithHeaders(2*time.Second, nil)
	req.NoError(err)
	defer func() { _ = underlay.Close() }()

	select {
	case result := <-accepted:
		req.Equal([]byte("peer:test-server"), result.headers[helloHeaderProviderTestHeader],
			"header derived from the peer's certificates should reach the listener via the hello")
		req.NotEmpty(result.certs, "listener should see the client's certificates")
		req.Equal("test-client", result.certs[0].Subject.CommonName)
	case <-time.After(2 * time.Second):
		req.Fail("listener did not receive an underlay")
	}
}

// Test_HelloHeaderProvider_ErrorAbortsDial verifies that a HelloHeaderProvider returning an
// error aborts the dial.
func Test_HelloHeaderProvider_ErrorAbortsDial(t *testing.T) {
	transport.AddAddressParser(tcp.AddressParser{})
	req := require.New(t)

	listenAddr, err := transport.ParseAddress("tcp:0.0.0.0:6769")
	req.NoError(err)
	dialAddr, err := transport.ParseAddress("tcp:127.0.0.1:6769")
	req.NoError(err)

	listener, err := NewClassicListenerF(&identity.TokenId{Token: "test-server"}, listenAddr,
		ListenerConfig{ConnectOptions: DefaultConnectOptions()}, func(underlay Underlay) {})
	req.NoError(err)
	defer func() { _ = listener.Close() }()

	var providerCalls atomic.Int32
	dialer := NewClassicDialer(DialerConfig{
		Identity: &identity.TokenId{Token: "test-client"},
		Endpoint: dialAddr,
		HelloHeaderProvider: func(peerCertificates []*x509.Certificate, headers map[int32][]byte) error {
			providerCalls.Add(1)
			return errors.New("provider rejected the connection")
		},
	})

	_, err = dialer.CreateWithHeaders(2*time.Second, nil)
	req.Error(err, "a provider error should abort the dial")
	req.True(IsNonRetryable(err), "a provider error should be reported as non-retryable")
	req.Equal(int32(1), providerCalls.Load(),
		"a provider error must not be retried: the provider should be invoked exactly once")
}

// newTestTlsIdentities generates an in-memory test PKI (CA plus server and client leaf certs)
// and returns identities usable for a TLS listener and dialer.
func newTestTlsIdentities(req *require.Assertions) (serverId *identity.TokenId, clientId *identity.TokenId) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	req.NoError(err)

	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDer, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	req.NoError(err)
	caCert, err := x509.ParseCertificate(caDer)
	req.NoError(err)
	caPem := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDer}))

	newLeafIdentity := func(cn string, serial int64, extKeyUsage x509.ExtKeyUsage, isServer bool) *identity.TokenId {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		req.NoError(err)

		template := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: cn},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{extKeyUsage},
			IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
			DNSNames:     []string{"localhost"},
		}
		der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
		req.NoError(err)
		keyDer, err := x509.MarshalECPrivateKey(key)
		req.NoError(err)

		cfg := identity.Config{
			Key:  "pem:" + string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer})),
			Cert: "pem:" + string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
			CA:   "pem:" + caPem,
		}
		if isServer {
			cfg.ServerCert = cfg.Cert
		}

		id, err := identity.LoadIdentity(cfg)
		req.NoError(err)
		return identity.NewIdentity(id)
	}

	serverId = newLeafIdentity("test-server", 2, x509.ExtKeyUsageServerAuth, true)
	clientId = newLeafIdentity("test-client", 3, x509.ExtKeyUsageClientAuth, false)
	return serverId, clientId
}
