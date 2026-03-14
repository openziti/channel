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

// Binding is used to add handlers to Channel.
//
// NOTE: It is intended that the Add* methods are used at initial channel setup, and not invoked on an in-service
// Channel. The Binding should not be retained once the channel setup is complete
type Binding interface {
	AddReceiveHandler(contentType int32, h ReceiveHandler)
	AddReceiveHandlerF(contentType int32, h ReceiveHandlerF)
	AddMsgReceiveHandler(contentType int32, h MsgReceiveHandler)
	AddMsgReceiveHandlerF(contentType int32, h MsgReceiveHandlerF)
	AddPeekHandler(h PeekHandler)
	AddTransformHandler(h TransformHandler)
	AddErrorHandler(h ErrorHandler)
	AddCloseHandler(h CloseHandler)
	SetUserData(data any)
	GetUserData() any
	GetChannel() Channel
}

// BindHandler is a handler that configures a Channel via its Binding during setup.
type BindHandler interface {
	BindChannel(binding Binding) error
}

// BindHandlerF is the function form of BindHandler.
type BindHandlerF func(binding Binding) error

func (f BindHandlerF) BindChannel(binding Binding) error {
	return f(binding)
}

// BindHandlers combines multiple BindHandlers into one, running them sequentially.
// Returns the first error encountered, or nil if all succeed.
func BindHandlers(handlers ...BindHandler) BindHandler {
	if len(handlers) == 1 {
		return handlers[0]
	}

	return BindHandlerF(func(binding Binding) error {
		for _, handler := range handlers {
			if handler != nil {
				if err := handler.BindChannel(binding); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// TypedBinding extends Binding with typed senders access.
type TypedBinding[S Senders] interface {
	Binding
	GetSenders() S
	AddTypedReceiveHandler(contentType int32, h TypedReceiveHandler[S])
	AddTypedReceiveHandlerF(contentType int32, h TypedReceiveHandlerF[S])
}

// TypedBindHandler is a bind handler that receives a TypedBinding.
type TypedBindHandler[S Senders] interface {
	BindChannel(binding TypedBinding[S]) error
}

// TypedBindHandlerF is the function form of TypedBindHandler.
type TypedBindHandlerF[S Senders] func(binding TypedBinding[S]) error

func (self TypedBindHandlerF[S]) BindChannel(binding TypedBinding[S]) error {
	return self(binding)
}

// TypedBindHandlers combines multiple TypedBindHandlers into one.
func TypedBindHandlers[S Senders](handlers ...TypedBindHandler[S]) TypedBindHandler[S] {
	if len(handlers) == 1 {
		return handlers[0]
	}
	return TypedBindHandlerF[S](func(binding TypedBinding[S]) error {
		for _, handler := range handlers {
			if handler != nil {
				if err := handler.BindChannel(binding); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// bindingImpl implements TypedBinding[S] (and therefore Binding).
type bindingImpl[S Senders] struct {
	ch      *channelImpl
	senders S
}

func (self *bindingImpl[S]) GetChannel() Channel {
	return self.ch
}

func (self *bindingImpl[S]) GetSenders() S {
	return self.senders
}

func (self *bindingImpl[S]) AddReceiveHandler(contentType int32, h ReceiveHandler) {
	self.ch.receiveHandlers[contentType] = h.HandleReceive
}

func (self *bindingImpl[S]) AddReceiveHandlerF(contentType int32, h ReceiveHandlerF) {
	self.ch.receiveHandlers[contentType] = ReceiveHandlerF(h)
}

func (self *bindingImpl[S]) AddMsgReceiveHandler(contentType int32, h MsgReceiveHandler) {
	self.ch.receiveHandlers[contentType] = func(m *Message, _ Channel) {
		h.HandleReceive(m)
	}
}

func (self *bindingImpl[S]) AddMsgReceiveHandlerF(contentType int32, h MsgReceiveHandlerF) {
	self.ch.receiveHandlers[contentType] = func(m *Message, _ Channel) {
		h(m)
	}
}

func (self *bindingImpl[S]) AddTypedReceiveHandler(contentType int32, h TypedReceiveHandler[S]) {
	senders := self.senders
	self.ch.receiveHandlers[contentType] = func(m *Message, ch Channel) {
		h.HandleReceive(m, ch, senders)
	}
}

func (self *bindingImpl[S]) AddTypedReceiveHandlerF(contentType int32, h TypedReceiveHandlerF[S]) {
	senders := self.senders
	self.ch.receiveHandlers[contentType] = func(m *Message, ch Channel) {
		h(m, ch, senders)
	}
}

func (self *bindingImpl[S]) AddPeekHandler(h PeekHandler) {
	self.ch.peekHandlers = append(self.ch.peekHandlers, h)
}

func (self *bindingImpl[S]) AddTransformHandler(h TransformHandler) {
	self.ch.transformHandlers = append(self.ch.transformHandlers, h)
}

func (self *bindingImpl[S]) AddErrorHandler(h ErrorHandler) {
	self.ch.errorHandlers = append(self.ch.errorHandlers, h)
}

func (self *bindingImpl[S]) AddCloseHandler(h CloseHandler) {
	self.ch.closeHandlers = append(self.ch.closeHandlers, h)
}

func (self *bindingImpl[S]) SetUserData(data any) {
	self.ch.userData = data
}

func (self *bindingImpl[S]) GetUserData() any {
	return self.ch.userData
}

// MakeTypedBinder creates a binder function from a TypedBindHandler and senders.
func MakeTypedBinder[S Senders](senders S, handler TypedBindHandler[S]) func(*channelImpl) error {
	return func(ch *channelImpl) error {
		if handler == nil {
			return nil
		}
		binding := &bindingImpl[S]{ch: ch, senders: senders}
		return handler.BindChannel(binding)
	}
}

// MakeBinder creates a binder function from a BindHandler.
func MakeBinder(handler BindHandler) func(*channelImpl) error {
	return func(ch *channelImpl) error {
		if handler == nil {
			return nil
		}
		binding := &bindingImpl[Senders]{ch: ch, senders: ch.senders}
		return handler.BindChannel(binding)
	}
}
