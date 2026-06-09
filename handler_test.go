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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type recordingBinding struct {
	Binding
	handlers map[int32]ReceiveHandler
}

func (self *recordingBinding) AddReceiveHandler(contentType int32, h ReceiveHandler) {
	self.handlers[contentType] = h
}

type testContentTypeReceiver struct {
	contentType int32
	received    []*Message
}

func (self *testContentTypeReceiver) ContentType() int32 {
	return self.contentType
}

func (self *testContentTypeReceiver) HandleReceive(m *Message, _ Channel) {
	self.received = append(self.received, m)
}

func TestAddReceiveHandlers(t *testing.T) {
	req := require.New(t)

	binding := &recordingBinding{handlers: map[int32]ReceiveHandler{}}

	first := &testContentTypeReceiver{contentType: 100}
	second := &testContentTypeReceiver{contentType: 200}

	AddReceiveHandlers(binding, first, second)

	req.Len(binding.handlers, 2)

	msg := NewMessage(100, nil)
	binding.handlers[100].HandleReceive(msg, nil)
	req.Equal([]*Message{msg}, first.received)
	req.Empty(second.received)

	binding.handlers[200].HandleReceive(msg, nil)
	req.Len(second.received, 1)
}

func TestAsyncFunctionReceiveAdapter(t *testing.T) {
	req := require.New(t)

	binding := &recordingBinding{handlers: map[int32]ReceiveHandler{}}

	received := make(chan *Message, 1)
	AddReceiveHandlers(binding, &AsyncFunctionReceiveAdapter{
		Type: 100,
		Handler: func(m *Message, ch Channel) {
			received <- m
		},
	})

	req.Len(binding.handlers, 1)

	msg := NewMessage(100, nil)
	binding.handlers[100].HandleReceive(msg, nil)

	select {
	case m := <-received:
		req.Equal(msg, m)
	case <-time.After(time.Second):
		req.Fail("timed out waiting for async handler dispatch")
	}
}
