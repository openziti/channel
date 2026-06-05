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
)

// ConnectionHandler handles new incoming connections during the hello/handshake phase.
type ConnectionHandler interface {
	HandleConnection(hello *Hello, certificates []*x509.Certificate) error
}

// PeekHandler observes messages as they flow through the channel without modifying them.
type PeekHandler interface {
	Connect(ch Channel, remoteAddress string)
	Rx(m *Message, ch Channel)
	Tx(m *Message, ch Channel)
	Close(ch Channel)
}

// TransformHandler can modify messages as they flow through the channel.
type TransformHandler interface {
	Rx(m *Message, ch Channel)
	Tx(m *Message, ch Channel)
}

// ReceiveHandler handles received messages for a specific content type.
type ReceiveHandler interface {
	HandleReceive(m *Message, ch Channel)
}

// ReceiveHandlerF is the function form of ReceiveHandler.
type ReceiveHandlerF func(m *Message, ch Channel)

func (self ReceiveHandlerF) HandleReceive(m *Message, ch Channel) {
	self(m, ch)
}

// ContentTypeReceiver is a receive handler which reports the content type it handles.
// It allows handlers to be registered without separately specifying the content type,
// using AddReceiveHandlers.
type ContentTypeReceiver interface {
	ContentType() int32
	ReceiveHandler
}

// AddReceiveHandlers registers receive handlers which provide their own content type.
func AddReceiveHandlers(binding Binding, handlers ...ContentTypeReceiver) {
	for _, h := range handlers {
		binding.AddReceiveHandler(h.ContentType(), h)
	}
}

// AsyncFunctionReceiveAdapter is a ContentTypeReceiver which handles each message
// in a new goroutine, for handlers which may block or run long. Receive handlers
// are otherwise invoked on the channel's receive loop.
type AsyncFunctionReceiveAdapter struct {
	Type    int32
	Handler ReceiveHandlerF
}

// ContentType returns the message content type this handler processes.
func (adapter *AsyncFunctionReceiveAdapter) ContentType() int32 {
	return adapter.Type
}

// HandleReceive dispatches the message to the wrapped handler in a new goroutine.
func (adapter *AsyncFunctionReceiveAdapter) HandleReceive(m *Message, ch Channel) {
	go adapter.Handler(m, ch)
}

// MsgReceiveHandler handles received messages without channel context.
type MsgReceiveHandler interface {
	HandleReceive(m *Message)
}

// MsgReceiveHandlerF is the function form of MsgReceiveHandler.
type MsgReceiveHandlerF func(m *Message)

func (self MsgReceiveHandlerF) HandleReceive(m *Message) {
	self(m)
}

// TypedReceiveHandler is a receive handler that gets typed senders access.
type TypedReceiveHandler[S Senders] interface {
	HandleReceive(m *Message, ch Channel, senders S)
}

// TypedReceiveHandlerF is the function form of TypedReceiveHandler.
type TypedReceiveHandlerF[S Senders] func(m *Message, ch Channel, senders S)

func (self TypedReceiveHandlerF[S]) HandleReceive(m *Message, ch Channel, senders S) {
	self(m, ch, senders)
}

// ErrorHandler handles errors that occur during channel operations.
type ErrorHandler interface {
	HandleError(err error, ch Channel)
}

// ErrorHandlerF is the function form of ErrorHandler.
type ErrorHandlerF func(err error, ch Channel)

func (self ErrorHandlerF) HandleError(err error, ch Channel) {
	self(err, ch)
}

// CloseHandler is notified when a channel closes.
type CloseHandler interface {
	HandleClose(ch Channel)
}

// CloseHandlerF is the function form of CloseHandler.
type CloseHandlerF func(ch Channel)

func (self CloseHandlerF) HandleClose(ch Channel) {
	self(ch)
}
