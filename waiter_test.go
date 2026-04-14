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
	"time"

	"github.com/openziti/foundation/v2/info"
	"github.com/stretchr/testify/require"
)

func Test_WaiterMap_AddAndRemove(t *testing.T) {
	var wm waiterMap
	receiver := &testReplyReceiver{}

	s := &testSendable{
		msg:           newTestMsg(42),
		replyReceiver: receiver,
		ctx:           context.Background(),
	}

	wm.AddWaiter(s)
	require.Equal(t, int32(1), wm.Size())

	got := wm.RemoveWaiter(42)
	require.Same(t, receiver, got)
	require.Equal(t, int32(0), wm.Size())
}

func Test_WaiterMap_RemoveNonExistent(t *testing.T) {
	var wm waiterMap
	got := wm.RemoveWaiter(99)
	require.Nil(t, got)
}

func Test_WaiterMap_SkipsAddWhenNoReplyReceiver(t *testing.T) {
	var wm waiterMap

	s := &testSendable{
		msg: newTestMsg(1),
		ctx: context.Background(),
		// replyReceiver is nil
	}

	wm.AddWaiter(s)
	require.Equal(t, int32(0), wm.Size())
}

func Test_WaiterMap_AddUsesContextDeadlineForTTL(t *testing.T) {
	var wm waiterMap

	deadline := time.Now().Add(5 * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	s := &testSendable{
		msg:           newTestMsg(1),
		replyReceiver: &testReplyReceiver{},
		ctx:           ctx,
	}
	wm.AddWaiter(s)

	// Reap with a time before the deadline — should not remove
	wm.reapExpired(deadline.UnixMilli() - 1000)
	require.Equal(t, int32(1), wm.Size())

	// Reap with a time after the deadline — should remove
	wm.reapExpired(deadline.UnixMilli() + 1000)
	require.Equal(t, int32(0), wm.Size())
}

func Test_WaiterMap_AddUsesDefaultTTLWithoutDeadline(t *testing.T) {
	var wm waiterMap

	s := &testSendable{
		msg:           newTestMsg(1),
		replyReceiver: &testReplyReceiver{},
		ctx:           context.Background(), // no deadline
	}
	wm.AddWaiter(s)

	// Reap with current time — should not remove (default TTL is 30s)
	wm.reapExpired(info.NowInMilliseconds())
	require.Equal(t, int32(1), wm.Size())

	// Reap with time 31s from now — should remove
	wm.reapExpired(info.NowInMilliseconds() + 31_000)
	require.Equal(t, int32(0), wm.Size())
}

func Test_WaiterMap_ReapExpiredLeavesUnexpired(t *testing.T) {
	var wm waiterMap

	now := info.NowInMilliseconds()

	// Add one that's already expired and one that's still valid
	expired := &testSendable{
		msg:           newTestMsg(1),
		replyReceiver: &testReplyReceiver{},
		ctx:           context.Background(),
	}
	wm.AddWaiter(expired)

	fresh := &testSendable{
		msg:           newTestMsg(2),
		replyReceiver: &testReplyReceiver{},
		ctx:           context.Background(),
	}
	wm.AddWaiter(fresh)

	require.Equal(t, int32(2), wm.Size())

	// Reap at now+31s — removes the first (default 30s TTL), but both were added ~same time
	// so both expire together. Use a deadline on one to keep it alive.
	var wm2 waiterMap

	shortCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(100*time.Millisecond))
	defer cancel()

	wm2.AddWaiter(&testSendable{
		msg:           newTestMsg(10),
		replyReceiver: &testReplyReceiver{},
		ctx:           shortCtx,
	})

	wm2.AddWaiter(&testSendable{
		msg:           newTestMsg(20),
		replyReceiver: &testReplyReceiver{},
		ctx:           context.Background(), // 30s TTL
	})

	require.Equal(t, int32(2), wm2.Size())

	// Reap at now + 1s — should expire the short one, keep the long one
	wm2.reapExpired(now + 1_000)
	require.Equal(t, int32(1), wm2.Size())

	// The long one should still be retrievable
	require.NotNil(t, wm2.RemoveWaiter(20))
}

func newTestMsg(seq int32) *Message {
	m := NewMessage(0, nil)
	m.SetSequence(seq)
	return m
}

type testReplyReceiver struct {
	msg *Message
}

func (r *testReplyReceiver) AcceptReply(m *Message) {
	r.msg = m
}

type testSendable struct {
	msg           *Message
	replyReceiver ReplyReceiver
	ctx           context.Context
	seq           int32
}

func (s *testSendable) Msg() *Message              { return s.msg }
func (s *testSendable) SetSequence(seq int32)       { s.seq = seq }
func (s *testSendable) Sequence() int32             { return s.seq }
func (s *testSendable) Context() context.Context    { return s.ctx }
func (s *testSendable) SendListener() SendListener  { return BaseSendListener{} }
func (s *testSendable) ReplyReceiver() ReplyReceiver { return s.replyReceiver }
