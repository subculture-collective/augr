// Package redditlimit coordinates Reddit feed throttling across every in-process
// consumer. Reddit applies limits at the client/provider level, not per subreddit,
// so a 429 from any feed must pause social sentiment and signal ingestion together.
package redditlimit

import (
	"errors"
	"math/rand/v2"
	"sync"
	"time"
)

const defaultCooldown = 15 * time.Minute

// ErrBudgetExhausted reports that a feed request was skipped because the
// shared hourly request budget is spent.
var ErrBudgetExhausted = errors.New("reddit: hourly request budget exhausted")

// Coordinator stores provider-wide cooldown and freshness state.
type Coordinator struct {
	mu          sync.Mutex
	cooldownTil time.Time
	lastSuccess time.Time
	observer    Observer
	// hourlyBudget caps feed requests across every consumer in a sliding hour;
	// zero means unlimited. requests holds the timestamps inside that hour.
	hourlyBudget int
	requests     []time.Time
}

// Observer exposes provider freshness and cooldown state without coupling the
// integration package to a metrics implementation.
type Observer interface {
	RecordDataSourceSuccess(source string, at time.Time)
	SetDataSourceCooldown(source string, until time.Time)
}

// Default is shared by all Reddit consumers in the application process.
var Default = &Coordinator{}

// Remaining returns the active provider-wide cooldown duration.
func (c *Coordinator) Remaining(now time.Time) time.Duration {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	remaining := c.cooldownTil.Sub(now)
	if remaining <= 0 {
		c.cooldownTil = time.Time{}
		observer := c.observer
		c.mu.Unlock()
		if observer != nil {
			observer.SetDataSourceCooldown("reddit", time.Time{})
		}
		return 0
	}
	c.mu.Unlock()
	return remaining
}

// Start begins or extends the provider-wide cooldown. A small positive jitter
// prevents all workers from resuming on the same boundary after Retry-After.
func (c *Coordinator) Start(now time.Time, duration time.Duration) time.Duration {
	if c == nil {
		return 0
	}
	if duration <= 0 {
		duration = defaultCooldown
	}
	jitter := time.Duration(rand.Int64N(max(int64(duration/10), 1)))
	effective := duration + jitter
	until := now.Add(effective)
	c.mu.Lock()
	if until.After(c.cooldownTil) {
		c.cooldownTil = until
	}
	observedUntil := c.cooldownTil
	observer := c.observer
	c.mu.Unlock()
	if observer != nil {
		observer.SetDataSourceCooldown("reddit", observedUntil)
	}
	return effective
}

// MarkSuccess records the last successful Reddit fetch.
func (c *Coordinator) MarkSuccess(at time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if at.After(c.lastSuccess) {
		c.lastSuccess = at
	}
	lastSuccess := c.lastSuccess
	observer := c.observer
	c.mu.Unlock()
	if observer != nil {
		observer.RecordDataSourceSuccess("reddit", lastSuccess)
		observer.SetDataSourceCooldown("reddit", time.Time{})
	}
}

// SetObserver attaches an optional process-wide freshness observer and
// immediately publishes the currently known state.
func (c *Coordinator) SetObserver(observer Observer) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.observer = observer
	lastSuccess := c.lastSuccess
	cooldownTil := c.cooldownTil
	c.mu.Unlock()
	if observer != nil {
		if !lastSuccess.IsZero() {
			observer.RecordDataSourceSuccess("reddit", lastSuccess)
		}
		observer.SetDataSourceCooldown("reddit", cooldownTil)
	}
}

// LastSuccess returns the provider-wide freshness timestamp.
func (c *Coordinator) LastSuccess() time.Time {
	if c == nil {
		return time.Time{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastSuccess
}

// SetHourlyBudget caps Reddit feed requests across all consumers in any
// sliding hour. Unauthenticated RSS answers bursts with a 429 and a 15-minute
// Retry-After, so staying under the budget keeps feeds available. Zero or a
// negative value removes the cap.
func (c *Coordinator) SetHourlyBudget(requests int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hourlyBudget = max(requests, 0)
}

// Acquire reserves one feed request. It returns false when the hourly budget
// is spent; callers skip the request instead of sending it.
func (c *Coordinator) Acquire(now time.Time) bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hourlyBudget <= 0 {
		return true
	}
	cutoff := now.Add(-time.Hour)
	kept := c.requests[:0]
	for _, at := range c.requests {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	c.requests = kept
	if len(c.requests) >= c.hourlyBudget {
		return false
	}
	c.requests = append(c.requests, now)
	return true
}
