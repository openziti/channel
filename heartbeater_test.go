package channel

import (
	"fmt"
	"github.com/michaelquigley/pfxlog"
	"github.com/stretchr/testify/require"
	"sync/atomic"
	"testing"
	"time"
)

// A simple test to check for failure of alignment on atomic operations for 64 bit variables in a struct
func Test64BitAlignment(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("One of the variables that was tested is not properly 64-bit aligned.")
		}
	}()

	hb := heartbeater{}
	chImpl := channelImpl{}

	atomic.LoadInt64(&hb.lastHeartbeatTx)
	atomic.LoadInt64(&hb.unrespondedHeartbeat)
	atomic.LoadInt64(&chImpl.lastRead)
}

func TestBaselineHeartbeat(t *testing.T) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	errC := make(chan error, 10)
	remoteDone := make(chan uint64, 1)
	var done atomic.Bool

	go func() {
		<-ticker.C
		done.Store(true)
	}()

	server := newTestServer()
	server.pingHandler = func(*Message, Channel) {}
	server.acceptHandler = func(ch Channel) {
		go func() {
			var count uint64
			defer func() {
				remoteDone <- count
			}()

			for !ch.IsClosed() && !done.Load() {
				msg := NewMessage(ContentTypePingType, []byte("hello"))
				if err := msg.WithTimeout(time.Second).Send(ch); err != nil {
					if !ch.IsClosed() {
						errC <- err
					}
					return
				}
				count++
			}
		}()
	}
	server.start(t)
	defer server.stop(t)

	req := require.New(t)

	options := DefaultOptions()
	options.WriteTimeout = 100 * time.Millisecond

	fmt.Printf("dialing server\n")
	time.Sleep(time.Second * 2)
	ch := dialServer(options, t, BindHandlerF(func(binding Binding) error {
		binding.AddReceiveHandlerF(ContentTypePingType, func(m *Message, ch Channel) {})
		return nil
	}))
	fmt.Printf("done dialing server\n")

	defer func() { _ = ch.Close() }()

	var count uint64
	for !ch.IsClosed() && !done.Load() {
		msg := NewMessage(ContentTypePingType, []byte("hello"))
		err := msg.WithTimeout(time.Second).Send(ch)
		if !ch.IsClosed() {
			req.NoError(err)
		}
		count++
	}

	var remoteCount uint64
	select {
	case remoteCount = <-remoteDone:
	case <-time.After(time.Second):
		req.FailNow("timed out waiting for remote done")
	}

	select {
	case err := <-errC:
		req.NoError(err)
	default:
	}

	fmt.Printf("count:  %v\nremote: %v\n", count, remoteCount)
}

func TestBusyHeartbeat(t *testing.T) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	errC := make(chan error, 10)
	remoteDone := make(chan uint64, 1)
	var done atomic.Bool

	go func() {
		<-ticker.C
		done.Store(true)
	}()

	server := newTestServer()
	server.bindHandler = func(binding Binding) error {
		binding.AddReceiveHandlerF(ContentTypePingType, func(m *Message, ch Channel) {})
		hb := &heartbeatTracker{id: "server"}
		ConfigureHeartbeat(binding, time.Second, 100*time.Millisecond, hb)
		binding.AddReceiveHandlerF(ContentTypeHeartbeat, hb.HeartbeatReceived)
		return nil
	}
	server.acceptHandler = func(ch Channel) {
		go func() {
			var count uint64
			defer func() {
				remoteDone <- count
			}()

			for !ch.IsClosed() && !done.Load() {
				msg := NewMessage(ContentTypePingType, []byte("hello"))
				if err := msg.WithTimeout(time.Second).Send(ch); err != nil {
					if !ch.IsClosed() {
						pfxlog.Logger().WithError(err).WithField("side", "server").Error("send error")
						errC <- err
					}
					return
				}
				count++
				time.Sleep(time.Millisecond)
			}
		}()
	}
	server.start(t)
	defer server.stop(t)

	req := require.New(t)

	options := DefaultOptions()
	options.WriteTimeout = time.Second

	hb := &heartbeatTracker{id: "client"}
	ch := dialServer(options, t, BindHandlerF(func(binding Binding) error {
		binding.AddReceiveHandlerF(ContentTypePingType, func(m *Message, ch Channel) {})
		ConfigureHeartbeat(binding, time.Second, 100*time.Millisecond, hb)
		binding.AddReceiveHandlerF(ContentTypeHeartbeat, hb.HeartbeatReceived)
		return nil
	}))
	defer func() { _ = ch.Close() }()

	var count uint64
	for !ch.IsClosed() && !done.Load() {
		msg := NewMessage(ContentTypePingType, []byte("hello"))
		err := msg.WithTimeout(time.Second).Send(ch)
		if !ch.IsClosed() {
			req.NoError(err)
		}
		time.Sleep(time.Millisecond)
		count++
	}

	var remoteCount uint64
	select {
	case remoteCount = <-remoteDone:
	case <-time.After(time.Second):
		req.FailNow("timed out waiting for remote done")
	}

	select {
	case err := <-errC:
		req.NoError(err)
	default:
	}

	fmt.Printf("count:  %v\nremote: %v\nchecks: %v\n", count, remoteCount, hb.checkCount)

	req.True(hb.respRx >= 8, "value: %v", hb.respRx)
	req.True(hb.respRx < 12, "value: %v", hb.respRx)
	req.True(hb.remoteCount >= 8, "value: %v", hb.remoteCount)
	req.True(hb.remoteCount < 12, "value: %v", hb.remoteCount)
	req.True(hb.checkCount > 80, "value: %v", hb.checkCount)
	req.True(hb.checkCount < 120, "value: %v", hb.checkCount)
}

func TestQuietHeartbeat(t *testing.T) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	errC := make(chan error, 10)
	remoteDone := make(chan uint64, 1)
	var done atomic.Bool

	go func() {
		<-ticker.C
		done.Store(true)
	}()

	server := newTestServer()
	server.bindHandler = func(binding Binding) error {
		binding.AddReceiveHandlerF(ContentTypePingType, func(m *Message, ch Channel) {})
		hb := &heartbeatTracker{id: "server"}
		ConfigureHeartbeat(binding, time.Second, 100*time.Millisecond, hb)
		binding.AddReceiveHandlerF(ContentTypeHeartbeat, hb.HeartbeatReceived)
		return nil
	}
	server.acceptHandler = func(ch Channel) {
		go func() {
			var count uint64
			defer func() {
				remoteDone <- count
			}()

			for !ch.IsClosed() && !done.Load() {
				msg := NewMessage(ContentTypePingType, []byte("hello"))
				if err := msg.WithTimeout(time.Second).Send(ch); err != nil {
					errC <- err
					return
				}
				time.Sleep(1500 * time.Millisecond)
				count++
			}
		}()
	}
	server.start(t)
	defer server.stop(t)

	req := require.New(t)

	options := DefaultOptions()
	options.WriteTimeout = 100 * time.Millisecond

	hb := &heartbeatTracker{id: "client"}
	ch := dialServer(options, t, BindHandlerF(func(binding Binding) error {
		binding.AddReceiveHandlerF(ContentTypePingType, func(m *Message, ch Channel) {})
		ConfigureHeartbeat(binding, time.Second, 100*time.Millisecond, hb)
		binding.AddReceiveHandlerF(ContentTypeHeartbeat, hb.HeartbeatReceived)
		return nil
	}))
	defer func() { _ = ch.Close() }()

	var count uint64
	for !ch.IsClosed() && !done.Load() {
		msg := NewMessage(ContentTypePingType, []byte("hello"))
		err := msg.WithTimeout(time.Second).Send(ch)
		req.NoError(err)
		count++
		time.Sleep(1500 * time.Millisecond)
	}

	var remoteCount uint64
	select {
	case remoteCount = <-remoteDone:
	case <-time.After(time.Second):
		req.FailNow("timed out waiting for remote done")
	}

	select {
	case err := <-errC:
		req.NoError(err)
	default:
	}

	fmt.Printf("count:  %v\nremote: %v\n", count, remoteCount)

	req.True(hb.respRx > 8)
	req.True(hb.respRx < 12)
	req.True(hb.remoteCount > 8)
	req.True(hb.remoteCount < 12)
}

type heartbeatTracker struct {
	id          string
	hbTx        int
	respRx      int
	remoteCount int
	checkCount  int
}

func (self *heartbeatTracker) HeartbeatTx(ts int64) {
	self.hbTx++
	fmt.Printf("%v: h-> @%v, hbTx: %v\n", self.id, ts, self.hbTx)
}

func (self *heartbeatTracker) HeartbeatRx(ts int64) {
	fmt.Printf("%v: <-h @%v\n", self.id, ts)
}

func (self *heartbeatTracker) HeartbeatRespTx(ts int64) {
	self.remoteCount++
	fmt.Printf("%v: r-> @%v, rc: %v\n", self.id, ts, self.remoteCount)
}

func (self *heartbeatTracker) HeartbeatRespRx(ts int64) {
	self.respRx++
	fmt.Printf("%v: <-r @%v, respRx: %v\n", self.id, ts, self.respRx)
}

func (self *heartbeatTracker) CheckHeartBeat() {
	self.checkCount++
}

func (self *heartbeatTracker) HeartbeatReceived(*Message, Channel) {
	fmt.Printf("%v: received heartbeat message\n", self.id)
}
