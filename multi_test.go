package channel

import (
	"github.com/openziti/identity"
	"github.com/openziti/transport/v2"
	"github.com/stretchr/testify/require"
	"io"
	"testing"
)

func Test_MultiUnderlayChannels(t *testing.T) {
	req := require.New(t)
	listenAddr, err := transport.ParseAddress("tcp:0.0.0.0:6767")
	req.NoError(err)

	multiListener := NewMultiListener(func(underlay Underlay) (MultiChannel, error) {
		multiConfig := MultiChannelConfig{
			LogicalName:     "test",
			DefaultSender:   nil,
			Options:         DefaultOptions(),
			UnderlayHandler: nil,
			BindHandler:     nil,
			Underlay:        underlay,
		}
		return NewMultiChannel(&multiConfig)
	})

	listener, err := NewClassicListenerF(&identity.TokenId{Token: "host"}, listenAddr, ListenerConfig{}, multiListener.AcceptUnderlay)
	req.NoError(err)
	defer func() { _ = listener.Close() }()
}

func NewListenerSidePriorityChannel(id string) {
	NewSingleChSender()
}

type priorityChannelBase struct {
	id             string
	prioritySender Sender
	defaultSender  Sender

	priorityMsgChan chan Sendable
	defaultMsgChan  chan Sendable
	retryMsgChan    chan Sendable
}

func (self *priorityChannelBase) GetDefaultSender() Sender {
	return self.defaultSender
}

func (self *priorityChannelBase) GetPrioritySender() Sender {
	return self.prioritySender
}

func (self *priorityChannelBase) GetNextMsgDefault(closeNotify <-chan struct{}) (Sendable, error) {
	select {
	case msg := <-self.priorityMsgChan:
		return msg, nil
	case msg := <-self.defaultMsgChan:
		return msg, nil
	case msg := <-self.retryMsgChan:
		return msg, nil
	case <-closeNotify:
		return nil, io.EOF
	}
}

func (self *priorityChannelBase) GetNextPriorityMsg(closeNotify <-chan struct{}) (Sendable, error) {
	select {
	case msg := <-self.priorityMsgChan:
		return msg, nil
	case msg := <-self.retryMsgChan:
		return msg, nil
	case <-closeNotify:
		return nil, io.EOF
	}
}

func (self *priorityChannelBase) GetMessageSource(underlay Underlay) MessageSourceF {
	if GetUnderlayType(underlay) == "priority" {
		return self.GetNextPriorityMsg
	}
	return self.GetNextMsgDefault
}

func (self *priorityChannelBase) TxFailed(underlay Underlay, sendable Sendable) {
	select {
	case self.retryMsgChan <- sendable:
	default:
	}
}

type dialPriorityChannel struct {
	priorityChannelBase

	dialer *classicDialer
	dialF  func(id string)
}

func (self *dialPriorityChannel) Start(channel MultiChannel) {
	counts := channel.GetUnderlayCountsByType()
}

func (self *dialPriorityChannel) HandleClose(channel MultiChannel, underlay Underlay) {
	underlayType := GetUnderlayType(underlay)
	if GetUnderlayType(underlay) == "edge.data" {
		_ = channel.Close()
	} else {

	}
}

func (self *priorityChannelBase) Dial(channel MultiChannel, underlayType string) {
	dialTimeout := channel.GetOptions().ConnectTimeout
	if dialTimeout == 0 {
		dialTimeout = DefaultConnectTimeout
	}

	underlay, err := self.dialer.CreateWithHeaders(dialTimeout, map[int32][]byte{
		TypeHeader:         []byte(underlayType),
		ConnectionIdHeader: []byte(channel.ConnectionId()),
	})
	if err != nil {
		return nil, err
	}

	return []Underlay{underlay}, nil
}
