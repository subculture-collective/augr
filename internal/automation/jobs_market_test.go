package automation

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/config"
	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
	"github.com/PatrickFanella/get-rich-quick/internal/universe"
)

type timedCurrentScopeProvider struct {
	mu          sync.Mutex
	callTime    time.Duration
	virtualTime time.Duration
	calls       int
}

func (p *timedCurrentScopeProvider) GetOHLCV(_ context.Context, ticker string, timeframe data.Timeframe, _, to time.Time) ([]domain.OHLCV, error) {
	p.mu.Lock()
	p.calls++
	p.virtualTime += p.callTime
	p.mu.Unlock()
	latest := to
	previous := to.Add(-5 * time.Minute)
	if timeframe == data.Timeframe1d {
		latest = expectedCompletedNYSESession(to)
		previous = latest.AddDate(0, 0, -1)
	} else {
		switch ticker {
		case "POS00", "POS01":
			return nil, nil
		case "POS02":
			latest = to.Add(-25 * time.Minute)
			previous = latest.Add(-5 * time.Minute)
		}
	}
	return []domain.OHLCV{
		{Timestamp: previous, Open: 1, High: 1, Low: 1, Close: 1, Volume: 1},
		{Timestamp: latest, Open: 1, High: 1, Low: 1, Close: 2, Volume: 2},
	}, nil
}

func (*timedCurrentScopeProvider) GetFundamentals(context.Context, string) (data.Fundamentals, error) {
	return data.Fundamentals{}, data.ErrNotImplemented
}

func (*timedCurrentScopeProvider) GetNews(context.Context, string, time.Time, time.Time) ([]data.NewsArticle, error) {
	return nil, data.ErrNotImplemented
}

func (*timedCurrentScopeProvider) GetSocialSentiment(context.Context, string, time.Time, time.Time) ([]data.SocialSentiment, error) {
	return nil, data.ErrNotImplemented
}

func TestCurrentDataRefreshUsesOperationalScopeAndHandsOffExactDegradedPayload(t *testing.T) {
	positions := make([]*domain.Position, 0, 41)
	strategies := make([]domain.Strategy, 0, 41)
	expected := make([]string, 0, 75)
	for i := 0; i < 39; i++ {
		ticker := fmt.Sprintf("POS%02d", i)
		positions = append(positions, &domain.Position{ID: uuid.New(), Ticker: ticker, MarketType: domain.MarketTypeStock})
		if i > 2 {
			expected = append(expected, ticker)
		}
	}
	for i := 0; i < 39; i++ {
		ticker := fmt.Sprintf("STRAT%02d", i)
		strategies = append(strategies, domain.Strategy{ID: uuid.New(), Ticker: ticker, Status: domain.StrategyStatusActive, MarketType: domain.MarketTypeStock})
		expected = append(expected, ticker)
	}
	positions = append(positions,
		&domain.Position{ID: uuid.New(), Ticker: "KALSHI", MarketType: domain.MarketTypeKalshi},
		&domain.Position{ID: uuid.New(), Ticker: "OPTION", MarketType: domain.MarketTypeStock, AssetClass: domain.AssetClassOption},
	)
	strategies = append(strategies,
		domain.Strategy{ID: uuid.New(), Ticker: "PAUSED", Status: domain.StrategyStatusPaused, MarketType: domain.MarketTypeStock},
		domain.Strategy{ID: uuid.New(), Ticker: "POLYMARKET", Status: domain.StrategyStatusActive, MarketType: domain.MarketTypePolymarket},
	)
	watchlist := []universe.TrackedTicker{{Ticker: "WATCHLIST_ONLY"}}
	universeRepo := &operationalUniverseRepo{watchlist: watchlist}
	provider := &timedCurrentScopeProvider{callTime: 5 * time.Second}
	registry := data.NewProviderRegistry()
	registry.Yahoo = func(data.ProviderConfig) data.DataProvider { return provider }
	service := data.NewDataService(
		config.Config{},
		registry,
		&partialResultHistoryRepo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
	)
	orch := NewJobOrchestrator(OrchestratorDeps{
		PositionRepo: newRecordingPositionRepo(positions...),
		StrategyRepo: &kalshiStrategyRepoStub{strategies: strategies},
		Universe:     universe.NewUniverse(universeRepo, nil, nil),
		DataService:  service,
	})
	orch.now = func() time.Time { return time.Date(2026, time.August, 6, 10, 30, 0, 0, easternTime) }
	orch.Register("current_data_refresh", "test", currentDataRefreshSpec, orch.currentDataRefresh)

	if err := orch.currentDataRefresh(context.Background()); !IsDegraded(err) {
		t.Fatalf("currentDataRefresh() error = %v, want degraded", err)
	}
	summary := singleJobStatus(t, orch, "current_data_refresh").LastSummary
	if summary["positions"] != 39 || summary["strategies"] != 39 || summary["watchlist"] != 0 || summary["selected"] != 78 {
		t.Fatalf("current scope summary = %#v", summary)
	}
	if summary["updated"] != 75 || summary["empty"] != 2 || summary["stale"] != 1 {
		t.Fatalf("current refresh coverage summary = %#v", summary)
	}
	if universeRepo.limit != 0 || len(universeRepo.limits) != 0 {
		t.Fatalf("current refresh queried watchlist with limits %v", universeRepo.limits)
	}
	if summary["batches"] != 8 {
		t.Fatalf("current refresh batches = %d, want 8", summary["batches"])
	}
	provider.mu.Lock()
	providerCalls, providerTime := provider.calls, provider.virtualTime
	provider.mu.Unlock()
	modeledDuration := providerTime + time.Duration(summary["batches"]-1)*150*time.Millisecond
	if providerCalls != 2*summary["selected"] || modeledDuration >= 30*time.Minute {
		t.Fatalf("current refresh provider calls = %d, modeled duration = %s; want %d calls before 30m hot cadence", providerCalls, modeledDuration, 2*summary["selected"])
	}
	if got := orch.getRefreshedTickers(); !reflect.DeepEqual(got, expected) {
		t.Fatalf("hot payload = %v, want exact fresh selection %v", got, expected)
	}
}

func TestCurrentDataRefreshExcludesWatchlistNonStockAndPausedSources(t *testing.T) {
	universeRepo := &operationalUniverseRepo{watchlist: []universe.TrackedTicker{{Ticker: "WATCHLIST_ONLY"}}}
	orch := NewJobOrchestrator(OrchestratorDeps{
		PositionRepo: newRecordingPositionRepo(
			&domain.Position{ID: uuid.New(), Ticker: "AAPL", MarketType: domain.MarketTypeStock},
			&domain.Position{ID: uuid.New(), Ticker: "KXTEST:YES", MarketType: domain.MarketTypeKalshi},
		),
		StrategyRepo: &kalshiStrategyRepoStub{strategies: []domain.Strategy{
			{ID: uuid.New(), Ticker: "PAUSED", Status: domain.StrategyStatusPaused, MarketType: domain.MarketTypeStock},
			{ID: uuid.New(), Ticker: "POLYMARKET", Status: domain.StrategyStatusActive, MarketType: domain.MarketTypePolymarket},
		}},
		Universe: universe.NewUniverse(universeRepo, nil, nil),
	})
	orch.Register("current_data_refresh", "test", currentDataRefreshSpec, orch.currentDataRefresh)

	if err := orch.currentDataRefresh(context.Background()); err == nil {
		t.Fatal("currentDataRefresh() error = nil, want missing data service error")
	}
	status := singleJobStatus(t, orch, "current_data_refresh")
	if got := status.LastSummary["tickers"]; got != 1 {
		t.Fatalf("refreshed ticker count = %d, want only the stock position", got)
	}
	if got := status.LastSummary["watchlist"]; got != 0 || len(universeRepo.limits) != 0 {
		t.Fatalf("watchlist count = %d, calls = %v; want omitted", got, universeRepo.limits)
	}
}

func TestHotScanRequiresAndConsumesCurrentRefreshState(t *testing.T) {
	repo := &operationalUniverseRepo{watchlist: []universe.TrackedTicker{{Ticker: "WRONG"}}}
	orch := NewJobOrchestrator(OrchestratorDeps{
		Universe:    universe.NewUniverse(repo, nil, nil),
		DataService: data.NewDataService(config.Config{}, nil, nil, nil, nil),
	})
	orch.Register("hot_scan", "test", hotScanSpec, orch.hotScan)
	if err := orch.hotScan(context.Background()); err == nil || !strings.Contains(err.Error(), "no fresh ticker selection") {
		t.Fatalf("hotScan() without refresh error = %v", err)
	}

	orch.setRefreshedTickers([]string{"AAPL", "MSFT"})
	if err := orch.hotScan(context.Background()); err == nil {
		t.Fatal("hotScan() with unavailable market data error = nil")
	}
	status := singleJobStatus(t, orch, "hot_scan")
	if got := status.LastSummary["selected"]; got != 2 {
		t.Fatalf("hot scan selected = %d, want exact refreshed set size 2", got)
	}
	if repo.limit != 0 {
		t.Fatalf("hot scan queried watchlist with limit %d", repo.limit)
	}
}

func TestMarketJobCadenceRunsDependenciesBeforeConsumers(t *testing.T) {
	t.Parallel()

	if currentDataRefreshSpec.Cron != "30 * * * 1-5" {
		t.Fatalf("current refresh cron = %q", currentDataRefreshSpec.Cron)
	}
	if currentDataRefreshSpec.Type != scheduler.ScheduleTypeMarketHours || currentDataRefreshSpec.PostCloseGraceMinutes != 30 {
		t.Fatalf("current refresh schedule = %+v", currentDataRefreshSpec)
	}
	for _, test := range []struct {
		now  time.Time
		want bool
	}{
		{now: time.Date(2026, time.August, 6, 15, 30, 0, 0, easternTime), want: true},
		{now: time.Date(2026, time.August, 6, 16, 30, 0, int(3*time.Millisecond), easternTime), want: true},
		{now: time.Date(2026, time.August, 6, 16, 31, 0, 0, easternTime), want: false},
		{now: time.Date(2026, time.August, 6, 17, 30, 0, 0, easternTime), want: false},
	} {
		if got := currentDataRefreshSpec.ShouldFire(test.now); got != test.want {
			t.Fatalf("current refresh ShouldFire(%s) = %t, want %t", test.now, got, test.want)
		}
	}
	if hotScanSpec.Cron != "0 * * * 1-5" {
		t.Fatalf("hot scan cron = %q, want minute 0", hotScanSpec.Cron)
	}
	if deepScanSpec.Cron != "25 * * * 1-5" {
		t.Fatalf("deep scan cron = %q, want minute 25", deepScanSpec.Cron)
	}
}

func TestMarketPipelineDependenciesRequireCurrentHourlyCycle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		consumer    string
		now         time.Time
		dependency  string
		completedAt time.Time
		wantBlocked bool
	}{
		{
			name: "opening hot scan accepts refresh at previous half-hour boundary", consumer: "hot_scan", dependency: "current_data_refresh",
			now: time.Date(2026, time.August, 6, 10, 0, 0, 0, easternTime), completedAt: time.Date(2026, time.August, 6, 9, 30, 0, 0, easternTime),
		},
		{
			name: "hot scan rejects refresh before previous half-hour boundary", consumer: "hot_scan", dependency: "current_data_refresh", wantBlocked: true,
			now: time.Date(2026, time.August, 6, 10, 0, 0, 0, easternTime), completedAt: time.Date(2026, time.August, 6, 9, 29, 59, 999999999, easternTime),
		},
		{
			name: "manual hot scan after half-hour accepts current boundary", consumer: "hot_scan", dependency: "current_data_refresh",
			now: time.Date(2026, time.August, 6, 10, 45, 0, 0, easternTime), completedAt: time.Date(2026, time.August, 6, 10, 30, 0, 0, easternTime),
		},
		{
			name: "deep scan accepts hot scan at current hour boundary", consumer: "deep_scan", dependency: "hot_scan",
			now: time.Date(2026, time.August, 6, 10, 25, 0, 0, easternTime), completedAt: time.Date(2026, time.August, 6, 10, 0, 0, 0, easternTime),
		},
		{
			name: "deep scan rejects hot scan before current hour boundary", consumer: "deep_scan", dependency: "hot_scan", wantBlocked: true,
			now: time.Date(2026, time.August, 6, 10, 25, 0, 0, easternTime), completedAt: time.Date(2026, time.August, 6, 9, 59, 59, 999999999, easternTime),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			orch := NewJobOrchestrator(OrchestratorDeps{})
			orch.Register(test.dependency, "dependency", currentDataRefreshSpec, func(context.Context) error { return nil })
			orch.Register(test.consumer, "consumer", hotScanSpec, func(context.Context) error { return nil }, test.dependency)
			dependency := orch.jobs[test.dependency]
			dependency.mu.Lock()
			dependency.LastRun = &test.completedAt
			dependency.LastResult = "success"
			dependency.mu.Unlock()
			if test.dependency == "current_data_refresh" {
				orch.setRefreshedTickers([]string{"AAPL"})
			}

			blockedBy, _ := orch.dependencyBlocker(orch.jobs[test.consumer], test.now)
			if got := blockedBy != ""; got != test.wantBlocked {
				t.Fatalf("dependency blocked = %t, want %t", got, test.wantBlocked)
			}
		})
	}
}

func TestCanonicalTriggeredStrategiesDeduplicatesSchedulerKeys(t *testing.T) {
	low := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	high := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	strategies := []domain.Strategy{
		{ID: high, Ticker: "AAPL", MarketType: domain.MarketTypeStock, ScheduleCron: "0 */2 * * *"},
		{ID: low, Ticker: "AAPL", MarketType: domain.MarketTypeStock, ScheduleCron: "0 */2 * * *"},
		{ID: uuid.New(), Ticker: "AAPL", MarketType: domain.MarketTypeStock, ScheduleCron: "0 9 * * 1-5"},
		{ID: uuid.New(), Ticker: "KX-A", MarketType: domain.MarketTypeKalshi, ScheduleCron: "0 */6 * * *"},
	}
	got := canonicalTriggeredStrategies(strategies)
	if len(got) != 2 {
		t.Fatalf("canonical strategies = %d, want 2 stock scheduler keys", len(got))
	}
	if got[0].ID != low {
		t.Fatalf("duplicate canonical ID = %s, want deterministic lowest %s", got[0].ID, low)
	}
}

func TestMarketScanCompletionErrorFailsVisibleOnPartialCoverage(t *testing.T) {
	t.Parallel()

	if err := marketScanCompletionError("deep_scan", map[string]int{}, 50); err == nil {
		t.Fatal("marketScanCompletionError(empty) = nil, want coverage error")
	}
	err := marketScanCompletionError("deep_scan", map[string]int{"selected": 10, "scored": 5, "fetch_errors": 1, "insufficient": 2, "stale": 2}, 50)
	if err == nil || !IsDegraded(err) || !strings.Contains(err.Error(), "coverage=50%") {
		t.Fatalf("marketScanCompletionError(partial) = %v", err)
	}
	if err := marketScanCompletionError("hot_scan", map[string]int{"selected": 10, "scored": 7, "insufficient": 3}, 80); err == nil || IsDegraded(err) {
		t.Fatalf("marketScanCompletionError(below floor) = %v, want true error", err)
	}
	if err := marketScanCompletionError("hot_scan", map[string]int{"selected": 10, "scored": 8, "insufficient": 2}, 80); !IsDegraded(err) {
		t.Fatalf("marketScanCompletionError(at floor) = %v, want degraded", err)
	}
	if err := marketScanCompletionError("hot_scan", map[string]int{"selected": 10, "scored": 10, "score_errors": 1}, 80); err == nil || IsDegraded(err) {
		t.Fatalf("marketScanCompletionError(score infrastructure) = %v, want true error", err)
	}
}

func TestMarketJobsFailWhenCoreDependenciesAreMissing(t *testing.T) {
	t.Parallel()

	orch := NewJobOrchestrator(OrchestratorDeps{})
	tests := map[string]func(context.Context) error{
		"current_data_refresh": orch.currentDataRefresh,
		"hot_scan":             orch.hotScan,
		"deep_scan":            orch.deepScan,
	}
	for name, run := range tests {
		name, run := name, run
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := run(context.Background()); err == nil {
				t.Fatalf("%s() error = nil, want missing dependency failure", name)
			}
		})
	}
}

func TestMarketBarFreshnessUsesRegularSessionAndTradingDate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 6, 10, 0, 0, 0, easternTime)
	if !intradayBarFresh(now, time.Date(2026, time.August, 6, 9, 45, 0, 0, easternTime)) {
		t.Fatal("15-minute-old same-session intraday bar should be fresh")
	}
	if intradayBarFresh(now, time.Date(2026, time.August, 6, 9, 35, 0, 0, easternTime)) {
		t.Fatal("25-minute-old intraday bar should be stale")
	}
	if intradayBarFresh(time.Date(2026, time.August, 6, 9, 20, 0, 0, easternTime), time.Date(2026, time.August, 5, 15, 55, 0, 0, easternTime)) {
		t.Fatal("premarket bar must not qualify for a regular-session refresh")
	}
	if dailyBarFresh(now, time.Date(2026, time.August, 6, 9, 30, 0, 0, easternTime)) {
		t.Fatal("incomplete current-session daily bar must not be fresh during market hours")
	}
	if !dailyBarFresh(now, time.Date(2026, time.August, 5, 9, 30, 0, 0, easternTime)) {
		t.Fatal("prior completed-session daily bar should be fresh during market hours")
	}
	preMarketMonday := time.Date(2026, time.August, 10, 8, 0, 0, 0, easternTime)
	if !dailyBarFresh(preMarketMonday, time.Date(2026, time.August, 7, 9, 30, 0, 0, easternTime)) {
		t.Fatal("Friday daily bar should be fresh before Monday open")
	}
	postClose := time.Date(2026, time.August, 6, 16, 5, 0, 0, easternTime)
	if !dailyBarFresh(postClose, time.Date(2026, time.August, 6, 16, 0, 0, 0, easternTime)) {
		t.Fatal("completed current-session daily bar should be fresh after close")
	}
	series := []domain.OHLCV{
		{Timestamp: time.Date(2026, time.August, 4, 16, 0, 0, 0, easternTime)},
		{Timestamp: time.Date(2026, time.August, 5, 16, 0, 0, 0, easternTime)},
		{Timestamp: time.Date(2026, time.August, 6, 10, 0, 0, 0, easternTime)},
	}
	completed := completedDailyBars(now, series)
	if len(completed) != 2 || !sameMarketDate(completed[1].Timestamp.In(easternTime), time.Date(2026, time.August, 5, 0, 0, 0, 0, easternTime)) {
		t.Fatalf("completed daily bars = %#v, want through prior session only", completed)
	}
	if !dailySeriesFresh(now, series) {
		t.Fatal("series containing a fresh completed bar plus a provisional candle should be fresh")
	}
}

func TestCurrentDataRefreshCompletionErrorUsesIntradayCoverage(t *testing.T) {
	t.Parallel()

	if err := currentDataRefreshCompletionError(map[string]int{}); err != nil {
		t.Fatalf("currentDataRefreshCompletionError(empty) = %v, want nil", err)
	}
	if err := currentDataRefreshCompletionError(map[string]int{"tickers": 10, "updated": 8, "cache_only": 2, "daily_stale": 1}); !IsDegraded(err) {
		t.Fatalf("currentDataRefreshCompletionError(at floor) = %v, want degraded", err)
	}
	err := currentDataRefreshCompletionError(map[string]int{"tickers": 10, "updated": 7, "cache_only": 3, "daily_updated": 10})
	if err == nil || IsDegraded(err) || !strings.Contains(err.Error(), "coverage=70%") {
		t.Fatalf("currentDataRefreshCompletionError(below floor with daily success) = %v, want true error", err)
	}
	err = currentDataRefreshCompletionError(map[string]int{"tickers": 4, "daily_updated": 4, "cache_only": 2, "daily_stale": 1})
	if err == nil || !strings.Contains(err.Error(), "cache_only=2") || !strings.Contains(err.Error(), "stale=1") {
		t.Fatalf("currentDataRefreshCompletionError(daily only) = %v", err)
	}
	if err := currentDataRefreshCompletionError(map[string]int{"tickers": 4, "updated": 4, "errors": 1}); err == nil || IsDegraded(err) {
		t.Fatalf("currentDataRefreshCompletionError(systemic) = %v, want true error", err)
	}
}

func TestCurrentDataRefreshClosingModeBoundaries(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{name: "before close", at: time.Date(2026, time.August, 26, 15, 59, 59, 0, easternTime)},
		{name: "at close", at: time.Date(2026, time.August, 26, 16, 0, 0, 0, easternTime), want: true},
		{name: "last grace minute", at: time.Date(2026, time.August, 26, 16, 30, 59, 0, easternTime), want: true},
		{name: "after grace", at: time.Date(2026, time.August, 26, 16, 31, 0, 0, easternTime)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := currentDataRefreshClosingMode(test.at); got != test.want {
				t.Fatalf("currentDataRefreshClosingMode(%v) = %t, want %t", test.at, got, test.want)
			}
		})
	}
}

func TestCurrentDataRefreshClosingCompletionPolicy(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		summary  map[string]int
		wantKind string
	}{
		{name: "no proof", summary: map[string]int{"closing_mode": 1, "selected": 10}, wantKind: "error"},
		{name: "below floor", summary: map[string]int{"closing_mode": 1, "selected": 10, "daily_closing_updated": 7}, wantKind: "error"},
		{name: "exact floor", summary: map[string]int{"closing_mode": 1, "selected": 10, "daily_closing_updated": 8, "daily_empty": 2}, wantKind: "degraded"},
		{name: "full proof ignores intraday findings", summary: map[string]int{"closing_mode": 1, "selected": 10, "daily_closing_updated": 10, "provider_failures": 4, "empty": 3, "stale": 2}},
		{name: "daily systemic error", summary: map[string]int{"closing_mode": 1, "selected": 10, "daily_closing_updated": 10, "daily_errors": 1, "errors": 1}, wantKind: "error"},
		{name: "intraday systemic error", summary: map[string]int{"closing_mode": 1, "selected": 10, "daily_closing_updated": 10, "intraday_errors": 1, "errors": 1}, wantKind: "error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := currentDataRefreshCompletionError(test.summary)
			switch test.wantKind {
			case "error":
				if err == nil || IsDegraded(err) {
					t.Fatalf("completion error = %v, want true error", err)
				}
			case "degraded":
				if !IsDegraded(err) {
					t.Fatalf("completion error = %v, want degraded", err)
				}
			default:
				if err != nil {
					t.Fatalf("completion error = %v, want nil", err)
				}
			}
		})
	}
}

func TestClosingDailyProviderProvenRequiresCurrentProviderEvidence(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, time.August, 26, 16, 0, 0, 0, easternTime)
	for _, test := range []struct {
		name          string
		requests      int
		freshBars     int
		latest        time.Time
		latestPresent bool
		want          bool
	}{
		{name: "no request", freshBars: 1, latest: start, latestPresent: true},
		{name: "no fresh bars", requests: 1, latest: start, latestPresent: true},
		{name: "no latest timestamp", requests: 1, freshBars: 1},
		{name: "prior session", requests: 1, freshBars: 1, latest: start.AddDate(0, 0, -1), latestPresent: true},
		{name: "admitted session", requests: 1, freshBars: 1, latest: start, latestPresent: true, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := closingDailyProviderProven(start, test.requests, test.freshBars, test.latest, test.latestPresent); got != test.want {
				t.Fatalf("closingDailyProviderProven() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestCurrentDataRefreshHandsOffExactDegradedSelection(t *testing.T) {
	orch := NewJobOrchestrator(OrchestratorDeps{})
	err := orch.completeCurrentDataRefresh(map[string]int{"selected": 10, "updated": 8, "empty": 2}, []string{"AAPL", "MSFT"})
	if !IsDegraded(err) {
		t.Fatalf("completeCurrentDataRefresh() = %v, want degraded", err)
	}
	if got := orch.getRefreshedTickers(); len(got) != 2 || got[0] != "AAPL" || got[1] != "MSFT" {
		t.Fatalf("refreshed tickers = %v, want exact degraded selection", got)
	}
	orch.setRefreshedTickers([]string{"KEEP"})
	err = orch.completeCurrentDataRefresh(map[string]int{"selected": 10, "updated": 7, "empty": 3}, []string{"DROP"})
	if err == nil || IsDegraded(err) {
		t.Fatalf("completeCurrentDataRefresh() = %v, want true error", err)
	}
	if got := orch.getRefreshedTickers(); len(got) != 0 {
		t.Fatalf("refreshed tickers after true error = %v, want cleared payload", got)
	}
	orch.setRefreshedTickers([]string{"KEEP"})
	err = orch.completeCurrentDataRefresh(map[string]int{"closing_mode": 1, "selected": 1, "daily_closing_updated": 1}, []string{"DROP"})
	if err != nil {
		t.Fatalf("closing completeCurrentDataRefresh() = %v", err)
	}
	if got := orch.getRefreshedTickers(); len(got) != 0 {
		t.Fatalf("closing refreshed tickers = %v, want no payload", got)
	}
}
