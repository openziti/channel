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

	"github.com/michaelquigley/pfxlog"
)

// DialPolicy controls how additional underlays are dialed for a multi-underlay channel.
// The channel passes all necessary context directly, so implementations are self-contained.
type DialPolicy interface {
	Dial(underlayType string, connectionId string, groupSecret []byte, connectTimeout time.Duration, cancel <-chan struct{}) (Underlay, error)
}

// BackoffConfig controls exponential backoff behavior.
type BackoffConfig struct {
	// BaseDelay is the initial delay after the first failure (default: 2s).
	BaseDelay time.Duration
	// MaxDelay is the maximum delay between attempts (default: 60s).
	MaxDelay time.Duration
	// MinStableDuration is how long a connection must live to be considered stable (default: 5s).
	// If a new dial is requested before this duration has elapsed since the last success,
	// the connection is treated as short-lived and backoff is applied.
	MinStableDuration time.Duration
	// MinDialInterval is the minimum time between dial attempts (default: 0, no limit).
	// If a dial is requested before this interval has elapsed since the last dial,
	// the goroutine sleeps until the interval is satisfied.
	MinDialInterval time.Duration
}

// DefaultBackoffConfig provides sensible defaults for BackoffConfig.
var DefaultBackoffConfig = BackoffConfig{
	BaseDelay:         2 * time.Second,
	MaxDelay:          time.Minute,
	MinStableDuration: 5 * time.Second,
}

// BackoffDialPolicy wraps a DialUnderlayFactory with exponential backoff retry logic.
// It tracks consecutive failures and detects short-lived connections.
type BackoffDialPolicy struct {
	Dialer              DialUnderlayFactory
	Backoff             BackoffConfig
	mu                  sync.Mutex
	consecutiveFailures uint32
	lastSuccess         time.Time
	lastDialTime        time.Time
}

// NewBackoffDialPolicy creates a BackoffDialPolicy with default configuration.
func NewBackoffDialPolicy(dialer DialUnderlayFactory) *BackoffDialPolicy {
	return &BackoffDialPolicy{
		Dialer:  dialer,
		Backoff: DefaultBackoffConfig,
	}
}

// NewBackoffDialPolicyWithConfig creates a BackoffDialPolicy with the given configuration.
func NewBackoffDialPolicyWithConfig(dialer DialUnderlayFactory, config BackoffConfig) *BackoffDialPolicy {
	return &BackoffDialPolicy{
		Dialer:  dialer,
		Backoff: config,
	}
}

// recordStartDial evaluates the previous connection's lifetime at the start of a new dial.
// If the last connection was short-lived (< MinStableDuration), it counts as a failure.
// If it was stable, the failure count is reset.
func (self *BackoffDialPolicy) recordStartDial() {
	self.mu.Lock()
	defer self.mu.Unlock()

	if self.lastSuccess.IsZero() {
		return
	}

	if time.Since(self.lastSuccess) < self.Backoff.MinStableDuration {
		self.consecutiveFailures++
	} else {
		self.consecutiveFailures = 0
	}
	self.lastSuccess = time.Time{} // reset last success
}

func (self *BackoffDialPolicy) getBackoffDelay() time.Duration {
	self.mu.Lock()
	defer self.mu.Unlock()

	if self.consecutiveFailures == 0 {
		return 0
	}

	// Exponential backoff: baseDelay * 2^(failures-1), capped at maxDelay
	delay := self.Backoff.BaseDelay * (1 << min(self.consecutiveFailures-1, 30))
	return min(delay, self.Backoff.MaxDelay)
}

func (self *BackoffDialPolicy) recordSuccess() {
	self.mu.Lock()
	defer self.mu.Unlock()
	self.lastSuccess = time.Now()
	// Don't reset consecutiveFailures yet — wait to see if connection is stable.
	// Checked on next call to recordShortLivedConnection.
}

func (self *BackoffDialPolicy) recordFailure() {
	self.mu.Lock()
	defer self.mu.Unlock()
	self.consecutiveFailures++
	self.lastSuccess = time.Time{}
}

// ConsecutiveFailures returns the current consecutive failure count.
func (self *BackoffDialPolicy) ConsecutiveFailures() uint32 {
	self.mu.Lock()
	defer self.mu.Unlock()
	return self.consecutiveFailures
}

// LastDialTime returns the time of the most recent dial attempt.
func (self *BackoffDialPolicy) LastDialTime() time.Time {
	self.mu.Lock()
	defer self.mu.Unlock()
	return self.lastDialTime
}

// Dial attempts to create a new underlay, applying exponential backoff if previous attempts failed.
func (self *BackoffDialPolicy) Dial(underlayType string, connectionId string, groupSecret []byte, connectTimeout time.Duration, cancel <-chan struct{}) (Underlay, error) {
	log := pfxlog.Logger().WithField("underlayType", underlayType).WithField("connectionId", connectionId)

	self.recordStartDial()

	if self.Backoff.MinDialInterval > 0 {
		self.mu.Lock()
		if elapsed := time.Since(self.lastDialTime); elapsed < self.Backoff.MinDialInterval {
			wait := self.Backoff.MinDialInterval - elapsed
			self.mu.Unlock()
			select {
			case <-cancel:
				return nil, ClosedError{}
			case <-time.After(wait):
			}
		} else {
			self.mu.Unlock()
		}
	}

	self.mu.Lock()
	self.lastDialTime = time.Now()
	self.mu.Unlock()

	if delay := self.getBackoffDelay(); delay > 0 {
		log.WithField("delay", delay).Debug("backing off before dial")
		select {
		case <-cancel:
			return nil, ClosedError{}
		case <-time.After(delay):
		}
	}

	select {
	case <-cancel:
		return nil, ClosedError{}
	default:
	}

	underlay, err := self.Dialer.CreateWithHeaders(connectTimeout, map[int32][]byte{
		TypeHeader:         []byte(underlayType),
		ConnectionIdHeader: []byte(connectionId),
		GroupSecretHeader:  groupSecret,
		IsGroupedHeader:    {1},
	})
	if err != nil {
		self.recordFailure()
		log.WithError(err).Info("dial failed")
		return nil, err
	}

	self.recordSuccess()
	return underlay, nil
}
