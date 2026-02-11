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
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_TrySend_QueueFull(t *testing.T) {
	ctx := NewSenderContext()
	msgC := make(chan Sendable, 1)
	sender := NewSingleChSender(ctx, msgC)

	// Fill the queue
	msg1 := NewMessage(1, nil)
	sent, err := sender.TrySend(msg1.ToSendable())
	require.NoError(t, err)
	require.True(t, sent)

	// Second send should fail without error
	msg2 := NewMessage(2, nil)
	sent, err = sender.TrySend(msg2.ToSendable())
	require.NoError(t, err)
	require.False(t, sent, "should return false when queue is full")
}

func Test_TrySend_ExpiredContext(t *testing.T) {
	ctx := NewSenderContext()
	msgC := make(chan Sendable, 1)
	sender := NewSingleChSender(ctx, msgC)

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	msg := NewMessage(1, nil).WithContext(cancelCtx).ToSendable()
	sent, err := sender.TrySend(msg)
	require.Error(t, err)
	require.False(t, sent)
}

func Test_Send_ExpiredContext(t *testing.T) {
	ctx := NewSenderContext()
	msgC := make(chan Sendable, 1)
	sender := NewSingleChSender(ctx, msgC)

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()

	msg := NewMessage(1, nil).WithContext(cancelCtx).ToSendable()
	err := sender.Send(msg)
	require.Error(t, err)
}

func Test_TrySend_ClosedSender(t *testing.T) {
	ctx := NewSenderContext()
	msgC := make(chan Sendable) // unbuffered, will block
	sender := NewSingleChSender(ctx, msgC)

	close(ctx.GetCloseNotify())

	msg := NewMessage(1, nil)
	sent, err := sender.TrySend(msg.ToSendable())
	require.Error(t, err)
	require.False(t, sent)
	require.IsType(t, ClosedError{}, err)
}

func Test_Send_ClosedSender(t *testing.T) {
	ctx := NewSenderContext()
	msgC := make(chan Sendable) // unbuffered
	sender := NewSingleChSender(ctx, msgC)

	close(ctx.GetCloseNotify())

	msg := NewMessage(1, nil)
	err := sender.Send(msg.ToSendable())
	require.Error(t, err)
	require.IsType(t, ClosedError{}, err)
}

func Test_TrySend_AssignsSequence(t *testing.T) {
	ctx := NewSenderContext()
	msgC := make(chan Sendable, 2)
	sender := NewSingleChSender(ctx, msgC)

	msg1 := NewMessage(1, nil).ToSendable()
	msg2 := NewMessage(2, nil).ToSendable()

	_, err := sender.TrySend(msg1)
	require.NoError(t, err)
	_, err = sender.TrySend(msg2)
	require.NoError(t, err)

	require.NotEqual(t, msg1.Sequence(), msg2.Sequence(), "each message should get a unique sequence")
}
