package automation

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/config"
	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/universe"
)

type partialResultProvider struct {
	mu    sync.Mutex
	calls []string
}

func (p *partialResultProvider) GetOHLCV(_ context.Context, ticker string, timeframe data.Timeframe, _, to time.Time) ([]domain.OHLCV, error) {
	p.mu.Lock()
	p.calls = append(p.calls, ticker+":"+timeframe.String())
	p.mu.Unlock()
	if ticker == "FAIL" {
		return nil, errors.New("provider failed")
	}
	if ticker == "CANCEL" {
		return nil, context.Canceled
	}
	timestamp := to
	if timeframe == data.Timeframe1d {
		timestamp = expectedCompletedNYSESession(to)
	}
	return []domain.OHLCV{{Timestamp: timestamp, Open: 1, High: 1, Low: 1, Close: 1, Volume: 1}}, nil
}

func (p *partialResultProvider) GetFundamentals(context.Context, string) (data.Fundamentals, error) {
	return data.Fundamentals{}, data.ErrNotImplemented
}

func (p *partialResultProvider) GetNews(context.Context, string, time.Time, time.Time) ([]data.NewsArticle, error) {
	return nil, data.ErrNotImplemented
}

func (p *partialResultProvider) GetSocialSentiment(context.Context, string, time.Time, time.Time) ([]data.SocialSentiment, error) {
	return nil, data.ErrNotImplemented
}

func (p *partialResultProvider) called(ticker, timeframe string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Contains(p.calls, ticker+":"+timeframe)
}

type partialResultHistoryRepo struct {
	mu   sync.Mutex
	bars []domain.HistoricalOHLCV
}

func (r *partialResultHistoryRepo) Get(context.Context, repository.MarketDataCacheKey) (*domain.MarketData, error) {
	return nil, repository.ErrNotFound
}

func (r *partialResultHistoryRepo) Set(context.Context, *domain.MarketData) error { return nil }

func (r *partialResultHistoryRepo) Expire(context.Context, repository.MarketDataCacheExpireFilter) error {
	return nil
}

func (r *partialResultHistoryRepo) UpsertHistoricalOHLCV(_ context.Context, bars []domain.HistoricalOHLCV) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bars = append(r.bars, bars...)
	return nil
}

func (r *partialResultHistoryRepo) ListHistoricalOHLCV(_ context.Context, filter repository.HistoricalOHLCVFilter) ([]domain.HistoricalOHLCV, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []domain.HistoricalOHLCV
	for _, bar := range r.bars {
		if bar.Ticker == filter.Ticker && bar.Provider == filter.Provider && bar.Timeframe == filter.Timeframe && !bar.Timestamp.Before(filter.From) && !bar.Timestamp.After(filter.To) {
			result = append(result, bar)
		}
	}
	return result, nil
}

func (r *partialResultHistoryRepo) UpsertHistoricalOHLCVCoverage(context.Context, domain.HistoricalOHLCVCoverage) error {
	return nil
}

func (r *partialResultHistoryRepo) ListHistoricalOHLCVCoverage(context.Context, repository.HistoricalOHLCVCoverageFilter) ([]domain.HistoricalOHLCVCoverage, error) {
	return nil, nil
}

func partialResultDataService(provider *partialResultProvider, repo repository.MarketDataCacheRepository) *data.DataService {
	registry := data.NewProviderRegistry()
	registry.Yahoo = func(data.ProviderConfig) data.DataProvider { return provider }
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return data.NewDataService(config.Config{}, registry, repo, logger, nil)
}

func partialResultOrchestrator(tickers []string, service *data.DataService) *JobOrchestrator {
	tracked := make([]universe.TrackedTicker, 0, len(tickers))
	for _, ticker := range tickers {
		tracked = append(tracked, universe.TrackedTicker{Ticker: ticker})
	}
	return NewJobOrchestrator(OrchestratorDeps{
		PositionRepo: newRecordingPositionRepo(),
		StrategyRepo: &kalshiStrategyRepoStub{},
		Universe:     universe.NewUniverse(&operationalUniverseRepo{watchlist: tracked}, nil, nil),
		DataService:  service,
	})
}

func TestCurrentDataRefreshConsumesLivePartialStatsWithoutSystemicError(t *testing.T) {
	provider := &partialResultProvider{}
	orch := partialResultOrchestrator([]string{"AAPL", "FAIL"}, partialResultDataService(provider, &partialResultHistoryRepo{}))
	orch.Register("current_data_refresh", "test", currentDataRefreshSpec, orch.currentDataRefresh)

	_ = orch.currentDataRefresh(context.Background())
	summary := singleJobStatus(t, orch, "current_data_refresh").LastSummary
	if summary["errors"] != 0 || summary["provider_failures"] != 1 || summary["daily_provider_failures"] != 1 {
		t.Fatalf("partial summary = %#v, want provider findings without systemic errors", summary)
	}
}

func TestCurrentDataRefreshNilStatsAreSystemic(t *testing.T) {
	orch := partialResultOrchestrator([]string{"AAPL"}, data.NewDataService(config.Config{}, nil, nil, nil, nil))
	orch.Register("current_data_refresh", "test", currentDataRefreshSpec, orch.currentDataRefresh)

	err := orch.currentDataRefresh(context.Background())
	if err == nil || IsDegraded(err) {
		t.Fatalf("currentDataRefresh() = %v, want true error", err)
	}
	if got := singleJobStatus(t, orch, "current_data_refresh").LastSummary["errors"]; got != 2 {
		t.Fatalf("systemic errors = %d, want one for each nil timeframe result", got)
	}
}

func TestHistoryRefreshConsumesPartialStatsAndContinuesBatches(t *testing.T) {
	provider := &partialResultProvider{}
	tickers := []string{"A01", "A02", "A03", "A04", "A05", "FAIL", "A07", "A08", "A09", "A10", "LATER"}
	orch := partialResultOrchestrator(tickers, partialResultDataService(provider, &partialResultHistoryRepo{}))
	orch.Register("history_refresh", "test", historyRefreshSpec, orch.historyRefresh)

	err := orch.historyRefresh(context.Background())
	if !IsDegraded(err) {
		t.Fatalf("historyRefresh() = %v, want degraded partial coverage", err)
	}
	summary := singleJobStatus(t, orch, "history_refresh").LastSummary
	if summary["updated"] != 10 || summary["failed"] != 1 || summary["provider_failures"] != 1 || summary["batches"] != 2 {
		t.Fatalf("partial history summary = %#v", summary)
	}
	if !provider.called("LATER", data.Timeframe1d.String()) {
		t.Fatal("later batch was not attempted after partial result")
	}
}

func TestHistoryRefreshNilResultIsTerminal(t *testing.T) {
	provider := &partialResultProvider{}
	orch := partialResultOrchestrator([]string{"AAPL", "MSFT"}, partialResultDataService(provider, nil))
	orch.Register("history_refresh", "test", historyRefreshSpec, orch.historyRefresh)

	err := orch.historyRefresh(context.Background())
	if err == nil || IsDegraded(err) {
		t.Fatalf("historyRefresh() = %v, want systemic true error", err)
	}
	summary := singleJobStatus(t, orch, "history_refresh").LastSummary
	if summary["failed"] != 2 || summary["batches"] != 1 {
		t.Fatalf("nil-result history summary = %#v", summary)
	}
	if provider.called("AAPL", data.Timeframe1d.String()) {
		t.Fatal("provider called despite unavailable historical repository")
	}
}

func TestHistoryRefreshContextCancellationStopsLaterBatches(t *testing.T) {
	provider := &partialResultProvider{}
	tickers := []string{"A01", "A02", "A03", "A04", "A05", "CANCEL", "A07", "A08", "A09", "A10", "LATER"}
	orch := partialResultOrchestrator(tickers, partialResultDataService(provider, &partialResultHistoryRepo{}))
	orch.Register("history_refresh", "test", historyRefreshSpec, orch.historyRefresh)

	err := orch.historyRefresh(context.Background())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("historyRefresh() = %v, want context cancellation", err)
	}
	if provider.called("LATER", data.Timeframe1d.String()) {
		t.Fatal("later batch attempted after context cancellation")
	}
}
