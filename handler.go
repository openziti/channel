/*
	Copyright NetFoundry, Inc.

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

// Binding is used to add handlers to Channel.
//
// NOTE: It is intended that the Add* methods are used at initial channel setup, and not invoked on an in-service
// Channel. This API may change in the future to enforce those semantics programmatically.
//
type Binding interface {
	Bind(h BindHandler) error
	AddPeekHandler(h PeekHandler)
	AddTransformHandler(h TransformHandler)
	AddReceiveHandler(contentType int32, h ReceiveHandler)
	AddReceiveHandlerF(contentType int32, h ReceiveHandlerF)
	AddTypedReceiveHandler(h TypedReceiveHandler)
	AddErrorHandler(h ErrorHandler)
	AddCloseHandler(h CloseHandler)
	SetUserData(data interface{})
	GetUserData() interface{}
	GetChannel() Channel
}

type BindHandler interface {
	BindChannel(binding Binding) error
}

type BindHandlerF func(binding Binding) error

func (f BindHandlerF) BindChannel(binding Binding) error {
	return f(binding)
}

type ConnectionHandler interface {
	HandleConnection(hello *Hello, certificates []*x509.Certificate) error
}

type PeekHandler interface {
	Connect(ch Channel, remoteAddress string)
	Rx(m *Message, ch Channel)
	Tx(m *Message, ch Channel)
	Close(ch Channel)
}

type TransformHandler interface {
	Rx(m *Message, ch Channel)
	Tx(m *Message, ch Channel)
}

type ReceiveHandler interface {
	HandleReceive(m *Message, ch Channel)
}

type TypedReceiveHandler interface {
	ContentType() int32
	ReceiveHandler
}

type ReceiveHandlerF func(m *Message, ch Channel)

func (self ReceiveHandlerF) HandleReceive(m *Message, ch Channel) {
	self(m, ch)
}

type AsyncFunctionReceiveAdapter struct {
	Type    int32
	Handler ReceiveHandlerF
}

func (adapter *AsyncFunctionReceiveAdapter) ContentType() int32 {
	return adapter.Type
}

func (adapter *AsyncFunctionReceiveAdapter) HandleReceive(m *Message, ch Channel) {
	go adapter.Handler(m, ch)
}

type ErrorHandler interface {
	HandleError(err error, ch Channel)
}

type ErrorHandlerF func(err error, ch Channel)

func (self ErrorHandlerF) HandleError(err error, ch Channel) {
	self(err, ch)
}

type CloseHandler interface {
	HandleClose(ch Channel)
}

type CloseHandlerF func(ch Channel)

func (self CloseHandlerF) HandleClose(ch Channel) {
	self(ch)
}