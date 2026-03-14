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

package channel_test

import (
	"fmt"
	"time"

	"github.com/openziti/channel/v5"
	"github.com/openziti/identity"
	"github.com/openziti/transport/v2/tcp"
)

func Example() {
	addr, err := tcp.AddressParser{}.Parse("tcp:localhost:6565")
	if err != nil {
		panic(err)
	}
	dialId := &identity.TokenId{Token: "echo-client"}
	underlayFactory := channel.NewClassicDialer(channel.DialerConfig{Identity: dialId, Endpoint: addr})

	ch, err := channel.NewSingleChannel("echo-test", underlayFactory, nil, nil)
	if err != nil {
		panic(err)
	}

	helloMessageType := int32(256)
	msg := channel.NewMessage(helloMessageType, []byte("hello!"))

	// Can send the message on the channel. The call will return once the message is queued
	if err := ch.Send(msg); err != nil {
		panic(err)
	}

	// Can also have the message send itself on the channel
	if err := msg.Send(ch); err != nil {
		panic(err)
	}

	// Can set a timeout before sending. If the message can't be queued before the timeout, an error will be returned
	// If the timeout expires before the message can be sent, the message won't be sent
	if err := msg.WithTimeout(time.Second).Send(ch); err != nil {
		panic(err)
	}

	// Can set a timeout before sending and wait for the message to be written to the wire. If the timeout expires
	// before the message is sent, the message won't be sent and a TimeoutError will be returned
	if err := msg.WithTimeout(time.Second).SendAndWaitForWire(ch); err != nil {
		panic(err)
	}

	// Can set a timeout before sending and waiting for a reply message. If the timeout expires before the message is
	// sent, the message won't be sent and a TimeoutError will be returned. If the timeout expires before the reply
	// arrives a TimeoutError will be returned.
	reply, err := msg.WithTimeout(time.Second).SendForReply(ch)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(reply.Body))
}
