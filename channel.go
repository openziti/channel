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
	"context"
	"crypto/x509"
	"io"
	"net"
	"time"

	"github.com/openziti/transport/v2"
	"github.com/pkg/errors"
)

// Channel represents an asynchronous, message-passing framework, designed to sit on top of an underlay.
type Channel interface {
	Identity
	SetLogicalName(logicalName string)
	Sender
	UnderlayAcceptor
	io.Closer
	IsClosed() bool
	Underlay() Underlay
	Headers() map[int32][]byte
	GetTimeSinceLastRead() time.Duration
	GetUserData() interface{}
	// GetSenders returns the Senders for this channel. Consumers that need access to
	// priority-specific senders can type-assert the result to their concrete type:
	//
	//     mySenders := ch.GetSenders().(*MyCustomSenders)
	//     mySenders.GetHighPrioritySender().Send(msg)
	//
	// This replaces v4's MultiChannel.GetUnderlayHandler() pattern.
	GetSenders() Senders
	GetUnderlays() []Underlay
	GetUnderlayCountsByType() map[string]int
}

// UnderlayConstraint specifies the desired and minimum number of underlays for a given type.
type UnderlayConstraint struct {
	Desired int
	Min     int
}

// OneUnderlay returns a constraint requiring exactly one underlay.
func OneUnderlay() UnderlayConstraint {
	return UnderlayConstraint{Desired: 1, Min: 1}
}

// OptionalUnderlays returns a constraint with min 0 and the given desired count.
func OptionalUnderlays(desired int) UnderlayConstraint {
	return UnderlayConstraint{Desired: desired, Min: 0}
}

// Sender sends messages on a channel. It supports both blocking and non-blocking sends.
type Sender interface {
	// Send will send the given Sendable. If the Sender is busy, it will wait until either the Sender
	// can process the Sendable, the channel is closed or the associated context.Context times out
	Send(s Sendable) error

	// TrySend will send the given Sendable. If the Sender is busy (outgoing message queue is full), it will return
	// immediately rather than wait. The boolean return indicates whether the message was queued or not
	TrySend(s Sendable) (bool, error)

	// CloseNotify returns a channel that is closed when the sender is closed
	CloseNotify() <-chan struct{}
}

// Sendable encapsulates all the data and callbacks that a Channel requires when sending a Message.
type Sendable interface {
	// Msg returns the Message to send
	Msg() *Message

	// SetSequence sets a sequence number indicating in which order the message was received
	SetSequence(seq int32)

	// Sequence returns the sequence number
	Sequence() int32

	// Context returns the Context used for timeouts/cancelling message sends, etc
	Context() context.Context

	// SendListener returns the SendListener to invoke at each stage of the send operation
	SendListener() SendListener

	// ReplyReceiver returns the ReplyReceiver to be invoked when a reply for the message or received, or nil if
	// no ReplyReceiver should be invoked if or when a reply is received
	ReplyReceiver() ReplyReceiver
}

// Envelope allows setting message context and timeouts. Message is an Envelope (as well as a Sendable)
type Envelope interface {
	// WithTimeout returns a TimeoutEnvelope with a context using the given timeout
	WithTimeout(duration time.Duration) TimeoutEnvelope

	// WithContext returns a TimeoutEnvelope which uses the given context for timeout/cancellation
	WithContext(c context.Context) TimeoutEnvelope

	// Send sends the envelope on the given Channel
	Send(sender Sender) error

	// ReplyTo allows setting the reply header in a fluent style
	ReplyTo(msg *Message) Envelope

	// ToSendable converts the Envelope into a Sendable, which can be submitted to a Channel for sending
	ToSendable() Sendable
}

// TimeoutEnvelope has timeout related convenience methods, such as waiting for a Message to be written to
// the wire or waiting for a Message reply
type TimeoutEnvelope interface {
	Envelope

	// SendAndWaitForWire will wait until the configured timeout or until the message is sent, whichever comes first
	// If the timeout happens first, the context error will be returned, wrapped by a TimeoutError
	SendAndWaitForWire(sender Sender) error

	// SendForReply will wait until the configured timeout or until a reply is received, whichever comes first
	// If the timeout happens first, the context error will be returned, wrapped by a TimeoutError
	SendForReply(sender Sender) (*Message, error)
}

// SendListener is notified at the various stages of a message send
type SendListener interface {
	// NotifyQueued is called when the message has been queued for send
	NotifyQueued()
	// NotifyBeforeWrite is called before send is called
	NotifyBeforeWrite()
	// NotifyAfterWrite is called after the message has been written to the Underlay
	NotifyAfterWrite()
	// NotifyErr is called if the Sendable context errors before send or if writing to the Underlay fails
	NotifyErr(error)
}

// ReplyReceiver is used to get notified when a Message reply arrives
type ReplyReceiver interface {
	AcceptReply(*Message)
}

// Identity exposes the identifying information of a channel to callers and lower-level resources.
type Identity interface {
	// The Id used to represent the identity of this channel to lower-level resources.
	//
	Id() string

	// The LogicalName represents the purpose or usage of this channel (i.e. 'ctrl', 'mgmt' 'r/001', etc.) Usually used
	// by humans in understand the logical purpose of a channel.
	//
	LogicalName() string

	// The ConnectionId represents the identity of this Channel to internal API components ("instance identifier").
	// Usually used by the Channel framework to differentiate Channel instances.
	//
	ConnectionId() string

	// Certificates contains the identity certificates provided by the peer.
	//
	Certificates() []*x509.Certificate

	// Label constructs a consistently-formatted string used for context logging purposes, from the components above.
	//
	Label() string
}

// UnderlayListener represents a component designed to listen for incoming peer connections.
type UnderlayListener interface {
	Listen(handlers ...ConnectionHandler) error
	UnderlayFactory
	io.Closer
}

// UnderlayFactory is used by Channel to obtain an Underlay instance. An underlay "dialer" or "listener" implement
// UnderlayFactory, to provide instances to Channel.
type UnderlayFactory interface {
	Create(timeout time.Duration) (Underlay, error)
}

// DialUnderlayFactory extends UnderlayFactory with the ability to pass custom headers during creation.
// Used by DialPolicy to include connection metadata (type, connection ID, group secret) when dialing.
type DialUnderlayFactory interface {
	UnderlayFactory
	CreateWithHeaders(timeout time.Duration, headers map[int32][]byte) (Underlay, error)
}

// Underlay abstracts a physical communications channel, typically sitting on top of 'transport'.
type Underlay interface {
	Rx() (*Message, error)
	Tx(m *Message) error
	Identity
	io.Closer
	IsClosed() bool
	Headers() map[int32][]byte
	SetWriteTimeout(duration time.Duration) error
	SetWriteDeadline(time time.Time) error
	GetLocalAddr() net.Addr
	GetRemoteAddr() net.Addr
}

type classicUnderlay interface {
	Underlay
	getPeer() transport.Conn
	init(id string, connectionId string, headers Headers)
	rxHello() (*Message, error)
}

// AnyContentType is a wildcard content type used to register a fallback receive handler.
const AnyContentType = -1

// HelloSequence is the sequence number used for hello/handshake messages.
const HelloSequence = -1

// TimeoutError is used to indicate a timeout happened
type TimeoutError struct {
	error
}

func (self TimeoutError) Unwrap() error {
	return self.error
}

// IsTimeout returns true if the error is or wraps a TimeoutError.
func IsTimeout(err error) bool {
	return errors.As(err, &TimeoutError{})
}

// ClosedError indicates an operation was attempted on a closed channel.
type ClosedError struct{}

func (ClosedError) Error() string {
	return "channel closed"
}

// ListenerClosedError is returned when an operation is attempted on a closed listener.
var ListenerClosedError = listenerClosedError{}

type listenerClosedError struct{}

func (err listenerClosedError) Error() string {
	return "closed"
}

// BaseSendable is a type that may be used to provide default methods for Sendable implementation
type BaseSendable struct{}

func (BaseSendable) Msg() *Message {
	return nil
}

func (BaseSendable) Context() context.Context {
	return context.Background()
}

func (BaseSendable) SendListener() SendListener {
	return &BaseSendListener{}
}

func (BaseSendable) ReplyReceiver() ReplyReceiver {
	return nil
}

// BaseSendListener is a type that may be used to provide default methods for SendListener implementation
type BaseSendListener struct{}

func (BaseSendListener) NotifyQueued() {}

func (BaseSendListener) NotifyBeforeWrite() {}

func (BaseSendListener) NotifyAfterWrite() {}

func (BaseSendListener) NotifyErr(error) {}
