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

package protobufs

import (
	"github.com/openziti/channel/v5"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
	"reflect"
)

// TypedMessage is a proto.Message that carries its own channel content type, so it can be
// marshalled into an Envelope without the caller supplying the type.
type TypedMessage interface {
	proto.Message
	GetContentType() int32
}

// MarshalProto marshals msg into an Envelope with the given content type, returning an error
// Envelope if marshalling fails.
func MarshalProto(contentType int32, msg proto.Message) channel.Envelope {
	b, err := proto.Marshal(msg)
	if err != nil {
		return channel.NewErrorEnvelope(errors.Wrapf(err, "failed to marshal %v", reflect.TypeOf(msg)))
	}
	return channel.NewMessage(contentType, b)
}

// MarshalTyped marshals a TypedMessage into an Envelope using the message's own content type.
func MarshalTyped(msg TypedMessage) channel.Envelope {
	return MarshalProto(msg.GetContentType(), msg)
}

// TypedResponse wraps a TypedMessage so a channel response can be unmarshalled into it with
// content-type checking, via TypedResponseImpl.Unmarshall.
func TypedResponse(message TypedMessage) TypedResponseImpl {
	return TypedResponseImpl{
		TypedMessage: message,
	}
}

// TypedResponseImpl wraps a TypedMessage to unmarshal a channel response into it, verifying the
// response's content type matches the expected type.
type TypedResponseImpl struct {
	TypedMessage
}

// Unmarshall unmarshals responseMsg into the wrapped TypedMessage. It returns err if non-nil, or
// an error if the response content type does not match the expected type.
func (self TypedResponseImpl) Unmarshall(responseMsg *channel.Message, err error) error {
	if err != nil {
		return err
	}
	if responseMsg.ContentType != self.GetContentType() {
		return errors.Errorf("unexpected response type %v. expected %v",
			responseMsg.ContentType, self.GetContentType())
	}
	if err = proto.Unmarshal(responseMsg.Body, self.TypedMessage); err != nil {
		return err
	}
	return nil
}
