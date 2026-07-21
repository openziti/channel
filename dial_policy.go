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
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/michaelquigley/pfxlog"
)

// DialPolicy controls how additional underlays are dialed for a multi-underlay channel.
// The channel passes all necessary context directly, so implementations are self-contained.
//
// isFirst is true when the dial is (re)establishing the group's first underlay - either the
// initial connection or a reconnect after all underlays were lost. A first connection must
// set the IsFirstGroupConnection header so the remote MultiListener creates a new channel
// rather than rejecting the underlay; subsequent underlays attach to the existing group.
//
// UnderlayClosed is invoked by the channel whenever an underlay is removed, with how long
// the underlay lived. This lets the policy judge connection stability directly, e.g. for
// flap detection, rather than inferring it from dial timing.
type DialPolicy interface {
	Dial(underlayType string, connectionId string, groupSecret []byte, isFirst bool, connectTimeout time.Duration, cancel <-chan struct{}) (Underlay, error)
	UnderlayClosed(underlayType string, lifetime time.Duration)
}

// BackoffConfig controls exponential backoff behavior.
type BackoffConfig struct {
	// BaseDelay is the initial delay after the first failure (default: 2s).
	BaseDelay time.Duration
	// MaxDelay is the maximum delay between attempts (default: 60s).
	MaxDelay time.Duration
	// MinStableDuration is how long a connection must live to be considered stable (default: 30s).
	// An underlay that closes before living this long is treated as short-lived and counts as
	// a failure for backoff purposes.
	MinStableDuration time.Duration
	// MinDialInterval is the minimum time between dial attempts (default: 0, no limit).
	// If a dial is requested before this interval has elapsed since the last dial,
	// the goroutine sleeps until the interval is satisfied.
	MinDialInterval time.Duration
	// Jitter is the maximum random fraction (0.0-1.0) of the computed backoff delay to subtract as
	// jitter (default: 0, disabled). When many peers back off in lockstep off the same exponential
	// schedule they re-dial simultaneously, producing a thundering herd on the destination. A
	// non-zero Jitter spreads those redials by subtracting a random delay of up to Jitter*delay from
	// the computed backoff, so the result falls within [(1-Jitter), 1]*delay. Subtracting rather than
	// adding keeps the delay within MaxDelay while still spreading redials once the backoff reaches
	// its cap. Values <= 0 disable jitter; values above 1.0 are capped at 1.0.
	Jitter float64
}

// DefaultBackoffConfig provides sensible defaults for BackoffConfig.
var DefaultBackoffConfig = BackoffConfig{
	BaseDelay:         2 * time.Second,
	MaxDelay:          time.Minute,
	MinStableDuration: 30 * time.Second,
}

// BackoffDialPolicy wraps a DialUnderlayFactory with exponential backoff retry logic.
// Failed dials and short-lived connections (reported via UnderlayClosed) count as failures;
// a connection that closes after a stable lifetime, or one that has demonstrably outlived
// MinStableDuration, resets the failure count.
// Accounting is intentionally global for the policy because all underlays dial the same
// destination. A stable underlay is evidence that the destination is not globally flapping.
type BackoffDialPolicy struct {
	Dialer              DialUnderlayFactory
	Backoff             BackoffConfig
	mu                  sync.Mutex
	consecutiveFailures uint32
	lastDialSuccess     time.Time
	lastNegativeEvent   time.Time
	lastDialTime        time.Time
	iteration           uint32
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

// UnderlayClosed records the lifetime of a closed underlay. A short-lived underlay
// (lifetime < MinStableDuration) counts as a failure for backoff purposes; a stable one
// resets the failure count.
func (self *BackoffDialPolicy) UnderlayClosed(underlayType string, lifetime time.Duration) {
	self.mu.Lock()
	defer self.mu.Unlock()

	if lifetime < self.Backoff.MinStableDuration {
		self.consecutiveFailures++
		self.lastNegativeEvent = time.Now()
		pfxlog.Logger().WithField("underlayType", underlayType).
			WithField("lifetime", lifetime).
			WithField("consecutiveFailures", self.consecutiveFailures).
			Info("short-lived underlay closed")
	} else {
		self.consecutiveFailures = 0
	}
}

// resetStaleFailures clears the failure count if the most recent successful dial postdates the
// last negative event (dial failure or short-lived close) and has aged past MinStableDuration.
// That connection has proven stable - if it had died young, the resulting UnderlayClosed call
// would have moved lastNegativeEvent past it. This keeps failures from a past flap episode
// from imposing backoff after a connection has settled.
func (self *BackoffDialPolicy) resetStaleFailures() {
	self.mu.Lock()
	defer self.mu.Unlock()

	if self.consecutiveFailures == 0 || self.lastDialSuccess.IsZero() {
		return
	}

	if self.lastDialSuccess.After(self.lastNegativeEvent) && time.Since(self.lastDialSuccess) >= self.Backoff.MinStableDuration {
		self.consecutiveFailures = 0
	}
}

// getBackoffDelay computes the delay before the next dial attempt. It is the exponential backoff
// (baseDelay * 2^(failures-1), capped at MaxDelay), optionally with random jitter subtracted when
// Backoff.Jitter is configured. Jitter spreads otherwise-synchronized redials across peers while
// keeping the delay within MaxDelay; see BackoffConfig.Jitter. With no failures the delay is zero
// and no jitter is applied.
func (self *BackoffDialPolicy) getBackoffDelay() time.Duration {
	self.mu.Lock()
	defer self.mu.Unlock()

	if self.consecutiveFailures == 0 {
		return 0
	}

	// Exponential backoff: baseDelay * 2^(failures-1), capped at maxDelay
	delay := self.Backoff.BaseDelay * (1 << min(self.consecutiveFailures-1, 30))
	delay = min(delay, self.Backoff.MaxDelay)

	if jitter := self.Backoff.Jitter; jitter > 0 && delay > 0 {
		if jitter > 1.0 {
			jitter = 1.0
		}
		// subtract rather than add so the result stays within [(1-jitter), 1]*delay and never
		// exceeds MaxDelay, while still spreading redials once the backoff reaches its cap.
		delay -= time.Duration(rand.Float64() * jitter * float64(delay))
	}

	return delay
}

func (self *BackoffDialPolicy) recordSuccess() {
	self.mu.Lock()
	defer self.mu.Unlock()
	self.lastDialSuccess = time.Now()
	// Don't reset consecutiveFailures yet - wait to see if the connection is stable.
	// It resets via UnderlayClosed (stable close) or resetStaleFailures (proven lifetime).
}

func (self *BackoffDialPolicy) recordFailure() {
	self.mu.Lock()
	defer self.mu.Unlock()
	self.consecutiveFailures++
	self.lastNegativeEvent = time.Now()
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

// groupConnectionId returns the wire connection id to use for this dial. On a first connection it
// advances the iteration counter so the (re)established group gets a distinct id; the initial
// iteration (0) uses the base id unchanged so it matches the externally dialed first underlay.
func (self *BackoffDialPolicy) groupConnectionId(connectionId string, isFirst bool) string {
	self.mu.Lock()
	defer self.mu.Unlock()
	if isFirst {
		self.iteration++
	}
	if self.iteration == 0 {
		return connectionId
	}
	return fmt.Sprintf("%s-%d", connectionId, self.iteration)
}

// Dial attempts to create a new underlay, applying exponential backoff if previous attempts failed.
// When isFirst is true the dial (re)establishes the group: it advances the iteration, derives a
// fresh iteration-suffixed connection id, and sets the IsFirstGroupConnection header so the remote
// MultiListener creates a new channel. A fresh id avoids attaching to a still-closing channel of
// the prior iteration during a loss/reconnect race. Subsequent (isFirst == false) dials reuse the
// current iteration id with no header, so they attach to the established group.
func (self *BackoffDialPolicy) Dial(underlayType string, connectionId string, groupSecret []byte, isFirst bool, connectTimeout time.Duration, cancel <-chan struct{}) (Underlay, error) {
	groupId := self.groupConnectionId(connectionId, isFirst)
	log := pfxlog.Logger().WithField("underlayType", underlayType).WithField("connectionId", groupId).WithField("isFirst", isFirst)

	self.resetStaleFailures()

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

	headers := map[int32][]byte{
		TypeHeader:         []byte(underlayType),
		ConnectionIdHeader: []byte(groupId),
		GroupSecretHeader:  groupSecret,
		IsGroupedHeader:    {1},
	}
	if isFirst {
		Headers(headers).PutBoolHeader(IsFirstGroupConnection, true)
	}

	underlay, err := self.Dialer.CreateWithHeaders(connectTimeout, headers)
	if err != nil {
		self.recordFailure()
		log.WithError(err).Info("dial failed")
		return nil, err
	}

	self.recordSuccess()
	return underlay, nil
}
