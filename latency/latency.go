/*
	Copyright NetFoundry, Inc.

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

package latency

import (
	"github.com/michaelquigley/pfxlog"
	"github.com/openziti/channel"
	"github.com/openziti/foundation/channel2"
	"sync/atomic"
	"time"
)

const (
	probeTime = 128
)

// LatencyHandler responds to latency messages with Result messages.
//
type LatencyHandler struct {
	responses int32
}

func (h *LatencyHandler) ContentType() int32 {
	return channel.ContentTypeLatencyType
}

func (h *LatencyHandler) HandleReceive(msg *channel.Message, ch channel.Channel) {
	// need to send response in a separate go-routine. We get stuck sending, we'll also pause the receiving side
	// limit the number of concurrent responses
	if count := atomic.AddInt32(&h.responses, 1); count < 2 {
		go func() {
			defer atomic.AddInt32(&h.responses, -1)
			response := channel.NewResult(true, "")
			response.ReplyTo(msg)
			if err := response.WithPriority(channel.High).Send(ch); err != nil {
				pfxlog.ContextLogger(ch.Label()).WithError(err).Errorf("error sending latency response")
			}
		}()
	} else {
		atomic.AddInt32(&h.responses, -1)
	}
}

type ProbeConfig struct {
	Channel        channel.Channel
	Interval       time.Duration
	Timeout        time.Duration
	ResultHandler  func(resultNanos int64)
	TimeoutHandler func()
	ExitHandler    func()
}

func ProbeLatencyConfigurable(config *ProbeConfig) {
	ch := config.Channel
	log := pfxlog.ContextLogger(ch.Label())
	log.Debug("started")
	defer log.Debug("exited")
	defer func() {
		if config.ExitHandler != nil {
			config.ExitHandler()
		}
	}()

	for {
		time.Sleep(config.Interval)
		if ch.IsClosed() {
			return
		}

		request := channel.NewMessage(channel2.ContentTypeLatencyType, nil)
		request.PutUint64Header(probeTime, uint64(time.Now().UnixNano()))
		response, err := request.WithPriority(channel.High).WithTimeout(config.Timeout).SendForReply(config.Channel)
		if err != nil {
			log.WithError(err).Error("unexpected error sending latency probe")
			if config.Channel.IsClosed() {
				log.WithError(err).Info("latency probe channel closed, exiting")
				return
			}
			if channel.IsTimeout(err) && config.TimeoutHandler != nil {
				config.TimeoutHandler()
			}
			continue
		}

		if sentTime, ok := response.GetUint64Header(probeTime); ok {
			latency := time.Now().UnixNano() - int64(sentTime)
			if config.ResultHandler != nil {
				config.ResultHandler(latency)
			}
		} else {
			log.Error("latency response did not contain probe time")
		}
	}
}

type Handler interface {
	LatencyReported(latency time.Duration)
	ChannelClosed()
}

func AddLatencyProbe(ch channel.Channel, interval time.Duration, handler Handler) {
	probe := &latencyProbe{
		handler:  handler,
		ch:       ch,
		interval: interval,
	}
	ch.AddReceiveHandler(probe)
	go probe.run()
}

type latencyProbe struct {
	handler  Handler
	ch       channel.Channel
	interval time.Duration
}

func (self *latencyProbe) ContentType() int32 {
	return channel2.ContentTypeLatencyResponseType
}

func (self *latencyProbe) HandleReceive(m *channel.Message, _ channel.Channel) {
	if sentTime, ok := m.GetUint64Header(probeTime); ok {
		latency := time.Duration(time.Now().UnixNano() - int64(sentTime))
		self.handler.LatencyReported(latency)
	} else {
		pfxlog.Logger().Error("no send time on latency response")
	}
}

func (self *latencyProbe) run() {
	log := pfxlog.ContextLogger(self.ch.Label())
	log.Debug("started")
	defer log.Debug("exited")
	defer self.handler.ChannelClosed()

	for !self.ch.IsClosed() {
		request := channel.NewMessage(channel.ContentTypeLatencyType, nil)
		request.PutUint64Header(probeTime, uint64(time.Now().UnixNano()))
		envelope := request.WithPriority(channel.High).WithTimeout(10 * time.Second)
		if err := envelope.Send(self.ch); err != nil {
			log.WithError(err).Error("unexpected error sending latency probe")
		}
		time.Sleep(self.interval)
	}
}

func AddLatencyProbeResponder(ch channel.Channel) {
	responder := &Responder{
		ch:              ch,
		responseChannel: make(chan *channel.Message, 1),
	}
	ch.AddReceiveHandler(responder)
	go responder.responseSender()
}

// Responder responds to latency messages with LatencyResponse messages.
//
type Responder struct {
	responseChannel chan *channel.Message
	ch              channel.Channel
}

func (self *Responder) ContentType() int32 {
	return channel.ContentTypeLatencyType
}

func (self *Responder) HandleReceive(msg *channel.Message, _ channel.Channel) {
	if sentTime, found := msg.Headers[probeTime]; found {
		resp := channel.NewMessage(channel.ContentTypeLatencyResponseType, nil)
		resp.Headers[probeTime] = sentTime
		select {
		case self.responseChannel <- resp:
		default:
		}
	}
}

func (self *Responder) responseSender() {
	log := pfxlog.ContextLogger(self.ch.Label())
	log.Debug("started")
	defer log.Debug("exited")

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case response := <-self.responseChannel:
			if err := response.WithPriority(channel.High).Send(self.ch); err != nil {
				log.WithError(err).Error("error sending latency response")
				if self.ch.IsClosed() {
					return
				}
			}
		case <-ticker.C:
			if self.ch.IsClosed() {
				return
			}
		}
	}
}
