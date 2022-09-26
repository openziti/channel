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

package memory

import (
	"fmt"
	"github.com/michaelquigley/pfxlog"
	"github.com/openziti/channel/v2"
	"github.com/openziti/identity"
	"github.com/openziti/transport/v2"
	"time"
)

type memoryListener struct {
	identity *identity.TokenId
	handlers []channel.ConnectionHandler
	ctx      *MemoryContext
	created  chan channel.Underlay
}

func NewMemoryListener(identity *identity.TokenId, ctx *MemoryContext) channel.UnderlayListener {
	return &memoryListener{
		identity: identity,
		ctx:      ctx,
		created:  make(chan channel.Underlay),
	}
}

func (listener *memoryListener) Listen(handlers ...channel.ConnectionHandler) error {
	go listener.listen()
	return nil
}

func (listener *memoryListener) Close() error {
	close(listener.ctx.request)
	close(listener.ctx.response)
	return nil
}

func (listener *memoryListener) Create(_ time.Duration, _ transport.Configuration) (channel.Underlay, error) {
	impl := <-listener.created
	if impl == nil {
		return nil, channel.ListenerClosedError
	}
	return impl, nil
}

func (listener *memoryListener) listen() {
	log := pfxlog.ContextLogger(fmt.Sprintf("%p", listener.ctx))
	log.Info("started")
	defer log.Info("exited")

	for request := range listener.ctx.request {
		if request != nil {
			log.Infof("connecting dialer [%s] and listener [%s]", request.hello.IdToken, listener.identity.Token)

			if connectionId, err := channel.NextConnectionId(); err == nil {
				listenerTx := make(chan *channel.Message)
				dialerTx := make(chan *channel.Message)

				dialerImpl := newMemoryImpl(dialerTx, listenerTx)
				dialerImpl.connectionId = connectionId
				listenerImpl := newMemoryImpl(listenerTx, dialerTx)
				listenerImpl.connectionId = connectionId
				listener.ctx.response <- dialerImpl
				listener.created <- listenerImpl

			} else {
				log.Errorf("unable to allocate connectionId (%s)", err)
			}
		} else {
			return
		}
	}
}
