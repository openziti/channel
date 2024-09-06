package protobufs

import (
	"github.com/openziti/channel/v3"
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
	if responseMsg.ContentType != self.TypedMessage.GetContentType() {
		return errors.Errorf("unexpected response type %v. expected %v",
			responseMsg.ContentType, self.TypedMessage.GetContentType())
	}
	if err = proto.Unmarshal(responseMsg.Body, self.TypedMessage); err != nil {
		return err
	}
	return nil
}
