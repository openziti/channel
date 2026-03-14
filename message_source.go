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

import "io"

// MessageSourceF is a function that returns the next message to send on an underlay.
// It blocks until a message is available or the notifier signals closure.
type MessageSourceF func(notifier *CloseNotifier) (Sendable, error)

// MessageSourceProvider returns the message source for a given underlay type.
type MessageSourceProvider interface {
	GetMessageSource(underlayType string) MessageSourceF
}

// MakeSource1 returns a MessageSourceF that reads from one queue.
func MakeSource1(closeNotify <-chan struct{}, q1 <-chan Sendable) MessageSourceF {
	return func(notifier *CloseNotifier) (Sendable, error) {
		select {
		case msg := <-q1:
			return msg, nil
		case <-closeNotify:
			return nil, io.EOF
		case <-notifier.GetCloseNotify():
			return nil, io.EOF
		}
	}
}

// MakeSource2 returns a MessageSourceF that reads from two queues with equal priority.
func MakeSource2(closeNotify <-chan struct{}, q1, q2 <-chan Sendable) MessageSourceF {
	return func(notifier *CloseNotifier) (Sendable, error) {
		select {
		case msg := <-q1:
			return msg, nil
		case msg := <-q2:
			return msg, nil
		case <-closeNotify:
			return nil, io.EOF
		case <-notifier.GetCloseNotify():
			return nil, io.EOF
		}
	}
}

// MakeSource3 returns a MessageSourceF that reads from three queues with equal priority.
func MakeSource3(closeNotify <-chan struct{}, q1, q2, q3 <-chan Sendable) MessageSourceF {
	return func(notifier *CloseNotifier) (Sendable, error) {
		select {
		case msg := <-q1:
			return msg, nil
		case msg := <-q2:
			return msg, nil
		case msg := <-q3:
			return msg, nil
		case <-closeNotify:
			return nil, io.EOF
		case <-notifier.GetCloseNotify():
			return nil, io.EOF
		}
	}
}

// SimpleMessageSourceProvider maps underlay types to message sources, with a default fallback.
type SimpleMessageSourceProvider struct {
	defaultSource MessageSourceF
	sources       map[string]MessageSourceF
}

// NewSimpleMessageSourceProvider creates a provider with the given default message source.
func NewSimpleMessageSourceProvider(defaultSource MessageSourceF) *SimpleMessageSourceProvider {
	return &SimpleMessageSourceProvider{
		defaultSource: defaultSource,
		sources:       map[string]MessageSourceF{},
	}
}

// AddSource registers a message source for a specific underlay type.
func (p *SimpleMessageSourceProvider) AddSource(underlayType string, source MessageSourceF) {
	p.sources[underlayType] = source
}

// GetMessageSource returns the message source for the given underlay type, falling back to the default.
func (p *SimpleMessageSourceProvider) GetMessageSource(underlayType string) MessageSourceF {
	if source, ok := p.sources[underlayType]; ok {
		return source
	}
	return p.defaultSource
}
