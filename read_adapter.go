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
	"github.com/michaelquigley/pfxlog"
	"github.com/openziti/foundation/v2/concurrenz"
	"github.com/pkg/errors"
	"io"
	"sync/atomic"
	"time"
)

// ErrClosed is returned when a read is attempted on a closed ReadAdapter.
var ErrClosed = errors.New("channel closed")

// ReadTimout is returned when a read operation exceeds its deadline. Implements net.Error.
type ReadTimout struct{}

func (r ReadTimout) Error() string {
	return "read timed out"
}

func (r ReadTimout) Timeout() bool {
	return true
}

func (r ReadTimout) Temporary() bool {
	return true
}

// NewReadAdapter creates a ReadAdapter with the given label and internal buffer depth.
func NewReadAdapter(label string, channelDepth int) *ReadAdapter {
	return &ReadAdapter{
		label:          label,
		ch:             make(chan []byte, channelDepth),
		closeNotify:    make(chan struct{}),
		deadlineNotify: make(chan struct{}),
	}
}

// ReadAdapter bridges push-based data delivery into an io.Reader interface with deadline support.
type ReadAdapter struct {
	label          string
	ch             chan []byte
	closeNotify    chan struct{}
	deadlineNotify chan struct{}
	deadline       concurrenz.AtomicValue[time.Time]
	closed         atomic.Bool
	readInProgress atomic.Bool
	leftover       []byte
}

// PushData enqueues data to be read. Blocks until the data is accepted or the adapter is closed.
func (self *ReadAdapter) PushData(data []byte) error {
	select {
	case self.ch <- data:
		return nil
	case <-self.closeNotify:
		return ErrClosed
	}
}

// SetReadDeadline sets the deadline for the next GetNext/Read call. A zero value clears the deadline.
func (self *ReadAdapter) SetReadDeadline(deadline time.Time) error {
	self.deadline.Store(deadline)
	if self.readInProgress.Load() {
		select {
		case self.deadlineNotify <- struct{}{}:
		case <-time.After(5 * time.Millisecond):
		}
	} else {
		select {
		case self.deadlineNotify <- struct{}{}:
		default:
		}
	}
	return nil
}

// GetNext blocks until data is available, the deadline expires, or the adapter is closed.
func (self *ReadAdapter) GetNext() ([]byte, error) {
	self.readInProgress.Store(true)
	defer self.readInProgress.Store(false)

	for {
		deadline := self.deadline.Load()

		var timeoutCh <-chan time.Time

		if !deadline.IsZero() {
			timeoutCh = time.After(time.Until(deadline))
		}

		select {
		case data := <-self.ch:
			return data, nil
		case <-self.closeNotify:
			// If we're closed, return any buffered values, otherwise return nil
			select {
			case data := <-self.ch:
				return data, nil
			default:
				return nil, ErrClosed
			}
		case <-self.deadlineNotify:
			continue
		case <-timeoutCh:
			// If we're timing out, return any buffered values, otherwise return nil
			select {
			case data := <-self.ch:
				return data, nil
			default:
				return nil, &ReadTimout{}
			}
		}
	}
}

func (self *ReadAdapter) Read(b []byte) (n int, err error) {
	log := pfxlog.Logger().WithField("label", self.label)
	if self.closed.Load() {
		return 0, io.EOF
	}

	log.Tracef("read buffer = %d bytes", len(b))
	if len(self.leftover) > 0 {
		log.Tracef("found %d leftover bytes", len(self.leftover))
		n = copy(b, self.leftover)
		self.leftover = self.leftover[n:]
		return n, nil
	}

	d, err := self.GetNext()
	if err != nil {
		return 0, err
	}
	log.Tracef("got buffer from sequencer %d bytes", len(d))

	n = copy(b, d)
	self.leftover = d[n:]

	log.Tracef("saving %d bytes for leftover", len(self.leftover))
	log.Tracef("reading %v bytes", n)
	return n, nil
}

func (self *ReadAdapter) Close() {
	if self.closed.CompareAndSwap(false, true) {
		close(self.closeNotify)
	}
}
