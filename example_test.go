package channel_test

import (
	"fmt"
	"github.com/openziti/channel"
	"github.com/openziti/foundation/identity/identity"
	"github.com/openziti/foundation/transport/tcp"
	"time"
)

func Example() {
	addr, err := tcp.AddressParser{}.Parse("tcp:localhost:6565")
	if err != nil {
		panic(err)
	}
	dialId := &identity.TokenId{Token: "echo-client"}
	underlayFactory := channel.NewClassicDialer(dialId, addr, nil)

	ch, err := channel.NewChannel("echo-test", underlayFactory, nil, nil)
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

	// Can set a priority on the message before sending. This will only affect the order in the send queue
	if err := msg.WithPriority(channel.High).Send(ch); err != nil {
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
