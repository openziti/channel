package channel

import (
	"context"
	"github.com/michaelquigley/pfxlog"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"time"
)

type priorityEnvelopeImpl struct {
	msg *Message
	p   Priority
}

func (self *priorityEnvelopeImpl) Send(ch Channel) error {
	return ch.Send(self)
}

func (self *priorityEnvelopeImpl) Msg() *Message {
	return self.msg
}

func (self *priorityEnvelopeImpl) Context() context.Context {
	return self.msg.Context()
}

func (self *priorityEnvelopeImpl) SendListener() SendListener {
	return self.msg.SendListener()
}

func (self *priorityEnvelopeImpl) ReplyReceiver() ReplyReceiver {
	return nil
}

func (self *priorityEnvelopeImpl) ToSendable() Sendable {
	return self
}

func (self *priorityEnvelopeImpl) Priority() Priority {
	return self.p
}

func (self *priorityEnvelopeImpl) WithPriority(p Priority) Envelope {
	self.p = p
	return self
}

func (self *priorityEnvelopeImpl) WithTimeout(duration time.Duration) TimeoutEnvelope {
	ctx, cancelF := context.WithTimeout(context.Background(), duration)
	return &envelopeImpl{
		msg:     self.msg,
		p:       self.p,
		context: ctx,
		cancelF: cancelF,
	}
}

type envelopeImpl struct {
	msg     *Message
	p       Priority
	context context.Context
	cancelF context.CancelFunc
}

func (self *envelopeImpl) Msg() *Message {
	return self.msg
}

func (self *envelopeImpl) ReplyReceiver() ReplyReceiver {
	return nil
}

func (self *envelopeImpl) ToSendable() Sendable {
	return self
}

func (self *envelopeImpl) SendListener() SendListener {
	return self
}

func (self *envelopeImpl) NotifyQueued(*Message) {}

func (self *envelopeImpl) NotifyBeforeWrite(*Message) {}

func (self *envelopeImpl) NotifyAfterWrite(*Message) {
	if self.cancelF != nil {
		self.cancelF()
	}
}

func (self *envelopeImpl) NotifyErr(*Message, error) {}

func (self *envelopeImpl) Priority() Priority {
	return self.p
}

func (self *envelopeImpl) WithPriority(p Priority) Envelope {
	self.p = p
	return self
}

func (self *envelopeImpl) Context() context.Context {
	return self.context
}

func (self *envelopeImpl) WithTimeout(duration time.Duration) TimeoutEnvelope {
	parent := self.context
	if parent == nil {
		parent = context.Background()
	}
	self.context, self.cancelF = context.WithTimeout(parent, duration)
	return self
}

func (self *envelopeImpl) Send(ch Channel) error {
	return ch.Send(self)
}

func (self *envelopeImpl) SendAndWaitForWire(ch Channel) error {
	waitSendContext := &SendWaitEnvelope{envelopeImpl: self}
	return waitSendContext.WaitForWire(ch)
}

func (self *envelopeImpl) SendForReply(ch Channel) (*Message, error) {
	replyContext := &ReplyEnvelope{envelopeImpl: self}
	return replyContext.WaitForReply(ch)
}

type SendWaitEnvelope struct {
	*envelopeImpl
	errC chan error
}

func (self *SendWaitEnvelope) ToSendable() Sendable {
	return self
}

func (self *SendWaitEnvelope) SendListener() SendListener {
	return self
}

func (self *SendWaitEnvelope) NotifyAfterWrite(m *Message) {
	close(self.errC)
}

func (self *SendWaitEnvelope) NotifyErr(_ *Message, err error) {
	self.errC <- err
}

func (self *SendWaitEnvelope) WaitForWire(ch Channel) error {
	if err := self.context.Err(); err != nil {
		return err
	}

	defer self.cancelF()

	self.errC = make(chan error, 1)

	if err := ch.Send(self); err != nil {
		return err
	}
	select {
	case err := <-self.errC:
		return err
	case <-self.context.Done():
		if err := self.context.Err(); err != nil {
			return TimeoutError{errors.Wrap(err, "timeout waiting for message to be written to wire")}
		}
		return errors.New("timeout waiting for message to be written to wire")
	}
}

type ReplyEnvelope struct {
	*envelopeImpl
	errC   chan error
	replyC chan *Message
}

func (self *ReplyEnvelope) ToSendable() Sendable {
	return self
}

func (self *ReplyEnvelope) SendListener() SendListener {
	return self
}

func (self *ReplyEnvelope) ReplyReceiver() ReplyReceiver {
	return self
}

func (self *ReplyEnvelope) NotifyAfterWrite(m *Message) {}

func (self *ReplyEnvelope) AcceptReply(message *Message) {
	select {
	case self.replyC <- message:
	default:
		logrus.
			WithField("seq", message.Sequence()).
			WithField("replyFor", message.ReplyFor()).
			WithField("contentType", message.ContentType).
			Error("could not send reply on reply channel, channel was busy")
	}
}

func (self *ReplyEnvelope) NotifyErr(_ *Message, err error) {
	self.errC <- err
}

func (self *ReplyEnvelope) WaitForReply(ch Channel) (*Message, error) {
	if err := self.context.Err(); err != nil {
		return nil, err
	}

	defer self.cancelF()

	self.errC = make(chan error, 1)
	self.replyC = make(chan *Message, 1)

	if err := ch.Send(self); err != nil {
		return nil, err
	}

	select {
	case err := <-self.errC:
		return nil, err
	case <-self.context.Done():
		if err := self.context.Err(); err != nil {
			return nil, TimeoutError{errors.Wrap(err, "timeout waiting for message reply")}
		}
		return nil, errors.New("timeout waiting for message reply")
	case reply := <-self.replyC:
		return reply, nil
	}
}

func NewErrorEnvelope(err error) Envelope {
	return &ErrorEnvelope{
		ctx: NewErrorContext(err),
	}
}

type ErrorEnvelope struct {
	ctx context.Context
}

func (self *ErrorEnvelope) Msg() *Message {
	return nil
}

func (self *ErrorEnvelope) Priority() Priority {
	return Standard
}

func (self *ErrorEnvelope) Context() context.Context {
	return self.ctx
}

func (self *ErrorEnvelope) SendListener() SendListener {
	return DefaultSendListener{}
}

func (self *ErrorEnvelope) ReplyReceiver() ReplyReceiver {
	return nil
}

func (self *ErrorEnvelope) ToSendable() Sendable {
	return self
}

func (self *ErrorEnvelope) SendAndWaitForWire(Channel) error {
	return self.ctx.Err()
}

func (self *ErrorEnvelope) SendForReply(Channel) (*Message, error) {
	return nil, self.ctx.Err()
}

func (self *ErrorEnvelope) WithTimeout(time.Duration) TimeoutEnvelope {
	return self
}

func (self *ErrorEnvelope) Send(Channel) error {
	return self.ctx.Err()
}

func (self *ErrorEnvelope) WithPriority(Priority) Envelope {
	return self
}

func NewErrorContext(err error) context.Context {
	result := &ErrorContext{
		err:     err,
		closedC: make(chan struct{}),
	}
	close(result.closedC)
	return result
}

type ErrorContext struct {
	err     error
	closedC chan struct{}
}

func (self *ErrorContext) Deadline() (deadline time.Time, ok bool) {
	return time.Time{}, false
}

func (self *ErrorContext) Done() <-chan struct{} {
	return self.closedC
}

func (self *ErrorContext) Err() error {
	return self.err
}

func (self *ErrorContext) Value(key interface{}) interface{} {
	// ignore for now. may need an implementation at some point
	pfxlog.Logger().Error("ErrorContext.Value called, but not implemented!!!")
	return nil
}
