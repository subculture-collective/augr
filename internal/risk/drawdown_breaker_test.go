package risk

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type fakeRiskRepo struct {
	state     *domain.RiskBreakerState
	tripCalls int
}

func (f *fakeRiskRepo) Trip(ctx context.Context, scope, reason string, trippedAt time.Time) error {
	f.tripCalls++
	f.state = &domain.RiskBreakerState{Scope: scope, Reason: reason, TrippedAt: trippedAt}
	return nil
}
func (f *fakeRiskRepo) Reset(ctx context.Context, scope string, resetAt time.Time) error {
	f.state = nil
	return nil
}
func (f *fakeRiskRepo) Get(ctx context.Context, scope string) (*domain.RiskBreakerState, error) {
	if f.state == nil || f.state.Scope != scope {
		return nil, repository.ErrNotFound
	}
	return f.state, nil
}
func (f *fakeRiskRepo) ListTripped(ctx context.Context) ([]domain.RiskBreakerState, error) {
	if f.state == nil {
		return nil, nil
	}
	return []domain.RiskBreakerState{*f.state}, nil
}

func TestDrawdownBreaker_AllowAndCheck(t *testing.T) {
	repo := &fakeRiskRepo{}
	b := NewDrawdownBreaker(DrawdownBreakerConfig{MaxDailyDD: 100}, repo)
	if err := b.Allow(context.Background(), domain.RiskBreakerScopeGlobal); err != nil {
		t.Fatal(err)
	}
	if err := b.CheckDrawdown(context.Background(), -50); err != nil {
		t.Fatal(err)
	}
	if err := b.CheckDrawdown(context.Background(), -150); err != nil {
		t.Fatal(err)
	}
	if repo.tripCalls != 1 {
		t.Fatalf("tripCalls=%d want 1", repo.tripCalls)
	}
	if err := b.Allow(context.Background(), domain.RiskBreakerScopeGlobal); !errors.Is(err, ErrBreakerTripped) {
		t.Fatalf("Allow error = %v", err)
	}
}
