package channel

import (
	"fmt"
	"github.com/michaelquigley/pfxlog"
	"github.com/openziti/identity"
	"github.com/stretchr/testify/require"
	"net"
	"testing"
	"time"
)

func TestExistingConnWriteAndReply(t *testing.T) {
	testAddr := "127.0.0.1:28433"
	req := require.New(t)

	l, err := net.Listen("tcp", testAddr)
	req.NoError(err)

	defer func() { req.NoError(l.Close()) }()

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			bindHandler := BindHandlerF(func(binding Binding) error {
				binding.AddReceiveHandlerF(ContentTypePingType, func(m *Message, ch Channel) {
					reply := NewResult(true, string(m.Body))
					reply.ReplyTo(m)
					if err := reply.WithTimeout(time.Second).SendAndWaitForWire(ch); err != nil {
						pfxlog.Logger().WithError(err).Error("unable to send reply")
					}
				})
				return nil
			})
			chListener := NewExistingConnListener(&identity.TokenId{Token: "listener"}, c, nil)
			_, err = NewChannel("existing.server", chListener, bindHandler, nil)
			req.NoError(err)
		}
	}()

	options := DefaultOptions()
	options.ConnectTimeout = time.Second
	options.WriteTimeout = 100 * time.Millisecond

	conn, err := net.Dial("tcp", testAddr)
	req.NoError(err)

	dialer := NewExistingConnDialer(&identity.TokenId{Token: "dialer"}, conn, nil)
	ch, err := NewChannel("existing.client", dialer, nil, options)
	req.NoError(err)

	defer func() { req.NoError(ch.Close()) }()

	for i := 0; i < 10; i++ {
		msg := NewMessage(ContentTypePingType, []byte(fmt.Sprintf("hello-%v", i)))
		reply, err := msg.WithTimeout(time.Second).SendForReply(ch)
		req.NoError(err)
		req.NotNil(reply)
		req.Equal(string(msg.Body), string(reply.Body))
	}
}
