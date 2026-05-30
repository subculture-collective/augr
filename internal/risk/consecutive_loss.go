// Package risk contains shared risk controls used by the trading system.
// ConsecutiveLossBreaker records closed-trade outcomes per strategy and trips
// the corresponding strategy scope after a configured loss streak.
package risk

import (
	"context"
	"fmt"
	"sync"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

const (
	DefaultConsecutiveLossThreshold = 3
	DefaultConsecutiveLossWindows   = 5
)

type ConsecutiveLossConfig struct {
	Threshold int
	Windows   int
}

type ConsecutiveLossBreaker struct {
	cfg     ConsecutiveLossConfig
	breaker Breaker
	mu      sync.Mutex
	losses  map[string]int
	blocked map[string]int
}

func NewConsecutiveLossBreaker(cfg ConsecutiveLossConfig, breaker Breaker) *ConsecutiveLossBreaker {
	if cfg.Threshold <= 0 {
		cfg.Threshold = DefaultConsecutiveLossThreshold
	}
	if cfg.Windows <= 0 {
		cfg.Windows = DefaultConsecutiveLossWindows
	}
	return &ConsecutiveLossBreaker{cfg: cfg, breaker: breaker, losses: map[string]int{}, blocked: map[string]int{}}
}

func (c *ConsecutiveLossBreaker) RecordResult(ctx context.Context, strategyID string, pnl float64) error {
	c.mu.Lock()
	if c.blocked[strategyID] > 0 {
		c.mu.Unlock()
		return nil
	}
	if pnl < 0 {
		c.losses[strategyID]++
	} else {
		c.losses[strategyID] = 0
	}
	streak := c.losses[strategyID]
	shouldTrip := streak >= c.cfg.Threshold
	if shouldTrip {
		c.losses[strategyID] = 0
		c.blocked[strategyID] = c.cfg.Windows
	}
	c.mu.Unlock()
	if !shouldTrip {
		return nil
	}
	reason := fmt.Sprintf("consecutive_losses=%d threshold=%d blocking %d windows", streak, c.cfg.Threshold, c.cfg.Windows)
	return c.breaker.Trip(ctx, domain.RiskBreakerScopeStrategy(strategyID), reason)
}

func (c *ConsecutiveLossBreaker) UnblockExpired(ctx context.Context) error {
	c.mu.Lock()
	var toReset []string
	for sid, n := range c.blocked {
		n--
		if n <= 0 {
			delete(c.blocked, sid)
			toReset = append(toReset, sid)
		} else {
			c.blocked[sid] = n
		}
	}
	c.mu.Unlock()
	var firstErr error
	for _, sid := range toReset {
		if err := c.breaker.Reset(ctx, domain.RiskBreakerScopeStrategy(sid)); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (c *ConsecutiveLossBreaker) Blocked(strategyID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.blocked[strategyID]
}
