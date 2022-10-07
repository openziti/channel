package channel

import (
	"fmt"
	"github.com/stretchr/testify/require"
	"sync/atomic"
	"testing"
	"time"
)

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
					errC <- err
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

	ch := dialServer(options, t, BindHandlerF(func(binding Binding) error {
		binding.AddReceiveHandlerF(ContentTypePingType, func(m *Message, ch Channel) {})
		return nil
	}))

	defer func() { _ = ch.Close() }()

	var count uint64
	for !ch.IsClosed() && !done.Load() {
		msg := NewMessage(ContentTypePingType, []byte("hello"))
		err := msg.WithTimeout(time.Second).Send(ch)
		req.NoError(err)
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
					errC <- err
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

	req.True(hb.localCount > 8)
	req.True(hb.localCount < 12)
	req.True(hb.remoteCount > 8)
	req.True(hb.remoteCount < 12)
	req.True(hb.checkCount > 80)
	req.True(hb.remoteCount < 120)
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

	req.True(hb.localCount > 8)
	req.True(hb.localCount < 12)
	req.True(hb.remoteCount > 8)
	req.True(hb.remoteCount < 12)
}

type heartbeatTracker struct {
	id          string
	localCount  int
	remoteCount int
	checkCount  int
}

func (self *heartbeatTracker) HeartbeatTx(ts int64) {
	fmt.Printf("%v: h-> @%v\n", self.id, ts)
}

func (self *heartbeatTracker) HeartbeatRx(ts int64) {
	fmt.Printf("%v: <-h @%v\n", self.id, ts)
}

func (self *heartbeatTracker) HeartbeatRespTx(ts int64) {
	fmt.Printf("%v: r-> @%v\n", self.id, ts)
	self.remoteCount++
}

func (self *heartbeatTracker) HeartbeatRespRx(ts int64) {
	fmt.Printf("%v: <-r @%v\n", self.id, ts)
	self.localCount++
}

func (self *heartbeatTracker) CheckHeartBeat() {
	self.checkCount++
}

func (self *heartbeatTracker) HeartbeatReceived(*Message, Channel) {
	fmt.Printf("%v: received heartbeat message\n", self.id)
}
