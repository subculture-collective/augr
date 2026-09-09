package discovery

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// Synthetic unit-test boundary, not provider or production evaluation evidence.
type frozenScopeLoader struct {
	start, end time.Time
}

func (loader frozenScopeLoader) LoadResearchInterval(context.Context, uuid.UUID) (data.ResearchInterval, error) {
	return data.ResearchInterval{Start: loader.start, End: loader.end}, nil
}

func (loader frozenScopeLoader) LoadSymbol(_ context.Context, _ uuid.UUID, _ string, _ data.Timeframe, from, to time.Time) ([]domain.OHLCV, data.ManifestBindingReceipt, error) {
	if from.Before(loader.start) || to.After(loader.end) {
		return nil, data.ManifestBindingReceipt{}, fmt.Errorf("request %s..%s escapes frozen scope %s..%s", from, to, loader.start, loader.end)
	}
	var bars []domain.OHLCV
	for at := from; !at.After(to); at = at.AddDate(0, 0, 1) {
		bars = append(bars, domain.OHLCV{Timestamp: at, Open: 100, High: 103, Low: 97, Close: 101, Volume: 1_000_000})
	}
	return bars, data.ManifestBindingReceipt{EffectiveStart: from, EffectiveEnd: to}, nil
}

func TestScreenUsesFrozenManifestEvaluationInterval(t *testing.T) {
	loader := frozenScopeLoader{
		start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		end:   time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
	}
	service, err := data.NewManifestBoundDataService(&data.DataService{}, uuid.New(), loader)
	if err != nil {
		t.Fatal(err)
	}
	results, err := Screen(context.Background(), service, ScreenerConfig{
		Tickers: []string{"SPY"}, MarketType: domain.MarketTypeStock,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil || len(results) != 1 {
		t.Fatalf("frozen scope screening should not request wall-clock history: results=%d err=%v", len(results), err)
	}
}

func TestSweepHistoryUsesFrozenManifestIntervalAcrossWallClocks(t *testing.T) {
	loader := frozenScopeLoader{
		start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		end:   time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
	}
	service, err := data.NewManifestBoundDataService(&data.DataService{}, uuid.New(), loader)
	if err != nil {
		t.Fatal(err)
	}
	for _, now := range []time.Time{loader.start.AddDate(-10, 0, 0), loader.end.AddDate(10, 0, 0)} {
		t.Run(now.Format("2006-01-02"), func(t *testing.T) {
			bars, err := loadSweepHistory(context.Background(), service, domain.MarketTypeStock, "SPY", now)
			if err != nil || len(bars) != 366 {
				t.Fatalf("scope sweep history: count=%d err=%v", len(bars), err)
			}
			if !bars[0].Timestamp.Equal(loader.start) || !bars[len(bars)-1].Timestamp.Equal(loader.end) {
				t.Fatal("sweep dates changed with wall clock")
			}
		})
	}
}
