package automation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type filingStrategyRepo struct{ *kalshiStrategyRepoStub }

func (s *filingStrategyRepo) Count(context.Context, repository.StrategyFilter) (int, error) {
	return len(s.strategies), nil
}

type filingEventsProviderStub struct {
	calls   int
	failAt  int
	err     error
	filings []domain.SECFiling
}

func (s *filingEventsProviderStub) GetEarningsCalendar(context.Context, time.Time, time.Time) ([]domain.EarningsEvent, error) {
	return nil, nil
}

func TestFilingMonitorFailsWhenAnalysisProviderIsMissing(t *testing.T) {
	t.Parallel()

	provider := &filingEventsProviderStub{failAt: -1, filings: []domain.SECFiling{{Symbol: "AAPL", Form: "8-K", URL: "https://example.invalid/filing"}}}
	orch := NewJobOrchestrator(OrchestratorDeps{
		EventsProvider: provider,
		StrategyRepo: &filingStrategyRepo{&kalshiStrategyRepoStub{strategies: []domain.Strategy{
			{Ticker: "AAPL", MarketType: domain.MarketTypeStock, Status: domain.StrategyStatusActive},
		}}},
	})
	orch.Register("filing_monitor", "test", schedulerSpecEveryMinute(), orch.filingMonitor)

	err := orch.filingMonitor(context.Background())
	if err == nil || !strings.Contains(err.Error(), "analyses failed") {
		t.Fatalf("filingMonitor() error = %v, want analysis failure", err)
	}
	if got := orch.jobs["filing_monitor"].LastSummary["analysis_errors"]; got != 2 {
		t.Fatalf("analysis_errors = %d, want 2", got)
	}
}

func (s *filingEventsProviderStub) GetNextEarnings(context.Context, string) (*domain.EarningsEvent, error) {
	return nil, nil
}

func (s *filingEventsProviderStub) GetFilings(context.Context, string, string, time.Time, time.Time) ([]domain.SECFiling, error) {
	s.calls++
	failAt := s.failAt
	if failAt == 0 {
		failAt = 3
	}
	if s.calls == failAt {
		if s.err != nil {
			return nil, s.err
		}
		return nil, filingRateLimitError{}
	}
	return s.filings, nil
}

func TestFilingMonitorFailsPartialNonRateLimitedCoverage(t *testing.T) {
	provider := &filingEventsProviderStub{failAt: 2, err: errors.New("provider unavailable")}
	orch := NewJobOrchestrator(OrchestratorDeps{
		EventsProvider: provider,
		StrategyRepo: &filingStrategyRepo{&kalshiStrategyRepoStub{strategies: []domain.Strategy{
			{Ticker: "AAPL", MarketType: domain.MarketTypeStock, Status: domain.StrategyStatusActive},
			{Ticker: "MSFT", MarketType: domain.MarketTypeStock, Status: domain.StrategyStatusActive},
		}}},
	})
	orch.Register("filing_monitor", "test", schedulerSpecEveryMinute(), orch.filingMonitor)

	err := orch.filingMonitor(context.Background())
	if err == nil || !strings.Contains(err.Error(), "1 provider requests failed") {
		t.Fatalf("filingMonitor() error = %v, want partial-coverage error", err)
	}

	got := orch.jobs["filing_monitor"].LastSummary
	want := map[string]int{
		"available": 2, "tickers_attempted": 2, "tickers_checked": 1,
		"filings_found": 0, "rate_limited": 0, "request_errors": 1,
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("summary[%q] = %d, want %d (summary=%v)", key, got[key], value, got)
		}
	}
}

func TestFilingMonitorRetriesOneTransientProviderFailure(t *testing.T) {
	provider := &filingEventsProviderStub{failAt: 1, err: filingStatusError{status: 503}}
	orch := NewJobOrchestrator(OrchestratorDeps{
		EventsProvider: provider,
		StrategyRepo: &filingStrategyRepo{&kalshiStrategyRepoStub{strategies: []domain.Strategy{
			{Ticker: "SPY", MarketType: domain.MarketTypeStock, Status: domain.StrategyStatusActive},
		}}},
	})
	orch.Register("filing_monitor", "test", schedulerSpecEveryMinute(), orch.filingMonitor)

	if err := orch.filingMonitor(context.Background()); err != nil {
		t.Fatalf("filingMonitor() error = %v, want transient recovery", err)
	}
	if provider.calls != 3 {
		t.Fatalf("provider calls = %d, want initial request, retry, and second form", provider.calls)
	}
	got := orch.jobs["filing_monitor"].LastSummary
	if got["request_retries"] != 1 || got["request_errors"] != 0 || got["tickers_checked"] != 1 {
		t.Fatalf("summary = %#v, want one recovered retry and complete ticker", got)
	}
}

func (s *filingEventsProviderStub) GetEconomicCalendar(context.Context) ([]domain.EconomicEvent, error) {
	return nil, nil
}

func (s *filingEventsProviderStub) GetIPOCalendar(context.Context, time.Time, time.Time) ([]domain.IPOEvent, error) {
	return nil, nil
}

type filingRateLimitError struct{}

func (filingRateLimitError) Error() string   { return "provider quota exhausted" }
func (filingRateLimitError) StatusCode() int { return 429 }

type filingStatusError struct{ status int }

func (e filingStatusError) Error() string   { return "provider unavailable" }
func (e filingStatusError) StatusCode() int { return e.status }

func TestFilingMonitorDistinguishesAttemptedAndCompletedTickers(t *testing.T) {
	provider := &filingEventsProviderStub{}
	orch := NewJobOrchestrator(OrchestratorDeps{
		EventsProvider: provider,
		StrategyRepo: &filingStrategyRepo{&kalshiStrategyRepoStub{strategies: []domain.Strategy{
			{Ticker: "AAPL", MarketType: domain.MarketTypeStock, Status: domain.StrategyStatusActive},
			{Ticker: "MSFT", MarketType: domain.MarketTypeStock, Status: domain.StrategyStatusActive},
		}}},
	})
	orch.Register("filing_monitor", "test", schedulerSpecEveryMinute(), orch.filingMonitor)

	err := orch.filingMonitor(context.Background())
	if err == nil || !isFilingProviderRateLimited(err) {
		t.Fatalf("filingMonitor() error = %v, want rate-limit error", err)
	}
	if !errors.As(err, new(filingStatusCoder)) {
		// The job wraps the provider error; this guards typed-error preservation.
		t.Fatalf("filingMonitor() error = %v, want wrapped status coder", err)
	}

	got := orch.jobs["filing_monitor"].LastSummary
	want := map[string]int{
		"available": 2, "tickers_attempted": 2, "tickers_checked": 1,
		"filings_found": 0, "rate_limited": 1,
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("summary[%q] = %d, want %d (summary=%v)", key, got[key], value, got)
		}
	}
}
