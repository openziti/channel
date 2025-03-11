package channel

import (
	"io"
	"time"
)

type EdgeChannel struct {
	id         string
	ctrlSender Sender
	dataSender Sender
	ctrlC      chan Sendable
	retryC     chan Sendable
	dataC      chan Sendable

	ch          *multiChannelImpl
	dialer      *classicDialer
	dialTimeout time.Duration
}

type EdgeUnderlayHandler struct {
	ctrlMsgChan  <-chan Sendable
	dataMsgChan  <-chan Sendable
	retryMsgChan chan Sendable
	dialer       *classicDialer
}

func (self *EdgeUnderlayHandler) Start(channel MultiChannel) {
	counts := channel.GetUnderlayCountsByType()
}

func (self *EdgeUnderlayHandler) GetMessageSource(underlay Underlay) MessageSourceF {
	if GetUnderlayType(underlay) == "edge.data" {
		return self.GetNextMsgDefault
	}
	return self.GetNextCtrlMsg
}

func (self *EdgeUnderlayHandler) TxFailed(underlay Underlay, sendable Sendable) {
	msg := sendable.Msg()
	if msg != nil && msg.ContentType != 10 {
		select {
		case self.retryMsgChan <- sendable:
		default:
		}
	}
}

func (self *EdgeUnderlayHandler) HandleClose(channel MultiChannel, underlay Underlay) {
	underlayType := GetUnderlayType(underlay)
	if GetUnderlayType(underlay) == "edge.data" {
		_ = channel.Close()
	} else {

	}
}

func (self *EdgeUnderlayHandler) Init(channel MultiChannel) ([]Underlay, error) {
	dialTimeout := channel.GetOptions().ConnectTimeout
	if dialTimeout == 0 {
		dialTimeout = DefaultConnectTimeout
	}

	underlay, err := self.dialer.CreateWithHeaders(dialTimeout, map[int32][]byte{
		TypeHeader:         []byte("edge.data"),
		ConnectionIdHeader: []byte(channel.ConnectionId()),
	})
	if err != nil {
		return nil, err
	}

	return []Underlay{underlay}, nil
}

func (self *EdgeUnderlayHandler) GetNextMsgDefault(closeNotify <-chan struct{}) (Sendable, error) {
	select {
	case msg := <-self.ctrlMsgChan:
		return msg, nil
	case msg := <-self.dataMsgChan:
		return msg, nil
	case msg := <-self.retryMsgChan:
		return msg, nil
	case <-closeNotify:
		return nil, io.EOF
	}
}

func (self *EdgeUnderlayHandler) GetNextCtrlMsg(closeNotify <-chan struct{}) (Sendable, error) {
	select {
	case msg := <-self.ctrlMsgChan:
		return msg, nil
	case msg := <-self.retryMsgChan:
		return msg, nil
	case <-closeNotify:
		return nil, io.EOF
	}
}
