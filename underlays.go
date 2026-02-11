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

import "sync"

// UnderlayEventListener is notified when underlays are added or removed from a channel.
type UnderlayEventListener interface {
	UnderlayAdded(ch Channel, underlay Underlay)
	UnderlayRemoved(ch Channel, underlay Underlay)
}

// Underlays manages a set of underlays with listener notification on add/remove.
type Underlays struct {
	lock      sync.Mutex
	entries   []Underlay
	listeners []UnderlayEventListener
}

// NewUnderlays creates a new empty Underlays collection.
func NewUnderlays() *Underlays {
	return &Underlays{}
}

// Add appends the underlay and notifies all listeners.
func (u *Underlays) Add(ch Channel, underlay Underlay) {
	u.lock.Lock()
	u.entries = append(u.entries, underlay)
	listeners := u.listeners
	u.lock.Unlock()

	for _, l := range listeners {
		l.UnderlayAdded(ch, underlay)
	}
}

// Remove removes the underlay and notifies all listeners if it was found.
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
	u.lock.Unlock()

	if removed {
		for _, l := range listeners {
			l.UnderlayRemoved(ch, underlay)
		}
	}
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
