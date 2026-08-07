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
	"testing"
	"time"

	"github.com/openziti/identity"
	"github.com/openziti/transport/v2"
	"github.com/openziti/transport/v2/tcp"
	"github.com/stretchr/testify/require"
)

func Test_RejectHello_SendsNegativeResultAndCloses(t *testing.T) {
	underlay := newGroupedTestUnderlay("conn-1", true)

	require.NoError(t, RejectHello(underlay, NewRejectedError(RejectClassBusy, "at capacity")))

	require.True(t, underlay.closed, "a rejected underlay must be closed")
	require.Len(t, underlay.sent, 1, "a rejection must send exactly one message")

	sent := underlay.sent[0]
	require.Equal(t, int32(ContentTypeResultType), sent.ContentType)
	require.True(t, sent.IsReplyingTo(HelloSequence),
		"a rejection must reply to the hello, or the dialer will treat it as a synchronization error")

	result := UnmarshalResult(sent)
	require.False(t, result.Success)
	require.Equal(t, "at capacity", result.Message)
	require.Equal(t, RejectClassBusy, getRejectClass(sent))
}

// Test_RejectHello_UnclassifiedError verifies an application that returns a plain error still produces
// a usable refusal: the message travels, and the class defaults to unspecified rather than to a
// meaning nobody chose.
func Test_RejectHello_UnclassifiedError(t *testing.T) {
	underlay := newGroupedTestUnderlay("conn-1", true)

	require.NoError(t, RejectHello(underlay, errors.New("no room")))

	sent := underlay.sent[0]
	require.Equal(t, "no room", UnmarshalResult(sent).Message)
	require.Equal(t, RejectClassUnspecified, getRejectClass(sent))
	_, found := sent.GetUint32Header(HelloRejectClassHeader)
	require.False(t, found, "an unspecified class should be sent as no header, not as a zero")
}

func Test_RejectHello_ClosesEvenWhenSendFails(t *testing.T) {
	underlay := &failingTxUnderlay{
		testUnderlay: testUnderlay{connectionId: "conn-1"},
		err:          errors.New("tx failed"),
	}

	err := RejectHello(underlay, NewRejectedError(RejectClassBusy, "at capacity"))

	require.Error(t, err, "a failed send must be reported")
	require.True(t, underlay.closed, "the underlay must be closed even when the refusal cannot be sent")
}

func Test_GetRejectClass(t *testing.T) {
	require.Equal(t, RejectClassUnspecified, GetRejectClass(nil))
	require.Equal(t, RejectClassUnspecified, GetRejectClass(errors.New("plain")))
	require.Equal(t, RejectClassBusy, GetRejectClass(NewRejectedError(RejectClassBusy, "busy")))
	require.Equal(t, RejectClassBusy, GetRejectClass(
		fmt.Errorf("wrapped: %w", NewRejectedError(RejectClassBusy, "busy"))),
		"a wrapped rejection must still be classifiable")
}

func Test_RejectedError_Message(t *testing.T) {
	require.Equal(t, "at capacity", NewRejectedError(RejectClassBusy, "at capacity").Error())
	require.Equal(t, "hello rejected", NewRejectedError(RejectClassUnspecified, "").Error())
	require.Equal(t, "hello rejected: busy", NewRejectedError(RejectClassBusy, "").Error())
}

// Test_RejectHello_DialerReceivesClassifiedRefusal exercises the whole path over a real connection: an
// Admitter refuses a channel, and the dialer's create fails with the classification rather than with an
// unexplained connection close.
func Test_RejectHello_DialerReceivesClassifiedRefusal(t *testing.T) {
	transport.AddAddressParser(tcp.AddressParser{})
	req := require.New(t)

	listenAddr, err := transport.ParseAddress("tcp:0.0.0.0:6771")
	req.NoError(err)
	dialAddr, err := transport.ParseAddress("tcp:127.0.0.1:6771")
	req.NoError(err)

	multiListener := NewMultiListenerWithConfig(MultiListenerConfig{
		Factory: func(Underlay, func()) (Channel, error) {
			return nil, errors.New("factory should not be reached for a refused channel")
		},
		UngroupedChannelFallback: func(Underlay) error {
			return errors.New("ungrouped not expected")
		},
		Admitter: func(Underlay) error {
			return NewRejectedError(RejectClassBusy, "controller at capacity")
		},
	})

	listener, err := NewClassicListenerWithAcceptor(&identity.TokenId{Token: "test-server"}, listenAddr,
		ListenerConfig{ConnectOptions: DefaultConnectOptions()}, multiListener)
	req.NoError(err)
	defer func() { _ = listener.Close() }()

	dialer := NewClassicDialer(DialerConfig{
		Identity: &identity.TokenId{Token: "test-client"},
		Endpoint: dialAddr,
	})

	headers := Headers{}
	headers.PutStringHeader(TypeHeader, "default")
	headers.PutBoolHeader(IsGroupedHeader, true)
	headers.PutBoolHeader(IsFirstGroupConnection, true)

	underlay, err := dialer.CreateWithHeaders(2*time.Second, headers)
	req.Error(err, "a refused channel must fail the dial")
	if underlay != nil {
		_ = underlay.Close()
	}

	var rejected *RejectedError
	req.True(errors.As(err, &rejected), "a refusal must reach the dialer as a RejectedError, got %T: %v", err, err)
	req.Equal(RejectClassBusy, rejected.Class)
	req.Equal("controller at capacity", rejected.Message)
}
