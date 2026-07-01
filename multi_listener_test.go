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
	"crypto/x509"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newGroupedTestUnderlay(connId string, isFirst bool) *testUnderlay {
	headers := map[int32][]byte{
		TypeHeader: []byte("default"),
	}
	Headers(headers).PutBoolHeader(IsGroupedHeader, true)
	if isFirst {
		Headers(headers).PutBoolHeader(IsFirstGroupConnection, true)
	}
	return &testUnderlay{
		headers:      headers,
		connectionId: connId,
	}
}

func newUngroupedTestUnderlay(connId string) *testUnderlay {
	return &testUnderlay{
		headers:      map[int32][]byte{},
		connectionId: connId,
	}
}

// ackOK is a no-op hello acknowledgement for tests that don't care about ack timing.
func ackOK() error { return nil }

// helloAcceptorFunc adapts a full-signature accept function to a HelloAcceptor, for
// callers that need control over the ack (e.g. to wrap the underlay first).
type helloAcceptorFunc func(underlay Underlay, ackHello func() error)

func (f helloAcceptorFunc) AcceptUnderlay(underlay Underlay, ackHello func() error) {
	f(underlay, ackHello)
}

func Test_MultiListener_FirstGroupedCreatesChannel(t *testing.T) {
	var factoryCalled atomic.Bool

	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		factoryCalled.Store(true)
		return &testChannel{connId: underlay.ConnectionId()}, nil
	}, func(underlay Underlay) error {
		return errors.New("ungrouped not expected")
	})

	ml.AcceptUnderlay(newGroupedTestUnderlay("conn-1", true), ackOK)

	require.True(t, factoryCalled.Load())

	ml.lock.Lock()
	_, exists := ml.channels["conn-1"]
	ml.lock.Unlock()
	require.True(t, exists)
}

func Test_MultiListener_NonFirstWithNoExistingChannelClosesUnderlay(t *testing.T) {
	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		t.Fatal("factory should not be called")
		return nil, nil
	}, func(underlay Underlay) error {
		return errors.New("ungrouped not expected")
	})

	underlay := newGroupedTestUnderlay("conn-1", false)
	acked := false
	ml.AcceptUnderlay(underlay, func() error { acked = true; return nil })

	require.True(t, underlay.closed, "underlay should be closed when not first and no group exists")
	require.False(t, acked, "hello should not be acked when rejecting a non-first underlay with no group")
}

func Test_MultiListener_UngroupedDelegatesToFallback(t *testing.T) {
	var fallbackCalled atomic.Bool

	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		t.Fatal("factory should not be called for ungrouped")
		return nil, nil
	}, func(underlay Underlay) error {
		fallbackCalled.Store(true)
		return nil
	})

	ml.AcceptUnderlay(newUngroupedTestUnderlay("conn-1"), ackOK)

	require.True(t, fallbackCalled.Load())
}

func Test_MultiListener_UngroupedFallbackErrorClosesUnderlay(t *testing.T) {
	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		return nil, nil
	}, func(underlay Underlay) error {
		return errors.New("fallback failed")
	})

	underlay := newUngroupedTestUnderlay("conn-1")
	ml.AcceptUnderlay(underlay, ackOK)

	require.True(t, underlay.closed, "underlay should be closed when fallback errors")
}

func Test_MultiListener_FactoryNilNilReturnsError(t *testing.T) {
	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		return nil, nil
	}, func(underlay Underlay) error {
		return nil
	})

	underlay := newGroupedTestUnderlay("conn-1", true)
	ml.AcceptUnderlay(underlay, ackOK)

	require.True(t, underlay.closed, "underlay should be closed when factory returns nil/nil")

	ml.lock.Lock()
	_, exists := ml.channels["conn-1"]
	ml.lock.Unlock()
	require.False(t, exists, "channel should not be stored when factory returns nil/nil")
}

func Test_MultiListener_SecondUnderlayRoutedToExistingChannel(t *testing.T) {
	ch := &testChannel{connId: "conn-1"}

	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		return ch, nil
	}, func(underlay Underlay) error {
		return nil
	})

	ml.AcceptUnderlay(newGroupedTestUnderlay("conn-1", true), ackOK)

	ml.AcceptUnderlay(newGroupedTestUnderlay("conn-1", false), ackOK)

	require.Equal(t, 1, ch.acceptCount, "second underlay should be accepted by existing channel")
}

func Test_MultiListener_ConcurrentFirstUnderlaySameId(t *testing.T) {
	var factoryCallCount atomic.Int32
	factoryGate := make(chan struct{})

	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		factoryCallCount.Add(1)
		<-factoryGate
		return &testChannel{connId: underlay.ConnectionId()}, nil
	}, func(underlay Underlay) error {
		return nil
	})

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		ml.AcceptUnderlay(newGroupedTestUnderlay("conn-1", true), ackOK)
	}()

	go func() {
		defer wg.Done()
		ml.AcceptUnderlay(newGroupedTestUnderlay("conn-1", true), ackOK)
	}()

	// Wait for factory to be called once, then release
	go func() {
		for factoryCallCount.Load() < 1 {
			time.Sleep(time.Millisecond)
		}
		close(factoryGate)
	}()

	wg.Wait()

	require.Equal(t, int32(1), factoryCallCount.Load(),
		"factory should only be called once for concurrent first underlays with same ID")
}

func Test_MultiListener_CloseChannelRemovesFromMap(t *testing.T) {
	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		return &testChannel{connId: underlay.ConnectionId()}, nil
	}, func(underlay Underlay) error {
		return nil
	})

	ml.AcceptUnderlay(newGroupedTestUnderlay("conn-1", true), ackOK)

	ml.lock.Lock()
	_, exists := ml.channels["conn-1"]
	ml.lock.Unlock()
	require.True(t, exists)

	ml.CloseChannel("conn-1")

	ml.lock.Lock()
	_, exists = ml.channels["conn-1"]
	ml.lock.Unlock()
	require.False(t, exists)
}

// Test_MultiListener_CloseCallbackIgnoresStaleChannel verifies the identity check:
// a close callback from an old channel must not evict a newer channel that has
// reconnected under the same id.
func Test_MultiListener_CloseCallbackIgnoresStaleChannel(t *testing.T) {
	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		return &testChannel{connId: underlay.ConnectionId()}, nil
	}, func(underlay Underlay) error {
		return nil
	})

	ml.AcceptUnderlay(newGroupedTestUnderlay("conn-1", true), ackOK)
	ml.lock.Lock()
	current := ml.channels["conn-1"]
	ml.lock.Unlock()
	require.NotNil(t, current)

	// A stale callback for a different registration under the same id must be a no-op.
	ml.closeRegistration("conn-1", &registration{ch: &testChannel{connId: "conn-1"}})

	ml.lock.Lock()
	stillThere := ml.channels["conn-1"]
	ml.lock.Unlock()
	require.Same(t, current, stillThere, "current channel must not be evicted by a stale callback")
}

func Test_MultiListener_CloseCallbackSupportsNonComparableChannel(t *testing.T) {
	var closeCallback func()
	ml := NewMultiListener(func(underlay Underlay, callback func()) (Channel, error) {
		closeCallback = callback
		return nonComparableTestChannel{
			testChannel: &testChannel{connId: underlay.ConnectionId()},
			data:        []byte("not comparable"),
		}, nil
	}, func(underlay Underlay) error {
		return errors.New("ungrouped not expected")
	})

	ml.AcceptUnderlay(newGroupedTestUnderlay("conn-1", true), ackOK)
	require.NotNil(t, closeCallback)
	require.NotPanics(t, closeCallback)

	ml.lock.Lock()
	_, exists := ml.channels["conn-1"]
	ml.lock.Unlock()
	require.False(t, exists, "close callback should remove a non-comparable channel")
}

func Test_MultiListener_IsClosedDoesNotDeadlockCloseCallback(t *testing.T) {
	closeHasLock := make(chan struct{})
	continueClose := make(chan struct{})
	probeStarted := make(chan struct{})

	var ch *mutexCloseTestChannel
	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		ch = &mutexCloseTestChannel{
			testChannel:   &testChannel{connId: underlay.ConnectionId()},
			closeCallback: closeCallback,
			closeHasLock:  closeHasLock,
			continueClose: continueClose,
			probeStarted:  probeStarted,
		}
		return ch, nil
	}, func(underlay Underlay) error {
		return errors.New("ungrouped not expected")
	})

	ml.AcceptUnderlay(newGroupedTestUnderlay("conn-1", true), ackOK)
	ch.blockProbe.Store(true)

	closeDone := make(chan struct{})
	go func() {
		defer close(closeDone)
		_ = ch.Close()
	}()
	<-closeHasLock

	lookupDone := make(chan struct{})
	go func() {
		defer close(lookupDone)
		ml.AcceptUnderlay(newGroupedTestUnderlay("conn-1", false), ackOK)
	}()
	<-probeStarted

	// Close calls its callback while holding the mutex needed by IsClosed. The callback
	// can only finish if the concurrent lookup did not retain the listener lock.
	close(continueClose)
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("channel close deadlocked against listener lookup")
	}
	select {
	case <-lookupDone:
	case <-time.After(time.Second):
		t.Fatal("listener lookup did not finish after channel close")
	}
}

// Test_MultiListener_ChannelClosedDuringCreationNotRegistered verifies the bug fix:
// if a channel closes during creation (its close callback ran before it could be
// registered), the listener must not register the dead channel, and a subsequent
// reconnect for the same id must succeed with a fresh channel rather than being
// rejected indefinitely.
func Test_MultiListener_ChannelClosedDuringCreationNotRegistered(t *testing.T) {
	returnClosed := true
	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		ch := &testChannel{connId: underlay.ConnectionId(), closed: returnClosed}
		if ch.closed {
			// Mirror the real channel: its close callback runs when it closes, before
			// the listener gets a chance to register it.
			closeCallback()
		}
		return ch, nil
	}, func(underlay Underlay) error {
		return errors.New("ungrouped not expected")
	})

	// First underlay's channel closes during creation: it must not be registered.
	ml.AcceptUnderlay(newGroupedTestUnderlay("conn-1", true), ackOK)
	ml.lock.Lock()
	_, exists := ml.channels["conn-1"]
	ml.lock.Unlock()
	require.False(t, exists, "a channel that closed during creation must not be registered")

	// A subsequent reconnect (fresh, open channel) must succeed, not be rejected.
	returnClosed = false
	ml.AcceptUnderlay(newGroupedTestUnderlay("conn-1", true), ackOK)
	ml.lock.Lock()
	reg, exists := ml.channels["conn-1"]
	ml.lock.Unlock()
	require.True(t, exists, "reconnect after a close-during-creation must register a fresh channel")
	require.False(t, reg.ch.IsClosed())
}

// Test_MultiListener_EvictsClosedChannelOnLookup verifies that if a closed channel is
// somehow still registered, a new underlay for that id evicts it and creates a fresh
// channel instead of being rejected.
func Test_MultiListener_EvictsClosedChannelOnLookup(t *testing.T) {
	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		return &testChannel{connId: underlay.ConnectionId()}, nil
	}, func(underlay Underlay) error {
		return errors.New("ungrouped not expected")
	})

	// Plant a stale closed channel directly in the map.
	stale := &testChannel{connId: "conn-1", closed: true}
	ml.lock.Lock()
	ml.channels["conn-1"] = &registration{ch: stale}
	ml.lock.Unlock()

	ml.AcceptUnderlay(newGroupedTestUnderlay("conn-1", true), ackOK)

	ml.lock.Lock()
	reg := ml.channels["conn-1"]
	ml.lock.Unlock()
	require.NotNil(t, reg)
	require.NotSame(t, Channel(stale), reg.ch, "stale closed channel should have been replaced")
	require.False(t, reg.ch.IsClosed())
}

// Test_MultiListener_GroupReservedBeforeAck verifies the core ordering guarantee: when
// a grouped first connection's hello is acknowledged, the group is already registered
// (a channel or a create-in-progress notifier). The ack is what releases the dialer to
// dial subsequent underlays, so reserving before it ensures those underlays can never
// reach the listener before the group is known.
func Test_MultiListener_GroupReservedBeforeAck(t *testing.T) {
	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		return &testChannel{connId: underlay.ConnectionId()}, nil
	}, func(underlay Underlay) error {
		return errors.New("ungrouped not expected")
	})

	reservedAtAck := false
	ml.AcceptUnderlay(newGroupedTestUnderlay("conn-1", true), func() error {
		ml.lock.Lock()
		_, hasChannel := ml.channels["conn-1"]
		_, hasNotifier := ml.createNotifiers["conn-1"]
		ml.lock.Unlock()
		reservedAtAck = hasChannel || hasNotifier
		return nil
	})

	require.True(t, reservedAtAck, "group must be reserved before the hello is acknowledged")
}

// Test_MultiListener_AckFailureReleasesReservation verifies that if the hello ack fails
// for a first connection, the reservation is released (so future dials on that id aren't
// stranded) and no channel is created.
func Test_MultiListener_AckFailureReleasesReservation(t *testing.T) {
	factoryCalled := false
	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		factoryCalled = true
		return &testChannel{connId: underlay.ConnectionId()}, nil
	}, func(underlay Underlay) error {
		return errors.New("ungrouped not expected")
	})

	underlay := newGroupedTestUnderlay("conn-1", true)
	ml.AcceptUnderlay(underlay, func() error { return errors.New("ack failed") })

	require.True(t, underlay.closed, "underlay should be closed when the ack fails")
	require.False(t, factoryCalled, "channel should not be created when the ack fails")

	ml.lock.Lock()
	_, hasChannel := ml.channels["conn-1"]
	_, hasNotifier := ml.createNotifiers["conn-1"]
	ml.lock.Unlock()
	require.False(t, hasChannel, "no channel should be registered after an ack failure")
	require.False(t, hasNotifier, "the create-notifier should be released after an ack failure")
}

// Test_MultiListener_NonFirstAttachesWhileFirstInFlight verifies the reconnect-race fix:
// a subsequent underlay that arrives while its group's first connection is still being
// created waits for the group and attaches, rather than being rejected.
func Test_MultiListener_NonFirstAttachesWhileFirstInFlight(t *testing.T) {
	var ch *testChannel
	var factoryCallCount atomic.Int32
	factoryGate := make(chan struct{})

	ml := NewMultiListener(func(underlay Underlay, closeCallback func()) (Channel, error) {
		factoryCallCount.Add(1)
		<-factoryGate
		ch = &testChannel{connId: underlay.ConnectionId()}
		return ch, nil
	}, func(underlay Underlay) error {
		return errors.New("ungrouped not expected")
	})

	var wg sync.WaitGroup
	wg.Add(2)

	// First connection enters the (gated) factory, holding the create-notifier.
	go func() {
		defer wg.Done()
		ml.AcceptUnderlay(newGroupedTestUnderlay("conn-1", true), ackOK)
	}()
	for factoryCallCount.Load() < 1 {
		time.Sleep(time.Millisecond)
	}

	// Subsequent underlay arrives while the first is still being created.
	nonFirst := newGroupedTestUnderlay("conn-1", false)
	go func() {
		defer wg.Done()
		ml.AcceptUnderlay(nonFirst, ackOK)
	}()

	// Give the subsequent underlay time to reach the wait, then let the factory finish.
	time.Sleep(20 * time.Millisecond)
	close(factoryGate)
	wg.Wait()

	require.False(t, nonFirst.closed, "subsequent underlay should attach while the first is in flight, not be rejected")
	require.Equal(t, int32(1), factoryCallCount.Load(), "factory should be called once")
	require.Equal(t, 1, ch.acceptCount, "subsequent underlay should be accepted by the group's channel")
}

// testChannel is a minimal Channel implementation for MultiListener tests.
type testChannel struct {
	connId      string
	acceptCount int
	closed      bool
}

// nonComparableTestChannel is a valid value-backed Channel whose slice prevents
// comparison when it is stored in an interface.
type nonComparableTestChannel struct {
	*testChannel
	data []byte
}

// mutexCloseTestChannel models a Channel that protects IsClosed with the same mutex
// held while Close invokes its listener callback.
type mutexCloseTestChannel struct {
	*testChannel
	lock          sync.Mutex
	closeCallback func()
	closeHasLock  chan struct{}
	continueClose chan struct{}
	probeStarted  chan struct{}
	blockProbe    atomic.Bool
	probeOnce     sync.Once
}

func (c *mutexCloseTestChannel) Close() error {
	c.lock.Lock()
	defer c.lock.Unlock()
	close(c.closeHasLock)
	<-c.continueClose
	c.closed = true
	c.closeCallback()
	return nil
}

func (c *mutexCloseTestChannel) IsClosed() bool {
	if c.blockProbe.Load() {
		c.probeOnce.Do(func() { close(c.probeStarted) })
	}
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.closed
}

func (c *testChannel) AcceptUnderlay(Underlay) error {
	if c.closed {
		return errors.New("new underlay not accepted: channel is closed")
	}
	c.acceptCount++
	return nil
}
func (c *testChannel) Id() string                              { return c.connId }
func (c *testChannel) LogicalName() string                     { return "test" }
func (c *testChannel) SetLogicalName(string)                   {}
func (c *testChannel) ConnectionId() string                    { return c.connId }
func (c *testChannel) Certificates() []*x509.Certificate       { return nil }
func (c *testChannel) Label() string                           { return "test-ch" }
func (c *testChannel) Send(Sendable) error                     { return nil }
func (c *testChannel) TrySend(Sendable) (bool, error)          { return true, nil }
func (c *testChannel) CloseNotify() <-chan struct{}            { return nil }
func (c *testChannel) Close() error                            { return nil }
func (c *testChannel) IsClosed() bool                          { return c.closed }
func (c *testChannel) Underlay() Underlay                      { return nil }
func (c *testChannel) Headers() map[int32][]byte               { return nil }
func (c *testChannel) GetTimeSinceLastRead() time.Duration     { return 0 }
func (c *testChannel) GetUserData() any                        { return nil }
func (c *testChannel) GetSenders() Senders                     { return nil }
func (c *testChannel) GetUnderlays() []Underlay                { return nil }
func (c *testChannel) GetUnderlayCountsByType() map[string]int { return nil }
