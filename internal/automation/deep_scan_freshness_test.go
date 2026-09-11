package automation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/config"
	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/universe"
)

type deepScanCache struct {
	partialResultHistoryRepo
	entry *domain.MarketData
	reads int
}

type scoringWriteRecorder struct {
	operationalUniverseRepo
	writes int
}

func (r *scoringWriteRecorder) UpdateScore(context.Context, string, float64) error {
	r.writes++
	return nil
}

func TestDeepScanGapNeverPersistsOrAcquiresPolygon(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 25, 0, 0, easternTime)
	for _, cached := range []bool{false, true} {
		t.Run(map[bool]string{false: "provider", true: "cache"}[cached], func(t *testing.T) {
			var bars []domain.OHLCV
			for _, day := range []int{1, 2, 3, 4, 10} {
				bars = append(bars, domain.OHLCV{Timestamp: time.Date(2026, 9, day, 9, 30, 0, 0, easternTime), Close: 5, Volume: 100})
			}
			primary := &partialResultProvider{ohlcv: func(string, time.Time, time.Time) ([]domain.OHLCV, error) { return bars, nil }}
			polygon := &partialResultProvider{ohlcv: func(string, time.Time, time.Time) ([]domain.OHLCV, error) {
				t.Fatal("scoring rejection must not acquire Polygon data")
				return nil, nil
			}}
			var cache repository.MarketDataCacheRepository
			if cached {
				raw, err := json.Marshal(bars)
				if err != nil {
					t.Fatal(err)
				}
				cache = &deepScanCache{entry: &domain.MarketData{Data: raw, ExpiresAt: time.Now().Add(time.Hour)}}
			}
			service := data.NewDataService(config.Config{DataProviders: config.DataProviderConfigs{Polygon: config.DataProviderConfig{APIKey: "synthetic-only"}}}, &data.ProviderRegistry{
				Yahoo:   func(data.ProviderConfig) data.DataProvider { return primary },
				Polygon: func(data.ProviderConfig) data.DataProvider { return polygon },
			}, cache, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
			orch := partialResultOrchestrator([]string{"ALP"}, service)
			repo := &scoringWriteRecorder{operationalUniverseRepo: operationalUniverseRepo{watchlist: []universe.TrackedTicker{{Ticker: "ALP"}}}}
			orch.deps.Universe = universe.NewUniverse(repo, nil, nil)
			fallback := &dailyFallbackStub{get: func(context.Context, string, time.Time, time.Time) (data.ExactHistoricalResult, error) {
				return data.ExactHistoricalResult{}, errors.New("synthetic denial")
			}}
			orch.deps.OperationalDailyProvider = fallback
			orch.now = func() time.Time { return now }
			orch.Register("deep_scan", "test", deepScanSpec, orch.deepScan)
			err := orch.deepScan(context.Background())
			summary := singleJobStatus(t, orch, "deep_scan").LastSummary
			if err == nil || repo.writes != 0 || summary["scored"] != 0 || summary["fetch_errors"] != 1 || fallback.calls != 1 || primary.called("ALP", "1d") == cached {
				t.Fatalf("gap did not fail closed: err=%v writes=%d summary=%v primary=%v fallback=%d", err, repo.writes, summary, primary.calls, fallback.calls)
			}
		})
	}
}

func (c *deepScanCache) Get(context.Context, repository.MarketDataCacheKey) (*domain.MarketData, error) {
	c.reads++
	return c.entry, nil
}

func TestDeepScanFreshnessCacheAndClassification(t *testing.T) {
	now := time.Date(2026, time.September, 10, 10, 25, 0, 0, easternTime)
	for _, test := range []struct {
		name         string
		cache        bool
		stale        bool
		count        int
		wantProvider bool
		wantScored   int
		wantShort    int
		wantStale    int
	}{
		{name: "fresh cache avoids acquisition", cache: true, count: 5, wantScored: 1},
		{name: "stale cache does not add generic acquisition", cache: true, stale: true, count: 5, wantStale: 1},
		{name: "fresh provider", count: 5, wantProvider: true, wantScored: 1},
		{name: "insufficient cache remains insufficient", cache: true, count: 4, wantShort: 1},
		{name: "insufficient provider remains insufficient", count: 4, wantProvider: true, wantShort: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			series := func(stale bool, count int) []domain.OHLCV {
				latest := now.AddDate(0, 0, -1)
				if stale {
					latest = now.AddDate(0, 0, -6)
				}
				bars := make([]domain.OHLCV, count)
				for i := range bars {
					bars[i] = domain.OHLCV{Timestamp: latest.AddDate(0, 0, i-count+1), Close: 5, Volume: 100}
				}
				return bars
			}
			provider := &partialResultProvider{ohlcv: func(string, time.Time, time.Time) ([]domain.OHLCV, error) {
				return series(false, test.count), nil
			}}
			var repo repository.MarketDataCacheRepository
			var cache *deepScanCache
			if test.cache {
				raw, err := json.Marshal(series(test.stale, test.count))
				if err != nil {
					t.Fatal(err)
				}
				cache = &deepScanCache{entry: &domain.MarketData{Data: raw, ExpiresAt: time.Now().Add(time.Hour)}}
				repo = cache
			}
			orch := partialResultOrchestrator([]string{"ALP"}, partialResultDataService(provider, repo))
			if test.wantScored == 1 {
				orch.deps.OperationalDailyProvider = &dailyFallbackStub{get: func(context.Context, string, time.Time, time.Time) (data.ExactHistoricalResult, error) {
					t.Fatal("fresh primary must not acquire fallback")
					return data.ExactHistoricalResult{}, nil
				}}
			}
			orch.now = func() time.Time { return now }
			orch.Register("deep_scan", "test", deepScanSpec, orch.deepScan)
			err := orch.deepScan(context.Background())
			summary := singleJobStatus(t, orch, "deep_scan").LastSummary
			if (err == nil) != (test.wantScored == 1) || summary["scored"] != test.wantScored || summary["insufficient"] != test.wantShort || summary["stale"] != test.wantStale {
				t.Fatalf("unexpected classification: err=%v summary=%v", err, summary)
			}
			if provider.called("ALP", "1d") != test.wantProvider {
				t.Fatalf("provider calls=%v, want acquisition=%v", provider.calls, test.wantProvider)
			}
			if cache != nil && cache.reads != 1 {
				t.Fatalf("cache reads=%d, want one", cache.reads)
			}
		})
	}
}

func TestDeepScanFallsBackFromStaleCompletedDailySeries(t *testing.T) {
	now := time.Date(2026, time.September, 10, 10, 25, 0, 0, easternTime)
	bars := func(fresh bool) []domain.OHLCV {
		result := make([]domain.OHLCV, 0, 7)
		for day := 1; day <= 4; day++ {
			result = append(result, domain.OHLCV{Timestamp: time.Date(2026, time.September, day, 9, 30, 0, 0, easternTime), Close: 5, Volume: 100})
		}
		result = append([]domain.OHLCV{{Timestamp: time.Date(2026, time.August, 31, 9, 30, 0, 0, easternTime), Close: 5, Volume: 100}}, result...)
		if fresh {
			result = append(result, domain.OHLCV{Timestamp: time.Date(2026, time.September, 8, 9, 30, 0, 0, easternTime), Close: 5, Volume: 100})
			result = append(result, domain.OHLCV{Timestamp: time.Date(2026, time.September, 9, 9, 30, 0, 0, easternTime), Close: 6, Volume: 200})
		}
		// Today's provisional candle must never disguise missing completed data.
		return append(result, domain.OHLCV{Timestamp: time.Date(2026, time.September, 10, 9, 30, 0, 0, easternTime), Close: 999, Volume: 999})
	}
	for _, outcome := range []string{"fresh", "stale", "denied"} {
		t.Run(outcome, func(t *testing.T) {
			primary := &partialResultProvider{ohlcv: func(string, time.Time, time.Time) ([]domain.OHLCV, error) { return bars(false), nil }}
			fallback := &dailyFallbackStub{get: func(_ context.Context, symbol string, from, to time.Time) (data.ExactHistoricalResult, error) {
				if symbol != "ALP" || !from.Equal(from.UTC().Truncate(24*time.Hour)) || !to.Equal(now.UTC().Truncate(24*time.Hour)) {
					t.Fatalf("incorrect bounded request: %s %v %v", symbol, from, to)
				}
				if outcome == "denied" {
					return data.ExactHistoricalResult{}, errors.New("synthetic provider denied")
				}
				return dailyExactFixture(completedDailyBars(now, bars(outcome == "fresh"))), nil
			}}
			polygon := &partialResultProvider{ohlcv: func(string, time.Time, time.Time) ([]domain.OHLCV, error) {
				t.Fatal("stale success must not cause new Polygon acquisition")
				return nil, nil
			}}
			service := data.NewDataService(config.Config{DataProviders: config.DataProviderConfigs{
				Polygon: config.DataProviderConfig{APIKey: "synthetic-only"},
			}}, &data.ProviderRegistry{
				Yahoo:   func(data.ProviderConfig) data.DataProvider { return primary },
				Polygon: func(data.ProviderConfig) data.DataProvider { return polygon },
			}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
			orch := partialResultOrchestrator([]string{"ALP"}, service)
			orch.deps.OperationalDailyProvider = fallback
			orch.now = func() time.Time { return now }
			orch.Register("deep_scan", "test", deepScanSpec, orch.deepScan)
			err := orch.deepScan(context.Background())
			summary := singleJobStatus(t, orch, "deep_scan").LastSummary
			if !primary.called("ALP", "1d") || fallback.calls != 1 {
				t.Fatalf("stale primary did not fall through: primary=%v fallback=%v", primary.calls, fallback.calls)
			}
			if outcome == "fresh" {
				if err != nil || summary["scored"] != 1 || summary["stale"] != 0 {
					t.Fatalf("fresh fallback rejected: err=%v summary=%v", err, summary)
				}
			} else if err == nil || summary["scored"] != 0 || summary["stale"] != 1 {
				t.Fatalf("unusable fallback accepted: err=%v summary=%v", err, summary)
			}
		})
	}
}
