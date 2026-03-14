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
	"errors"
	"fmt"
)

// SenderContext provides sequence numbering and close notification for senders.
type SenderContext interface {
	NextSequence() int32
	GetCloseNotify() chan struct{}
}

// Senders provides access to senders and handles transmission failures.
type Senders interface {
	SenderContext
	// GetDefaultSender returns the default sender for the channel.
	GetDefaultSender() Sender
	// HandleTxFailed is called when an underlay write fails.
	// Returns true if the message was requeued for retry.
	HandleTxFailed(underlayType string, sendable Sendable) bool
}

// MessageQueue pairs a Sender with its backing channel. Use this to create
// priority-based message routing: create one queue per priority level, then
// wire them to message sources using MakeSource1/2/3.
type MessageQueue struct {
	C      chan Sendable
	Sender Sender
}

// NewMessageQueue creates a MessageQueue with the given buffer size.
func NewMessageQueue(ctx SenderContext, size int) *MessageQueue {
	ch := make(chan Sendable, size)
	return &MessageQueue{
		C:      ch,
		Sender: NewSingleChSender(ctx, ch),
	}
}

// RetryToQueues returns a HandleTxFailed function that tries to push the sendable
// to each queue in order, returning true if any accepts it without blocking.
func RetryToQueues(queues ...*MessageQueue) func(string, Sendable) bool {
	return func(_ string, sendable Sendable) bool {
		for _, q := range queues {
			select {
			case q.C <- sendable:
				return true
			default:
			}
		}
		return false
	}
}

// NewSingleChSender creates a Sender that writes to a single Go channel.
func NewSingleChSender(ctx SenderContext, msgC chan<- Sendable) Sender {
	return &singleChSender{ctx: ctx, msgC: msgC}
}

type singleChSender struct {
	ctx  SenderContext
	msgC chan<- Sendable
}

func (self *singleChSender) CloseNotify() <-chan struct{} {
	return self.ctx.GetCloseNotify()
}

func (self *singleChSender) TrySend(s Sendable) (bool, error) {
	if err := s.Context().Err(); err != nil {
		return false, err
	}

	s.SetSequence(self.ctx.NextSequence())

	select {
	case <-s.Context().Done():
		if err := s.Context().Err(); err != nil {
			return false, TimeoutError{error: fmt.Errorf("timeout waiting to put message in send queue (%w)", err)}
		}
		return false, TimeoutError{error: errors.New("timeout waiting to put message in send queue")}
	case <-self.ctx.GetCloseNotify():
		return false, ClosedError{}
	case self.msgC <- s:
		return true, nil
	default:
		return false, nil
	}
}

func (self *singleChSender) Send(s Sendable) error {
	if err := s.Context().Err(); err != nil {
		return err
	}

	s.SetSequence(self.ctx.NextSequence())

	select {
	case <-s.Context().Done():
		if err := s.Context().Err(); err != nil {
			return TimeoutError{error: fmt.Errorf("timeout waiting to put message in send queue (%w)", err)}
		}
		return TimeoutError{error: errors.New("timeout waiting to put message in send queue")}
	case <-self.ctx.GetCloseNotify():
		return ClosedError{}
	case self.msgC <- s:
	}
	return nil
}
