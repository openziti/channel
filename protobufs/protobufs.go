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

type TypedMessage interface {
	proto.Message
	GetContentType() int32
}

func MarshalProto(contentType int32, msg proto.Message) channel.Envelope {
	b, err := proto.Marshal(msg)
	if err != nil {
		return channel.NewErrorEnvelope(errors.Wrapf(err, "failed to marshal %v", reflect.TypeOf(msg)))
	}
	return channel.NewMessage(contentType, b)
}

func MarshalTyped(msg TypedMessage) channel.Envelope {
	return MarshalProto(msg.GetContentType(), msg)
}

func TypedResponse(message TypedMessage) TypedResponseImpl {
	return TypedResponseImpl{
		TypedMessage: message,
	}
}

type TypedResponseImpl struct {
	TypedMessage
}

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
