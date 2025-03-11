package channel

import (
	"github.com/michaelquigley/pfxlog"
	"sync"
)

type MultiChannelFactory func(underlay Underlay) (MultiChannel, error)

type MultiListener struct {
	channels       map[string]MultiChannel
	lock           sync.Mutex
	channelFactory func(underlay Underlay) (MultiChannel, error)
}

func (self *MultiListener) AcceptUnderlay(underlay Underlay) {
	self.lock.Lock()
	defer self.lock.Unlock()
	if mc, ok := self.channels[underlay.ConnectionId()]; ok {
		mc.AcceptUnderlay(underlay)
	} else {
		mc, err := self.channelFactory(underlay)
		if err != nil {
			pfxlog.Logger().WithError(err).Errorf("failed to create multi-underlay channel")
		} else {
			self.channels[underlay.ConnectionId()] = mc
			mc.AcceptUnderlay(underlay)
		}
	}
}

func NewMultiListener(channelF MultiChannelFactory) *MultiListener {
	result := &MultiListener{
		channels:       make(map[string]MultiChannel),
		channelFactory: channelF,
	}
	return result
}
