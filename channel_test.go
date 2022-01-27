package channel

import (
	"fmt"
	"github.com/openziti/foundation/identity/identity"
	"github.com/openziti/foundation/transport/tcp"
	"github.com/openziti/foundation/util/concurrenz"
	"github.com/openziti/foundation/util/netz"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

var testAddress = "tcp:localhost:28433"

func TestWriteAndReply(t *testing.T) {
	server := newEchoServer()
	server.start(t)
	defer server.stop(t)

	req := require.New(t)

	options := DefaultOptions()
	options.WriteTimeout = 100 * time.Millisecond

	ch := dialServer(options, t)
	defer func() { _ = ch.Close() }()

	for i := 0; i < 10; i++ {
		msg := NewMessage(ContentTypePingType, []byte(fmt.Sprintf("hello-%v", i)))
		reply, err := msg.WithTimeout(time.Second).SendForReply(ch)
		req.NoError(err)
		req.NotNil(reply)
		req.Equal(string(msg.Body), string(reply.Body))
	}
}

func TestSendTimeout(t *testing.T) {
	server := newEchoServer()
	server.start(t)
	defer server.stop(t)

	req := require.New(t)

	options := DefaultOptions()
	options.WriteTimeout = 100 * time.Millisecond

	ch := dialServer(options, t)
	defer func() { _ = ch.Close() }()

	msg := NewMessage(ContentTypePingType, []byte(fmt.Sprintf("hello-%v", 0)))
	blockingSendContext := NewBlockingContext(msg)
	req.NoError(ch.Send(blockingSendContext))

	req.NoError(blockingSendContext.waitForBlocked(50 * time.Millisecond))

	msg = NewMessage(ContentTypePingType, []byte(fmt.Sprintf("hello-%v", 1)))
	err := msg.WithTimeout(100 * time.Millisecond).SendAndWaitForWire(ch)
	req.EqualError(err, "timeout waiting for message to be written to wire: context deadline exceeded")

	req.True(blockingSendContext.Unblock(100 * time.Millisecond))

	for i := 0; i < 10; i++ {
		msg := NewMessage(ContentTypePingType, []byte(fmt.Sprintf("hello-%v", i)))
		reply, err := msg.WithTimeout(time.Second).SendForReply(ch)
		req.NoError(err)
		req.NotNil(reply)
		req.Equal(string(msg.Body), string(reply.Body))
	}
}

func TestInQueueTimeout(t *testing.T) {
	server := newEchoServer()
	server.start(t)
	defer server.stop(t)

	req := require.New(t)

	options := DefaultOptions()
	options.WriteTimeout = 100 * time.Millisecond

	ch := dialServer(options, t)
	defer func() { _ = ch.Close() }()

	msg := NewMessage(ContentTypePingType, []byte(fmt.Sprintf("hello-%v", 0)))
	blockingSendContext := NewBlockingContext(msg)
	req.NoError(ch.Send(blockingSendContext))
	req.NoError(blockingSendContext.waitForBlocked(50 * time.Millisecond))

	msg2 := NewMessage(ContentTypePingType, []byte(fmt.Sprintf("hello-%v", 0))).WithPriority(High).(*priorityEnvelopeImpl)
	blockingSendContext2 := NewBlockingContext(msg2)
	req.NoError(ch.Send(blockingSendContext2))

	// unblock the first sender in 10ms. The second blocker and the wait for send message will go through
	// to the queue together. The blocking message has higher priority so it will go through first and
	// block. The second message should then timeout
	go func() {
		time.Sleep(50 * time.Millisecond)
		blockingSendContext.Unblock(10 * time.Millisecond)
	}()

	msg = NewMessage(ContentTypePingType, []byte(fmt.Sprintf("hello-%v", 1)))
	err := msg.WithTimeout(100 * time.Millisecond).SendAndWaitForWire(ch)
	req.EqualError(err, "timeout waiting for message to be written to wire: context deadline exceeded")
	req.True(IsTimeout(err))

	req.True(blockingSendContext2.Unblock(100 * time.Millisecond))

	for i := 0; i < 10; i++ {
		msg := NewMessage(ContentTypePingType, []byte(fmt.Sprintf("hello-%v", i)))
		reply, err := msg.WithTimeout(time.Second).SendForReply(ch)
		req.NoError(err)
		req.NotNil(reply)
		req.Equal(string(msg.Body), string(reply.Body))
	}
}

func TestReplyTimeout(t *testing.T) {
	server := newEchoServer()
	server.pingHandler = server.echoPingsWithDelay
	server.start(t)
	defer server.stop(t)

	req := require.New(t)

	options := DefaultOptions()
	options.WriteTimeout = 100 * time.Millisecond

	ch := dialServer(options, t)
	defer func() { _ = ch.Close() }()

	msg := NewMessage(ContentTypePingType, []byte("hello"))
	reply, err := msg.WithTimeout(50 * time.Millisecond).SendForReply(ch)
	req.Nil(reply)
	req.EqualError(err, "timeout waiting for message reply: context deadline exceeded")
	req.True(IsTimeout(err))

	for i := 0; i < 5; i++ {
		msg := NewMessage(ContentTypePingType, []byte(fmt.Sprintf("hello-%v", i)))
		reply, err := msg.WithTimeout(200 * time.Millisecond).SendForReply(ch)
		req.NoError(err)
		req.NotNil(reply)
		req.Equal(string(msg.Body), string(reply.Body))
	}
}

func TestWriteTimeout(t *testing.T) {
	server := newEchoServer()
	server.pingHandler = server.blockOnPing
	server.start(t)
	defer server.stop(t)

	req := require.New(t)

	options := DefaultOptions()
	options.WriteTimeout = 100 * time.Millisecond

	ch := dialServer(options, t)
	defer func() { _ = ch.Close() }()

	var stop concurrenz.AtomicBoolean
	defer stop.Set(true)

	errC := make(chan error, 1)
	go func() {
		buf := make([]byte, 8192)
		for i := range buf {
			buf[i] = byte(i)
		}
		for !stop.Get() {
			msg := NewMessage(ContentTypePingType, buf)
			err := msg.WithTimeout(time.Second).SendAndWaitForWire(ch)
			if err != nil {
				errC <- err
				return
			}
		}
	}()

	var err error
	select {
	case err = <-errC:
	case <-time.After(10 * time.Second):
	}
	req.NotNil(err)
	req.True(strings.Contains(err.Error(), "i/o timeout"))
}

func TestNoWriteTimeout(t *testing.T) {
	t.Skip("skipping long running test")
	server := newEchoServer()
	server.pingHandler = server.blockOnPing
	server.start(t)
	defer server.stop(t)

	req := require.New(t)

	options := DefaultOptions()

	ch := dialServer(options, t)
	defer func() { _ = ch.Close() }()

	var stop concurrenz.AtomicBoolean
	defer stop.Set(true)

	errC := make(chan error, 1)
	go func() {
		buf := make([]byte, 8192)
		for i := range buf {
			buf[i] = byte(i)
		}
		for !stop.Get() {
			msg := NewMessage(ContentTypePingType, buf)
			err := msg.WithTimeout(10 * time.Second).SendAndWaitForWire(ch)
			if err != nil {
				errC <- err
				return
			}
		}
	}()

	var err error
	select {
	case err = <-errC:
	case <-time.After(10 * time.Second):
	}
	req.NoError(err)
}

func TestPriorityOrdering(t *testing.T) {
	server := newEchoServer()
	server.start(t)
	defer server.stop(t)

	req := require.New(t)

	options := DefaultOptions()
	options.WriteTimeout = 100 * time.Millisecond

	ch := dialServer(options, t)
	defer func() { _ = ch.Close() }()

	msg := NewMessage(ContentTypePingType, []byte(fmt.Sprintf("hello-%v", 0)))
	blockingSendContext := NewBlockingContext(msg)
	req.NoError(ch.Send(blockingSendContext))
	req.NoError(blockingSendContext.waitForBlocked(50 * time.Millisecond))

	lowCtx := NewNotifySendable(NewMessage(ContentTypePingType, nil).WithPriority(Low).(*priorityEnvelopeImpl))
	req.NoError(ch.Send(lowCtx))

	stdCtx := NewNotifySendable(NewMessage(ContentTypePingType, nil).WithPriority(Standard).(*priorityEnvelopeImpl))
	req.NoError(ch.Send(stdCtx))

	highCtx := NewNotifySendable(NewMessage(ContentTypePingType, nil).WithPriority(High).(*priorityEnvelopeImpl))
	req.NoError(ch.Send(highCtx))

	highestCtx := NewNotifySendable(NewMessage(ContentTypePingType, nil).WithPriority(Highest).(*priorityEnvelopeImpl))
	req.NoError(ch.Send(highestCtx))

	blockingSendContext.Unblock(10 * time.Millisecond)

	highestCtx.AssertNext(t, 10*time.Millisecond)
	highCtx.AssertNext(t, 10*time.Millisecond)
	stdCtx.AssertNext(t, 10*time.Millisecond)
	lowCtx.AssertNext(t, 10*time.Millisecond)
}

func dialServer(options *Options, t *testing.T) Channel {
	req := require.New(t)
	addr, err := tcp.AddressParser{}.Parse(testAddress)
	req.NoError(err)

	clientId := &identity.TokenId{Token: "echo-client"}
	underlayFactory := NewClassicDialer(clientId, addr, nil)

	ch, err := NewChannel("echo-test", underlayFactory, options)
	req.NoError(err)

	return ch
}

func newEchoServer() *echoServer {
	options := DefaultOptions()
	options.MaxOutstandingConnects = 1
	options.MaxQueuedConnects = 1
	options.WriteTimeout = 1 * time.Second
	options.ConnectTimeoutMs = 1000

	result := &echoServer{
		options:   options,
		blockChan: make(chan struct{}),
	}
	result.pingHandler = result.echoPings
	return result
}

type echoServer struct {
	listener    UnderlayListener
	options     *Options
	pingHandler func(msg *Message, ch Channel)
	blockChan   chan struct{}
}

func (self *echoServer) start(t *testing.T) {
	id := &identity.TokenId{Token: "echo-server"}
	addr, err := tcp.AddressParser{}.Parse(testAddress)
	require.NoError(t, err)
	self.listener = NewClassicListener(id, addr, DefaultConnectOptions(), nil)
	require.NoError(t, self.listener.Listen())
	require.NoError(t, netz.WaitForPortActive("localhost:28433", time.Second*2))

	go self.accept()
}

func (self *echoServer) stop(t *testing.T) {
	if self.listener != nil {
		require.NoError(t, self.listener.Close())
		require.NoError(t, netz.WaitForPortGone("localhost:28433", time.Second*2))
	}

	select {
	case <-self.blockChan:
	default:
	}
}

func (self *echoServer) accept() {
	counter := 0

	self.options.SetBindHandlerF(func(binding Binding) error {
		binding.AddReceiveHandlerF(ContentTypePingType, self.pingHandler)
		return nil
	})

	for {
		counter++

		_, err := NewChannel(fmt.Sprintf("echo-server-%v", counter), self.listener, self.options)
		if err != nil {
			logrus.WithError(err).Error("echo listener error, exiting")
			return
		}
	}
}

func (self *echoServer) echoPings(msg *Message, ch Channel) {
	reply := NewResult(true, string(msg.Body))
	reply.ReplyTo(msg)
	if err := ch.Send(reply); err != nil {
		logrus.WithError(err).WithField("reqSeq", msg.Sequence()).Error("error responding to request")
	}
}

func (self *echoServer) echoPingsWithDelay(msg *Message, ch Channel) {
	time.Sleep(100 * time.Millisecond)
	reply := NewResult(true, string(msg.Body))
	reply.ReplyTo(msg)
	if err := ch.Send(reply); err != nil {
		logrus.WithError(err).WithField("reqSeq", msg.Sequence()).Error("error responding to request")
	}
}

func (self *echoServer) blockOnPing(*Message, Channel) {
	<-self.blockChan
}

func NewBlockingContext(wrapped Sendable) *BlockingSendable {
	return &BlockingSendable{
		Sendable:   wrapped,
		notify:     make(chan struct{}),
		isBlocking: make(chan struct{}, 1),
	}
}

type BlockingSendable struct {
	Sendable
	BaseSendListener
	notify     chan struct{}
	isBlocking chan struct{}
}

func (self *BlockingSendable) SendListener() SendListener {
	return self
}

func (self *BlockingSendable) Priority() Priority {
	return High
}

func (self *BlockingSendable) NotifyBeforeWrite() {
	fmt.Println("BlockingSendable is blocking")
	self.isBlocking <- struct{}{}
	self.notify <- struct{}{}
}

func (self *BlockingSendable) waitForBlocked(timeout time.Duration) error {
	select {
	case <-self.isBlocking:
		return nil
	case <-time.After(timeout):
		return errors.New("timed out")
	}
}

func (self *BlockingSendable) Unblock(timeout time.Duration) bool {
	select {
	case <-self.notify:
		fmt.Println("BlockingSendable is unblocked")
		return true
	case <-time.After(timeout):
		return false
	}
}

func NewNotifySendable(wrapped Sendable) *NotifySendable {
	return &NotifySendable{
		Sendable: wrapped,
		notify:   make(chan Sendable, 10),
	}
}

type NotifySendable struct {
	Sendable
	BaseSendListener
	notify chan Sendable
}

func (self *NotifySendable) SendListener() SendListener {
	return self
}

func (self *NotifySendable) NotifyBeforeWrite() {
	self.notify <- self
}

func (self *NotifySendable) AssertNext(t *testing.T, timeout time.Duration) {
	select {
	case next := <-self.notify:
		require.Equal(t, self, next)
		require.Equal(t, self.Priority(), next.Priority())
	case <-time.After(timeout):
		require.FailNow(t, "timed out")
	}
}
