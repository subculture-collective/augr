package risk

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

var ErrBreakerTripped = errors.New("risk breaker tripped")

type Breaker interface {
	Allow(ctx context.Context, scope string) error
	Trip(ctx context.Context, scope, reason string) error
	Reset(ctx context.Context, scope string) error
}

type DrawdownBreakerConfig struct{ MaxDailyDD float64 }

type DrawdownBreaker struct {
	cfg  DrawdownBreakerConfig
	repo repository.RiskBreakerRepository
	mu   sync.Mutex
}

func NewDrawdownBreaker(cfg DrawdownBreakerConfig, repo repository.RiskBreakerRepository) *DrawdownBreaker {
	return &DrawdownBreaker{cfg: cfg, repo: repo}
}

func (b *DrawdownBreaker) Allow(ctx context.Context, scope string) error {
	st, err := b.repo.Get(ctx, scope)
	if errors.Is(err, repository.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: %s (%s)", ErrBreakerTripped, scope, st.Reason)
}

func (b *DrawdownBreaker) Trip(ctx context.Context, scope, reason string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.repo.Trip(ctx, scope, reason, time.Now().UTC())
}

func (b *DrawdownBreaker) Reset(ctx context.Context, scope string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.repo.Reset(ctx, scope, time.Now().UTC())
}

func (b *DrawdownBreaker) CheckDrawdown(ctx context.Context, realizedPnL float64) error {
	if b.cfg.MaxDailyDD <= 0 {
		return nil
	}
	if realizedPnL < -b.cfg.MaxDailyDD {
		reason := fmt.Sprintf("realized_pnl=%.2f exceeded max_daily_dd=%.2f", realizedPnL, b.cfg.MaxDailyDD)
		return b.Trip(ctx, domain.RiskBreakerScopeGlobal, reason)
	}
	return nil
}
