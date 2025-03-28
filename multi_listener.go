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
	"github.com/michaelquigley/pfxlog"
	"sync"
)

type MultiChannelFactory func(underlay Underlay, closeCallback func()) (MultiChannel, error)

type MultiListener struct {
	channels       map[string]MultiChannel
	lock           sync.Mutex
	channelFactory MultiChannelFactory
}

func (self *MultiListener) AcceptUnderlay(underlay Underlay) {
	self.lock.Lock()
	defer self.lock.Unlock()

	log := pfxlog.Logger().WithField("underlayId", underlay.ConnectionId()).
		WithField("underlayType", GetUnderlayType(underlay))

	chId := underlay.ConnectionId()

	if mc, ok := self.channels[chId]; ok {
		log.Info("found existing channel for underlay")
		mc.AcceptUnderlay(underlay)
	} else {
		log.Info("no existing channel found for underlay")
		mc, err := self.channelFactory(underlay, func() {
			self.CloseChannel(chId)
		})

		if mc != nil {
			if err != nil {
				pfxlog.Logger().WithError(err).Errorf("failed to create multi-underlay channel")
			} else {
				self.channels[chId] = mc
			}
		}
	}
}

func (self *MultiListener) CloseChannel(chId string) {
	self.lock.Lock()
	defer self.lock.Unlock()
	delete(self.channels, chId)
}

func NewMultiListener(channelF MultiChannelFactory) *MultiListener {
	result := &MultiListener{
		channels:       make(map[string]MultiChannel),
		channelFactory: channelF,
	}
	return result
}
