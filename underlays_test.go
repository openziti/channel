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
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_Underlays_AddAndGetAll(t *testing.T) {
	u := NewUnderlays()
	a := &testUnderlay{headers: map[int32][]byte{TypeHeader: []byte("a")}}
	b := &testUnderlay{headers: map[int32][]byte{TypeHeader: []byte("b")}}

	u.Add(nil, a)
	u.Add(nil, b)

	all := u.GetAll()
	require.Len(t, all, 2)
	require.Same(t, a, all[0])
	require.Same(t, b, all[1])
}

func Test_Underlays_ZeroValueSupportsNotifications(t *testing.T) {
	var u Underlays
	l := &testUnderlayListener{}
	u.AddListener(l)

	a := &testUnderlay{}
	b := &testUnderlay{}
	u.Add(nil, a)
	u.Add(nil, b)

	require.Eventually(t, func() bool { return len(l.getAdded()) == 2 }, time.Second, time.Millisecond)
	require.Equal(t, []Underlay{a, b}, l.getAdded())
}

func Test_Underlays_GetAllReturnsSnapshot(t *testing.T) {
	u := NewUnderlays()
	a := &testUnderlay{}

	u.Add(nil, a)
	snapshot := u.GetAll()

	u.Add(nil, &testUnderlay{})
	require.Len(t, snapshot, 1, "snapshot should not be affected by later adds")
}

func Test_Underlays_First(t *testing.T) {
	u := NewUnderlays()
	require.Nil(t, u.First())

	a := &testUnderlay{}
	b := &testUnderlay{}
	u.Add(nil, a)
	u.Add(nil, b)

	require.Same(t, a, u.First())
}

func Test_Underlays_RemovePresent(t *testing.T) {
	u := NewUnderlays()
	a := &testUnderlay{}
	b := &testUnderlay{}
	u.Add(nil, a)
	u.Add(nil, b)

	removed := u.Remove(nil, a)
	require.True(t, removed)
	require.Len(t, u.GetAll(), 1)
	require.Same(t, b, u.First())
}

func Test_Underlays_RemoveNotPresent(t *testing.T) {
	u := NewUnderlays()
	a := &testUnderlay{}
	b := &testUnderlay{}
	u.Add(nil, a)

	removed := u.Remove(nil, b)
	require.False(t, removed)
	require.Len(t, u.GetAll(), 1)
}

func Test_Underlays_RemoveFromEmpty(t *testing.T) {
	u := NewUnderlays()
	removed := u.Remove(nil, &testUnderlay{})
	require.False(t, removed)
}

func Test_Underlays_ListenerNotifiedOnAdd(t *testing.T) {
	u := NewUnderlays()
	l := &testUnderlayListener{}
	u.AddListener(l)

	a := &testUnderlay{}
	u.Add(nil, a)

	// Notification is delivered on a goroutine owned by Underlays, so it is not done when Add returns.
	require.Eventually(t, func() bool { return len(l.getAdded()) == 1 }, time.Second, time.Millisecond)
	require.Same(t, a, l.getAdded()[0])
	require.Empty(t, l.getRemoved())
}

func Test_Underlays_ListenerNotifiedOnRemove(t *testing.T) {
	u := NewUnderlays()
	l := &testUnderlayListener{}
	u.AddListener(l)

	a := &testUnderlay{}
	u.Add(nil, a)
	u.Remove(nil, a)

	require.Eventually(t, func() bool { return len(l.getRemoved()) == 1 }, time.Second, time.Millisecond)
	require.Same(t, a, l.getRemoved()[0])
}

func Test_Underlays_ListenerNotNotifiedOnRemoveNotPresent(t *testing.T) {
	u := NewUnderlays()
	l := &testUnderlayListener{}
	u.AddListener(l)

	u.Remove(nil, &testUnderlay{})
	require.Empty(t, l.removed)
}

func Test_Underlays_ListenerCanCallGetAllWithoutDeadlock(t *testing.T) {
	u := NewUnderlays()
	var observed []Underlay

	// Listener that reads from the Underlays during the callback. This must not deadlock: the
	// notification runs on its own goroutine and holds neither the set lock nor the sequencer's.
	done := make(chan struct{})
	u.AddListener(&testUnderlayListenerF{
		onAdded: func(_ Channel, _ Underlay) {
			observed = u.GetAll()
			close(done)
		},
	})

	a := &testUnderlay{}
	u.Add(nil, a)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("listener was not notified; a callback reading back from Underlays must not deadlock")
	}

	require.Len(t, observed, 1)
	require.Same(t, a, observed[0])
}

func Test_Underlays_CountsByType(t *testing.T) {
	u := NewUnderlays()
	u.Add(nil, &testUnderlay{headers: map[int32][]byte{TypeHeader: []byte("tcp")}})
	u.Add(nil, &testUnderlay{headers: map[int32][]byte{TypeHeader: []byte("tcp")}})
	u.Add(nil, &testUnderlay{headers: map[int32][]byte{TypeHeader: []byte("ws")}})

	counts := u.CountsByType()
	require.Equal(t, 2, counts["tcp"])
	require.Equal(t, 1, counts["ws"])
}

func Test_Underlays_CountsByType_NoTypeHeaderMapsToDefault(t *testing.T) {
	u := NewUnderlays()
	u.Add(nil, &testUnderlay{headers: map[int32][]byte{}})
	u.Add(nil, &testUnderlay{}) // nil headers

	counts := u.CountsByType()
	require.Equal(t, 2, counts[DefaultUnderlayType])
}

// testUnderlayListener records what it was notified of. Notifications arrive on a goroutine owned by
// Underlays, so the slices are guarded and read back through the accessors.
type testUnderlayListener struct {
	lock    sync.Mutex
	added   []Underlay
	removed []Underlay
}

func (l *testUnderlayListener) UnderlayAdded(_ Channel, u Underlay, _ UnderlayEvent) {
	l.lock.Lock()
	defer l.lock.Unlock()
	l.added = append(l.added, u)
}

func (l *testUnderlayListener) UnderlayRemoved(_ Channel, u Underlay, _ UnderlayEvent) {
	l.lock.Lock()
	defer l.lock.Unlock()
	l.removed = append(l.removed, u)
}

func (l *testUnderlayListener) getAdded() []Underlay {
	l.lock.Lock()
	defer l.lock.Unlock()
	return append([]Underlay(nil), l.added...)
}

func (l *testUnderlayListener) getRemoved() []Underlay {
	l.lock.Lock()
	defer l.lock.Unlock()
	return append([]Underlay(nil), l.removed...)
}

type testUnderlayListenerF struct {
	onAdded   func(Channel, Underlay)
	onRemoved func(Channel, Underlay)
}

func (l *testUnderlayListenerF) UnderlayAdded(ch Channel, u Underlay, _ UnderlayEvent) {
	if l.onAdded != nil {
		l.onAdded(ch, u)
	}
}

func (l *testUnderlayListenerF) UnderlayRemoved(ch Channel, u Underlay, _ UnderlayEvent) {
	if l.onRemoved != nil {
		l.onRemoved(ch, u)
	}
}

// Test_applyConstraints_ClosesBelowMinEvenWhenApplyInProgress is the regression test for the
// ordering bug where applyConstraints took the in-progress guard before checking min-validity.
// A required-underlay loss must close the channel promptly even when another constraint
// fill / dial backoff is already running, rather than waiting for that dial to return.
func Test_applyConstraints_ClosesBelowMinEvenWhenApplyInProgress(t *testing.T) {
	impl := &channelImpl{
		log:         slog.Default(),
		constraints: map[string]UnderlayConstraint{"default": {Desired: 1, Min: 1}},
		underlays:   NewUnderlays(),
		closeNotify: make(chan struct{}),
	}
	// fallbackUnderlay backs Label()/Underlay(), used by the close-path logging.
	var u Underlay = &testUnderlay{}
	impl.fallbackUnderlay.Store(&u)

	// Simulate an applyConstraints run already in progress (e.g. a dial backing off).
	impl.applyInProgress.Store(true)

	// No underlays remain, so the channel is below the default Min of 1. The min-validity
	// close check runs before the in-progress guard, so the channel closes immediately
	// rather than waiting for the in-progress dial.
	impl.applyConstraints()

	require.True(t, impl.IsClosed(), "channel below min must close even while applyInProgress is held")
}

// Test_isMultiUnderlayCapable_MinTotalUnderlays verifies MinTotalUnderlays alone marks a
// channel multi-underlay-capable, so a channel can accept additional underlays without
// per-type constraints or a dial policy.
func Test_isMultiUnderlayCapable_MinTotalUnderlays(t *testing.T) {
	require.True(t, (&channelImpl{minTotalUnderlays: 1}).isMultiUnderlayCapable(),
		"minTotalUnderlays > 0 should be multi-underlay-capable")
	require.False(t, (&channelImpl{}).isMultiUnderlayCapable(),
		"a channel with no dial policy, constraints, or minTotalUnderlays is simple")
}

// Test_applyConstraints_ClosesBelowMinTotalWithoutConstraints verifies that a channel with
// only MinTotalUnderlays set (no per-type constraints, no dial policy) still closes when its
// total underlay count drops below the minimum. This is the close-on-empty behavior a listener
// relies on when it uses MinTotalUnderlays as its sole multi-underlay signal.
func Test_applyConstraints_ClosesBelowMinTotalWithoutConstraints(t *testing.T) {
	impl := &channelImpl{
		log:               slog.Default(),
		minTotalUnderlays: 1,
		underlays:         NewUnderlays(),
		closeNotify:       make(chan struct{}),
	}
	var u Underlay = &testUnderlay{}
	impl.fallbackUnderlay.Store(&u)

	// No underlays remain, so the total (0) is below minTotalUnderlays (1). Even with no
	// per-type constraints, applyConstraints must run the total check and close the channel.
	impl.applyConstraints()

	require.True(t, impl.IsClosed(), "channel below MinTotalUnderlays must close even with no per-type constraints")
}

// seqRecordingListener records the sequence and count of every event it is handed.
type seqRecordingListener struct {
	lock   sync.Mutex
	events []UnderlayEvent
}

func (l *seqRecordingListener) record(event UnderlayEvent) {
	l.lock.Lock()
	defer l.lock.Unlock()
	l.events = append(l.events, event)
}

func (l *seqRecordingListener) UnderlayAdded(_ Channel, _ Underlay, e UnderlayEvent)   { l.record(e) }
func (l *seqRecordingListener) UnderlayRemoved(_ Channel, _ Underlay, e UnderlayEvent) { l.record(e) }

func (l *seqRecordingListener) getEvents() []UnderlayEvent {
	l.lock.Lock()
	defer l.lock.Unlock()
	return append([]UnderlayEvent(nil), l.events...)
}

// Test_Underlays_NotificationsAreOrderedAndCountsMatch drives concurrent adds and removes, which is
// how a channel behaves when one underlay is replacing another. Listeners must see every change once,
// in the order the changes happened, with the count the change produced. Without that, a listener
// deriving state from the sequence settles on whichever notification happened to arrive last, and a
// channel that briefly lost every underlay looks as though it never did.
func Test_Underlays_NotificationsAreOrderedAndCountsMatch(t *testing.T) {
	req := require.New(t)

	u := NewUnderlays()
	l := &seqRecordingListener{}
	u.AddListener(l)

	const rounds = 100
	for i := 0; i < rounds; i++ {
		underlay := &testUnderlay{}
		u.Add(nil, underlay)

		var wg sync.WaitGroup
		wg.Add(2)
		// One underlay leaving while another arrives: the callbacks race.
		next := &testUnderlay{}
		go func() {
			defer wg.Done()
			u.Remove(nil, underlay)
		}()
		go func() {
			defer wg.Done()
			u.Add(nil, next)
		}()
		wg.Wait()
		u.Remove(nil, next)
	}

	expected := rounds * 4
	req.Eventually(func() bool { return len(l.getEvents()) == expected }, 30*time.Second, time.Millisecond,
		"expected every change to be delivered")

	events := l.getEvents()
	for i, event := range events {
		req.EqualValues(i, event.Seq, "notifications must arrive in the order the changes happened")
		req.GreaterOrEqual(event.Count, 0, "count must describe a real state of the set")
	}
}

// Test_Underlays_ZeroValueIsUsable guards the zero value, which callers may embed or declare directly
// rather than going through NewUnderlays. Sequencing notifications must not depend on construction.
func Test_Underlays_ZeroValueIsUsable(t *testing.T) {
	req := require.New(t)

	var u Underlays
	l := &seqRecordingListener{}
	u.AddListener(l)

	a := &testUnderlay{}
	req.NotPanics(func() {
		u.Add(nil, a)
		u.Remove(nil, a)
	})

	req.Eventually(func() bool { return len(l.getEvents()) == 2 }, time.Second, time.Millisecond)
	events := l.getEvents()
	req.EqualValues(0, events[0].Seq)
	req.Equal(1, events[0].Count)
	req.EqualValues(1, events[1].Seq)
	req.Equal(0, events[1].Count)
}
