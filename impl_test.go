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
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// blockingUnderlay is a testUnderlay whose Rx blocks until Close, so a channel's rx loop
// parks instead of spinning on the stub's immediate nil return.
type blockingUnderlay struct {
	*testUnderlay
	closeCh chan struct{}
	once    sync.Once
}

func (b *blockingUnderlay) Rx() (*Message, error) {
	<-b.closeCh
	return nil, io.EOF
}

// Close is idempotent and safe to call concurrently. The channel closes its underlay from
// both Channel.Close and the rx goroutine's deferred cleanup, so without the guard the two
// races on the embedded testUnderlay's unsynchronized closed flag.
func (b *blockingUnderlay) Close() error {
	b.once.Do(func() {
		close(b.closeCh)
		_ = b.testUnderlay.Close()
	})
	return nil
}

// TestNewSingleChannelWithoutGroupSecret verifies that a simple single-underlay channel can
// be created from an underlay that exposes no headers (e.g. a websocket underlay, whose
// Headers() returns nil). Such a channel never dials or accepts additional underlays, so it
// needs no group secret; requiring one previously panicked on the nil headers map.
func TestNewSingleChannelWithoutGroupSecret(t *testing.T) {
	underlay := &blockingUnderlay{testUnderlay: &testUnderlay{headers: nil}, closeCh: make(chan struct{})}

	var ch Channel
	require.NotPanics(t, func() {
		var err error
		ch, err = NewSingleChannelWithUnderlay("test", underlay, BindHandlerF(func(Binding) error { return nil }), nil)
		require.NoError(t, err)
	})
	require.NotNil(t, ch)
	require.Empty(t, ch.(*channelImpl).groupSecret)
	require.NoError(t, ch.Close())
}

// TestNewChannelMultiUnderlayRequiresGroupSecret verifies the other branch of the guard: a
// channel that can grow (here, one with constraints) must carry a group secret, since it
// needs one to match reconnecting or additional underlays. Constructing one without a secret
// must fail rather than silently produce a channel that can never validate new underlays.
func TestNewChannelMultiUnderlayRequiresGroupSecret(t *testing.T) {
	ctx := NewSenderContext()
	senders := &singleSenders{SenderContext: ctx, sender: NewSingleChSender(ctx, make(chan Sendable, 1))}
	msgSource := NewSimpleMessageSourceProvider(func(*CloseNotifier) (Sendable, error) { return nil, io.EOF })

	config := &Config{
		LogicalName:           "test",
		Underlay:              &testUnderlay{headers: nil},
		Binder:                MakeBinder(BindHandlerF(func(Binding) error { return nil })),
		Senders:               senders,
		MessageSourceProvider: msgSource,
		Constraints:           map[string]UnderlayConstraint{"default": {Desired: 2, Min: 1}},
	}

	ch, err := NewChannel(config)
	require.Error(t, err)
	require.Nil(t, ch)
	require.Contains(t, err.Error(), "no group secret")
}

// TestNewChannelMinTotalUnderlaysRequiresGroupSecret verifies that MinTotalUnderlays alone makes
// a channel multi-underlay-capable and so requires a group secret, matching isMultiUnderlayCapable.
// Without the secret, the channel would admit any secretless underlay, since bytes.Equal of two
// empty secrets is true.
func TestNewChannelMinTotalUnderlaysRequiresGroupSecret(t *testing.T) {
	ctx := NewSenderContext()
	senders := &singleSenders{SenderContext: ctx, sender: NewSingleChSender(ctx, make(chan Sendable, 1))}
	msgSource := NewSimpleMessageSourceProvider(func(*CloseNotifier) (Sendable, error) { return nil, io.EOF })

	config := &Config{
		LogicalName:           "test",
		Underlay:              &testUnderlay{headers: nil},
		Binder:                MakeBinder(BindHandlerF(func(Binding) error { return nil })),
		Senders:               senders,
		MessageSourceProvider: msgSource,
		MinTotalUnderlays:     2,
	}

	ch, err := NewChannel(config)
	require.Error(t, err)
	require.Nil(t, ch)
	require.Contains(t, err.Error(), "no group secret")
}

// TestSimpleChannelRejectsAdditionalUnderlays verifies that a simple channel (no
// dial policy, no constraints) refuses additional underlays via AcceptUnderlay,
// including a secretless one. Without the capability guard, a secretless simple
// channel's empty groupSecret would bytes.Equal-match another secretless underlay
// and wrongly admit it.
func TestSimpleChannelRejectsAdditionalUnderlays(t *testing.T) {
	underlay := &blockingUnderlay{testUnderlay: &testUnderlay{headers: nil}, closeCh: make(chan struct{})}
	ch, err := NewSingleChannelWithUnderlay("test", underlay, BindHandlerF(func(Binding) error { return nil }), nil)
	require.NoError(t, err)
	require.Empty(t, ch.(*channelImpl).groupSecret)
	t.Cleanup(func() { _ = ch.Close() })

	impl := ch.(*channelImpl)

	t.Run("secretless underlay rejected and closed", func(t *testing.T) {
		extra := &testUnderlay{headers: nil}
		err := impl.AcceptUnderlay(extra)
		require.Error(t, err)
		require.Contains(t, err.Error(), "does not accept additional underlays")
		require.True(t, extra.IsClosed(), "rejected underlay should be closed")
	})

	t.Run("secret-bearing underlay rejected and closed", func(t *testing.T) {
		extra := &testUnderlay{headers: map[int32][]byte{GroupSecretHeader: []byte("mysecret")}}
		err := impl.AcceptUnderlay(extra)
		require.Error(t, err)
		require.Contains(t, err.Error(), "does not accept additional underlays")
		require.True(t, extra.IsClosed(), "rejected underlay should be closed")
	})
}
