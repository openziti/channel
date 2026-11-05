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
	"sync"
	"time"
)

// UnderlayEvent describes one change to a channel's underlay set, as it was at the moment the set
// changed.
//
// Seq orders the changes. Notifications are delivered in Seq order, so a listener does not normally
// need it, but it identifies an event for logging and lets a listener that batches or drops work out
// what it missed.
//
// Count is the number of underlays after the change. It is sampled while the set is locked, so it is
// what the change actually produced rather than what a later read would report. A listener deciding
// connectivity must use it: a channel that loses its last underlay and immediately regains one
// produces Count 0 then Count 1, while reading the channel afterwards reports 1 both times and the
// outage disappears.
//
// Lifetime is how long the underlay was open, set for removals only. It is measured when the underlay
// leaves the set rather than when the listener runs, so it does not absorb notification delay.
type UnderlayEvent struct {
	Seq      uint64
	Count    int
	Lifetime time.Duration
}

// UnderlayEventListener is notified when underlays are added or removed from a channel.
//
// Callbacks run on a goroutine owned by Underlays, one at a time and in the order the changes
// happened, not on the goroutine that changed the set. A listener that blocks holds up every later
// notification for that channel, so hand off rather than work in the callback. Do not assume a
// callback has run by the time Add or Remove returns.
type UnderlayEventListener interface {
	UnderlayAdded(ch Channel, underlay Underlay, event UnderlayEvent)
	UnderlayRemoved(ch Channel, underlay Underlay, event UnderlayEvent)
}

// Underlays manages a set of underlays with listener notification on add/remove.
type Underlays struct {
	lock      sync.Mutex
	entries   []Underlay
	listeners []UnderlayEventListener
	nextSeq   uint64
	// notified closes when the most recently queued notification has finished. Each new notification
	// takes the current one as its predecessor and installs its own, so notifications form a chain
	// that runs in the order the changes were made. Nil until the first change, which is what keeps
	// the zero value usable.
	notified chan struct{}
}

// NewUnderlays creates a new empty Underlays collection. The zero value is also ready to use.
func NewUnderlays() *Underlays {
	return &Underlays{}
}

// notifyListeners delivers an event once every earlier change has been delivered. Linking to the
// predecessor happens under the caller's hold of u.lock, so the chain is in change order; the waiting
// happens on the new goroutine, so a change never blocks behind a listener.
func (u *Underlays) notifyListeners(listeners []UnderlayEventListener, deliver func(UnderlayEventListener)) {
	predecessor := u.notified
	done := make(chan struct{})
	u.notified = done

	go func() {
		defer close(done)
		if predecessor != nil {
			<-predecessor
		}
		for _, l := range listeners {
			deliver(l)
		}
	}()
}

// Add appends the underlay and notifies all listeners. Listeners run on a separate goroutine, in
// change order; see UnderlayEventListener.
func (u *Underlays) Add(ch Channel, underlay Underlay) {
	u.lock.Lock()
	u.entries = append(u.entries, underlay)
	listeners := u.listeners
	event := UnderlayEvent{Seq: u.nextSeq, Count: len(u.entries)}
	u.nextSeq++
	u.notifyListeners(listeners, func(l UnderlayEventListener) {
		l.UnderlayAdded(ch, underlay, event)
	})
	u.lock.Unlock()
}

// Remove removes the underlay and notifies all listeners if it was found. Listeners run on a separate
// goroutine, in change order; see UnderlayEventListener.
func (u *Underlays) Remove(ch Channel, underlay Underlay) bool {
	u.lock.Lock()
	removed := false
	for i, entry := range u.entries {
		if entry == underlay {
			u.entries = append(u.entries[:i], u.entries[i+1:]...)
			removed = true
			break
		}
	}
	listeners := u.listeners
	var event UnderlayEvent
	if removed {
		event = UnderlayEvent{
			Seq:      u.nextSeq,
			Count:    len(u.entries),
			Lifetime: time.Since(underlay.CreatedAt()),
		}
		u.nextSeq++
		u.notifyListeners(listeners, func(l UnderlayEventListener) {
			l.UnderlayRemoved(ch, underlay, event)
		})
	}
	u.lock.Unlock()

	return removed
}

// GetAll returns a snapshot copy of all current underlays.
func (u *Underlays) GetAll() []Underlay {
	u.lock.Lock()
	defer u.lock.Unlock()
	result := make([]Underlay, len(u.entries))
	copy(result, u.entries)
	return result
}

// First returns the first underlay, or nil if empty.
func (u *Underlays) First() Underlay {
	u.lock.Lock()
	defer u.lock.Unlock()
	if len(u.entries) > 0 {
		return u.entries[0]
	}
	return nil
}

// CountsByType returns the number of underlays for each underlay type.
func (u *Underlays) CountsByType() map[string]int {
	u.lock.Lock()
	defer u.lock.Unlock()
	result := map[string]int{}
	for _, entry := range u.entries {
		underlayType := GetUnderlayType(entry)
		result[underlayType]++
	}
	return result
}

// AddListener registers a listener to be notified on underlay add/remove events.
func (u *Underlays) AddListener(l UnderlayEventListener) {
	u.lock.Lock()
	defer u.lock.Unlock()
	u.listeners = append(u.listeners, l)
}
