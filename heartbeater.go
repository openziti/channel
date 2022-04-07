package channel

import (
	"github.com/sirupsen/logrus"
	"sync/atomic"
	"time"
)

// HeartbeatCallback provide an interface that is notified when various heartbeat events take place
type HeartbeatCallback interface {
	HeartbeatTx(ts int64)
	HeartbeatRx(ts int64)
	HeartbeatRespTx(ts int64)
	HeartbeatRespRx(ts int64)
	CheckHeartBeat()
}

// ConfigureHeartbeat setups up heartbeats on the given channel. It assumes that an equivalent setup happens on the
// other side of the channel.
//
// When possible, heartbeats will be sent on existing traffic. When a heartbeat is due to be sent, the next message sent
// will include a heartbeat header. If no message is sent by the time the checker runs on checkInterval, a standalone
// heartbeat message will be sent.
//
// Similarly, when a message with a heartbeat header is received, the next sent message will have a header set with
// the heartbeat response. If no message is sent within a few milliseconds, a standalone heartbeat response will be
// sent
func ConfigureHeartbeat(binding Binding, heartbeatInterval time.Duration, checkInterval time.Duration, cb HeartbeatCallback) {
	hb := &heartbeater{
		ch:                  binding.GetChannel(),
		heartBeatIntervalNs: heartbeatInterval.Nanoseconds(),
		callback:            cb,
		events:              make(chan heartbeatEvent, 4),
	}

	binding.AddReceiveHandler(ContentTypeHeartbeat, hb)
	binding.AddTransformHandler(hb)

	go hb.pulse(checkInterval)
}

type heartbeater struct {
	ch                   Channel
	lastHeartbeatTx      int64
	heartBeatIntervalNs  int64
	callback             HeartbeatCallback
	events               chan heartbeatEvent
	unrespondedHeartbeat int64
}

func (self *heartbeater) HandleReceive(*Message, Channel) {
	// ignore incoming heartbeat events, everything is handled by the transformer
}

func (self *heartbeater) Rx(m *Message, _ Channel) {
	if val, found := m.GetUint64Header(HeartbeatHeader); found {
		select {
		case self.events <- heartbeatRxEvent(val):
		default:
		}
	}

	if val, found := m.GetUint64Header(HeartbeatResponseHeader); found {
		select {
		case self.events <- heartbeatRespRxEvent(val):
		default:
		}
	}
}

func (self *heartbeater) Tx(m *Message, _ Channel) {
	now := time.Now().UnixNano()
	if now-self.lastHeartbeatTx > self.heartBeatIntervalNs {
		m.PutUint64Header(HeartbeatHeader, uint64(now))
		atomic.StoreInt64(&self.lastHeartbeatTx, now)
		select {
		case self.events <- heartbeatTxEvent(now):
		default:
		}
	}

	if unrespondedHeartbeat := atomic.LoadInt64(&self.unrespondedHeartbeat); unrespondedHeartbeat != 0 {
		m.PutUint64Header(HeartbeatResponseHeader, uint64(unrespondedHeartbeat))
		atomic.StoreInt64(&self.unrespondedHeartbeat, 0)
		select {
		case self.events <- heartbeatRespTxEvent(now):
		default:
		}
	}
}

func (self *heartbeater) pulse(checkInterval time.Duration) {
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for !self.ch.IsClosed() {
		select {
		case tick := <-ticker.C:
			now := tick.UnixNano()
			lastHeartbeatTx := atomic.LoadInt64(&self.lastHeartbeatTx)
			if now-lastHeartbeatTx > self.heartBeatIntervalNs {
				self.sendHeartbeat()
			}
			self.callback.CheckHeartBeat()

		case event := <-self.events:
			event.handle(self)
		}
	}
}

func (self *heartbeater) sendHeartbeat() {
	m := NewMessage(ContentTypeHeartbeat, nil) // don't need to add heartbeat
	if err := m.WithTimeout(time.Second).SendAndWaitForWire(self.ch); err != nil && !self.ch.IsClosed() {
		logrus.WithError(err).
			WithField("channelId", self.ch.Label()).
			Error("failed to send heartbeat")
	}
}

func (self *heartbeater) sendHeartbeatIfQueueFree() {
	m := NewMessage(ContentTypeHeartbeat, nil) // don't need to add heartbeat
	if err := m.WithTimeout(10 * time.Millisecond).Send(self.ch); err != nil && !self.ch.IsClosed() {
		logrus.WithError(err).
			WithField("channelId", self.ch.Label()).
			Error("failed to send heartbeat")
	}
}

type heartbeatEvent interface {
	handle(heartbeater *heartbeater)
}

type heartbeatTxEvent int64

func (h heartbeatTxEvent) handle(heartbeater *heartbeater) {
	heartbeater.callback.HeartbeatTx(int64(h))
}

type heartbeatRxEvent int64

func (h heartbeatRxEvent) handle(heartbeater *heartbeater) {
	atomic.StoreInt64(&heartbeater.unrespondedHeartbeat, int64(h))
	heartbeater.callback.HeartbeatRx(int64(h))

	// wait a few milliseconds to allowing already queued traffic to respond to the heartbeat
	time.AfterFunc(2*time.Millisecond, func() {
		select {
		case heartbeater.events <- handleUnresponded{}:
		}
	})
}

type heartbeatRespTxEvent int64

func (h heartbeatRespTxEvent) handle(heartbeater *heartbeater) {
	heartbeater.callback.HeartbeatRespTx(int64(h))
}

type heartbeatRespRxEvent int64

func (h heartbeatRespRxEvent) handle(heartbeater *heartbeater) {
	heartbeater.callback.HeartbeatRespRx(int64(h))
}

type handleUnresponded struct{}

func (h handleUnresponded) handle(heartbeater *heartbeater) {
	if unrespondedHeartbeat := atomic.LoadInt64(&heartbeater.unrespondedHeartbeat); unrespondedHeartbeat != 0 {
		heartbeater.sendHeartbeatIfQueueFree()
	}
}
