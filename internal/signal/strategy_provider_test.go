package signal

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestStrategyProviderWithCache_DefensiveCopies(t *testing.T) {
	t.Parallel()

	original := []StrategyWithThesis{{
		ID:         uuid.New(),
		Ticker:     "AAPL",
		WatchTerms: []string{"apple", "mac"},
	}}
	inner := &staticStrategyProvider{result: original}
	provider := NewStrategyProviderWithCache(inner, time.Hour)

	first, err := provider.ListActiveWithThesis(context.Background())
	if err != nil {
		t.Fatalf("first call error = %v", err)
	}
	first[0].Ticker = "MUTATED"
	first[0].WatchTerms[0] = "changed"

	second, err := provider.ListActiveWithThesis(context.Background())
	if err != nil {
		t.Fatalf("second call error = %v", err)
	}
	if second[0].Ticker != "AAPL" || second[0].WatchTerms[0] != "apple" {
		t.Fatalf("cache leaked mutation on hit: %+v", second[0])
	}
	second[0].Ticker = "MUTATED-AGAIN"
	second[0].WatchTerms[1] = "changed-again"

	third, err := provider.ListActiveWithThesis(context.Background())
	if err != nil {
		t.Fatalf("third call error = %v", err)
	}
	if third[0].Ticker != "AAPL" || third[0].WatchTerms[1] != "mac" {
		t.Fatalf("cache leaked mutation from hit copy: %+v", third[0])
	}
	if inner.calls != 1 {
		t.Fatalf("inner calls = %d, want 1", inner.calls)
	}
}

type staticStrategyProvider struct {
	calls  int
	result []StrategyWithThesis
}

func (p *staticStrategyProvider) ListActiveWithThesis(context.Context) ([]StrategyWithThesis, error) {
	p.calls++
	out := make([]StrategyWithThesis, len(p.result))
	copy(out, p.result)
	return out, nil
}
