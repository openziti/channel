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

package underlay

import (
	"fmt"
	"github.com/michaelquigley/pfxlog"
	"github.com/openziti/channel/v4"
)

const contentType = 99
const header = 101

func newMessage(count int) *channel.Message {
	msg := channel.NewMessage(contentType, nil)
	msg.Headers[header] = []byte(fmt.Sprintf("%d", count))
	return msg
}

type bindHandler struct{}

func (h *bindHandler) BindChannel(binding channel.Binding) error {
	binding.AddTypedReceiveHandler(&receiveHandler{})
	return nil
}

type receiveHandler struct{}

func (h *receiveHandler) ContentType() int32 {
	return int32(contentType)
}

func (h *receiveHandler) HandleReceive(m *channel.Message, ch channel.Channel) {
	pfxlog.ContextLogger(ch.Label()).Infof("header = [%s]", m.Headers[header])
}
