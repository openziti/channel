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

package channel

import (
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// Heartbeat timing defaults.
const (
	DefaultHeartbeatSendInterval  = 10 * time.Second
	DefaultHeartbeatCheckInterval = time.Second
	DefaultHeartbeatTimeout       = 30 * time.Second
)

// HeartbeatOptions configures heartbeat send interval, check interval, and unresponsive timeout.
type HeartbeatOptions struct {
	SendInterval             time.Duration `json:"sendInterval"`
	CheckInterval            time.Duration `json:"checkInterval"`
	CloseUnresponsiveTimeout time.Duration `json:"closeUnresponsiveTimeout"`
	src                      map[interface{}]interface{}
}

// GetDuration parses a named duration value from the source configuration map.
func (self *HeartbeatOptions) GetDuration(name string) (*time.Duration, error) {
	if value, found := self.src[name]; found {
		if strVal, ok := value.(string); ok {
			if d, err := time.ParseDuration(strVal); err == nil {
				return &d, nil
			} else {
				return nil, errors.Wrapf(err, "invalid value for %v: %v", name, value)
			}
		} else {
			return nil, errors.Errorf("invalid (non-string) value for %v: %v", name, value)
		}
	}
	return nil, nil
}

// DefaultHeartbeatOptions returns HeartbeatOptions with sensible defaults.
func DefaultHeartbeatOptions() *HeartbeatOptions {
	return &HeartbeatOptions{
		SendInterval:             DefaultHeartbeatSendInterval,
		CheckInterval:            DefaultHeartbeatCheckInterval,
		CloseUnresponsiveTimeout: DefaultHeartbeatTimeout,
	}
}

// LoadHeartbeatOptions parses HeartbeatOptions from a configuration map.
func LoadHeartbeatOptions(data map[interface{}]interface{}) (*HeartbeatOptions, error) {
	options := DefaultHeartbeatOptions()
	options.src = data

	if value, err := options.GetDuration("sendInterval"); err != nil {
		return nil, err
	} else if value != nil {
		options.SendInterval = *value
	}

	if value, err := options.GetDuration("checkInterval"); err != nil {
		return nil, err
	} else if value != nil {
		options.CheckInterval = *value
	}

	if value, err := options.GetDuration("closeUnresponsiveTimeout"); err != nil {
		return nil, err
	} else if value != nil {
		options.CloseUnresponsiveTimeout = *value
	}

	return options, nil
}

// HeartbeatCallback provide an interface that is notified when various heartbeat events take place
type HeartbeatCallback interface {
	HeartbeatTx(ts int64)
	HeartbeatRx(ts int64)
	HeartbeatRespTx(ts int64)
	HeartbeatRespRx(ts int64)
	CheckHeartBeat()
}

// HeartbeatControl retunes the heartbeat timing of a running channel. It is
// returned by ConfigureHeartbeat so a caller can change the send and check
// intervals without rebuilding the channel, for example when applying updated
// configuration to an established link. UpdateIntervals is meant to be called
// from a single goroutine per channel.
type HeartbeatControl interface {
	// UpdateIntervals changes the heartbeat send and check intervals. The send
	// interval takes effect on the next outbound message; the check interval on
	// the next pulse of the heartbeat loop.
	UpdateIntervals(sendInterval, checkInterval time.Duration)
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
//
// The returned HeartbeatControl can be used to retune the send and check
// intervals later without rebuilding the channel.
func ConfigureHeartbeat(binding Binding, heartbeatInterval time.Duration, checkInterval time.Duration, cb HeartbeatCallback) HeartbeatControl {
	hb := &heartbeater{
		ch:        binding.GetChannel(),
		callback:  cb,
		events:    make(chan heartbeatEvent, 4),
		reconfigC: make(chan time.Duration, 1),
	}
	hb.heartBeatIntervalNs.Store(heartbeatInterval.Nanoseconds())

	binding.AddReceiveHandler(ContentTypeHeartbeat, hb)
	binding.AddTransformHandler(hb)

	go hb.pulse(checkInterval)

	return hb
}

// UpdateIntervals implements HeartbeatControl.
func (self *heartbeater) UpdateIntervals(sendInterval, checkInterval time.Duration) {
	self.heartBeatIntervalNs.Store(sendInterval.Nanoseconds())

	// The pulse goroutine owns the ticker, so hand the new check interval to it
	// rather than touching the ticker here. Drain any value pulse hasn't
	// consumed so the latest request wins, and keep the send non-blocking so
	// callers never stall on a busy pulse loop.
	select {
	case <-self.reconfigC:
	default:
	}
	select {
	case self.reconfigC <- checkInterval:
	default:
	}
}

type heartbeater struct {
	ch                   Channel
	lastHeartbeatTx      atomic.Int64
	heartBeatIntervalNs  atomic.Int64
	unrespondedHeartbeat atomic.Int64
	callback             HeartbeatCallback
	events               chan heartbeatEvent
	reconfigC            chan time.Duration
}

func (self *heartbeater) HandleReceive(*Message, Channel) {
	// ignore incoming heartbeat events, everything is handled by the transformer
}

func (self *heartbeater) queueEvent(event heartbeatEvent) {
	select {
	case self.events <- event:
	default:
	}
}

func (self *heartbeater) Rx(m *Message, _ Channel) {
	if val, found := m.GetUint64Header(HeartbeatHeader); found {
		self.queueEvent(heartbeatRxEvent(val))
	}

	if val, found := m.GetUint64Header(HeartbeatResponseHeader); found {
		self.queueEvent(heartbeatRespRxEvent(val))
	}
}

func (self *heartbeater) Tx(m *Message, _ Channel) {
	if m.ContentType == ContentTypeRaw {
		return
	}
	now := time.Now().UnixNano()
	if now-self.lastHeartbeatTx.Load() > self.heartBeatIntervalNs.Load() {
		m.PutUint64Header(HeartbeatHeader, uint64(now))
		self.lastHeartbeatTx.Store(now)
		self.queueEvent(heartbeatTxEvent(now))
	}

	if unrespondedHeartbeat := self.unrespondedHeartbeat.Load(); unrespondedHeartbeat != 0 {
		m.PutUint64Header(HeartbeatResponseHeader, uint64(unrespondedHeartbeat))
		self.unrespondedHeartbeat.Store(0)
		self.queueEvent(heartbeatRespTxEvent(now))
	}
}

func (self *heartbeater) pulse(checkInterval time.Duration) {
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for !self.ch.IsClosed() {
		select {
		case tick := <-ticker.C:
			now := tick.UnixNano()
			lastHeartbeatTx := self.lastHeartbeatTx.Load()
			if now-lastHeartbeatTx > self.heartBeatIntervalNs.Load() {
				self.sendHeartbeat()
			}
			self.callback.CheckHeartBeat()

		case event := <-self.events:
			event.handle(self)

		case newCheckInterval := <-self.reconfigC:
			// time.Ticker.Reset panics on a non-positive duration, so ignore
			// invalid requests and keep the current interval.
			if newCheckInterval > 0 {
				ticker.Reset(newCheckInterval)
			}
		}
	}
}

func (self *heartbeater) sendHeartbeat() {
	m := NewMessage(ContentTypeHeartbeat, nil) // don't need to add heartbeat
	if err := m.WithTimeout(time.Second).SendAndWaitForWire(self.ch); err != nil && !self.ch.IsClosed() {
		logrus.WithError(err).
			WithField("channelId", self.ch.Label()).
			Error("pulse failed to send heartbeat")
	}
}

func (self *heartbeater) sendHeartbeatIfQueueFree() {
	m := NewMessage(ContentTypeHeartbeat, nil) // don't need to add heartbeat
	if err := m.WithTimeout(10 * time.Millisecond).Send(self.ch); err != nil && !self.ch.IsClosed() {
		logrus.WithError(err).
			WithField("channelId", self.ch.Label()).
			Error("handleUnresponded failed to send heartbeat")
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
	heartbeater.unrespondedHeartbeat.Store(int64(h))
	heartbeater.callback.HeartbeatRx(int64(h))

	// wait a few milliseconds to allowing already queued traffic to respond to the heartbeat
	time.AfterFunc(2*time.Millisecond, func() {
		select {
		case heartbeater.events <- handleUnresponded{}:
		default:
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
	if unrespondedHeartbeat := heartbeater.unrespondedHeartbeat.Load(); unrespondedHeartbeat != 0 {
		heartbeater.sendHeartbeatIfQueueFree()
	}
}
