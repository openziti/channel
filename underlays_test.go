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

	require.Len(t, l.added, 1)
	require.Same(t, a, l.added[0])
	require.Empty(t, l.removed)
}

func Test_Underlays_ListenerNotifiedOnRemove(t *testing.T) {
	u := NewUnderlays()
	l := &testUnderlayListener{}
	u.AddListener(l)

	a := &testUnderlay{}
	u.Add(nil, a)
	u.Remove(nil, a)

	require.Len(t, l.removed, 1)
	require.Same(t, a, l.removed[0])
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

	// Listener that reads from the Underlays during the callback.
	// This should not deadlock since notifications happen outside the lock.
	u.AddListener(&testUnderlayListenerF{
		onAdded: func(_ Channel, _ Underlay) {
			observed = u.GetAll()
		},
	})

	a := &testUnderlay{}
	u.Add(nil, a)
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

type testUnderlayListener struct {
	added   []Underlay
	removed []Underlay
}

func (l *testUnderlayListener) UnderlayAdded(_ Channel, u Underlay)   { l.added = append(l.added, u) }
func (l *testUnderlayListener) UnderlayRemoved(_ Channel, u Underlay) { l.removed = append(l.removed, u) }

type testUnderlayListenerF struct {
	onAdded   func(Channel, Underlay)
	onRemoved func(Channel, Underlay)
}

func (l *testUnderlayListenerF) UnderlayAdded(ch Channel, u Underlay) {
	if l.onAdded != nil {
		l.onAdded(ch, u)
	}
}

func (l *testUnderlayListenerF) UnderlayRemoved(ch Channel, u Underlay) {
	if l.onRemoved != nil {
		l.onRemoved(ch, u)
	}
}
