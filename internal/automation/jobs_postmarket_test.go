package automation

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/discovery"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/universe"
)

func TestEasternDayStartUTCUsesTradingDayAcrossUTCMidnight(t *testing.T) {
	got := easternDayStartUTC(time.Date(2026, time.August, 6, 0, 30, 0, 0, time.UTC))
	want := time.Date(2026, time.August, 5, 4, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("easternDayStartUTC() = %s, want %s", got, want)
	}
}

func TestPostMarketCompletionErrorsExposePartialCoverage(t *testing.T) {
	t.Parallel()

	if err := strategyResweepCompletionError(map[string]int{"supported": 107, "swept": 105, "failed": 2, "stale": 2}); err == nil || !IsDegraded(err) || !strings.Contains(err.Error(), "coverage_bps=9813") || !strings.Contains(err.Error(), "stale=2") {
		t.Fatalf("strategyResweepCompletionError(live) = %v, want detailed degraded result", err)
	}
	if err := strategyResweepCompletionError(map[string]int{"supported": 100, "swept": 79}); err == nil || IsDegraded(err) {
		t.Fatalf("strategyResweepCompletionError(79%%) = %v, want true error", err)
	}
	if err := strategyResweepCompletionError(map[string]int{"supported": 100, "swept": 80, "failed": 20, "config_failed": 20}); err == nil || !IsDegraded(err) {
		t.Fatalf("strategyResweepCompletionError(80%%) = %v, want degraded", err)
	}
	if err := strategyResweepCompletionError(map[string]int{"supported": 5, "failed": 5, "config_failed": 5}); err == nil || IsDegraded(err) {
		t.Fatalf("strategyResweepCompletionError(all invalid) = %v, want true error", err)
	}
	if err := strategyResweepCompletionError(map[string]int{"supported": 100, "swept": 100}); err != nil {
		t.Fatalf("strategyResweepCompletionError(complete) = %v, want nil", err)
	}

	if err := optionsScanCompletionError(map[string]int{"universe": 100, "optionable": 24, "chains": 24}); err == nil || IsDegraded(err) {
		t.Fatalf("optionsScanCompletionError(24%%) = %v, want true error", err)
	}
	if err := optionsScanCompletionError(map[string]int{"universe": 100, "optionable": 25, "chains": 20}); err == nil || !IsDegraded(err) {
		t.Fatalf("optionsScanCompletionError(exact floors) = %v, want degraded", err)
	}
	if err := optionsScanCompletionError(map[string]int{"universe": 100, "optionable": 27, "chains": 22, "price_empty": 73, "fetch_failed": 5}); err == nil || !IsDegraded(err) || !strings.Contains(err.Error(), "optionable_coverage_bps=2700") || !strings.Contains(err.Error(), "chain_coverage_bps=8148") {
		t.Fatalf("optionsScanCompletionError(live) = %v, want detailed degraded result", err)
	}
	if err := optionsScanCompletionError(map[string]int{"universe": 100, "optionable": 25, "chains": 19}); err == nil || IsDegraded(err) {
		t.Fatalf("optionsScanCompletionError(under chain floor) = %v, want true error", err)
	}
	if err := optionsScanCompletionError(map[string]int{"universe": 100, "optionable": 100, "chains": 100, "persist_failed": 1}); err == nil || IsDegraded(err) {
		t.Fatalf("optionsScanCompletionError(persistence) = %v, want true error", err)
	}
	if err := optionsScanCompletionError(map[string]int{"universe": 100, "optionable": 100, "chains": 100}); err != nil {
		t.Fatalf("optionsScanCompletionError(complete) = %v, want nil", err)
	}
}

func TestDailyReviewCompletionErrorOnlyRejectsIncompleteEvidence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		summary     map[string]int
		wantErr     bool
		wantMessage string
	}{
		{name: "failed only succeeds", summary: map[string]int{"failed": 2}},
		{name: "query errors fail", summary: map[string]int{"query_errors": 1}, wantErr: true, wantMessage: "query_errors=1"},
		{name: "running fails", summary: map[string]int{"running": 1}, wantErr: true, wantMessage: "running=1"},
		{name: "completed without signal fails", summary: map[string]int{"completed_without_signal": 1}, wantErr: true, wantMessage: "completed_without_signal=1"},
		{
			name:        "failed plus incomplete evidence fails",
			summary:     map[string]int{"failed": 2, "running": 1},
			wantErr:     true,
			wantMessage: "failed=2 running=1",
		},
		{name: "zero succeeds", summary: map[string]int{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := dailyReviewCompletionError(tt.summary)
			if (err != nil) != tt.wantErr {
				t.Fatalf("dailyReviewCompletionError(%v) = %v, wantErr %v", tt.summary, err, tt.wantErr)
			}
			if tt.wantMessage != "" && !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("dailyReviewCompletionError(%v) = %q, want substring %q", tt.summary, err, tt.wantMessage)
			}
		})
	}
}

func TestClassifyResweepScoresSeparatesUnqualifiedSentinelsFromInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		results []discovery.SweepResult
		state   string
		wantErr bool
	}{
		{
			name:    "comparable",
			results: []discovery.SweepResult{{Label: "variant_1", Score: 2}, {Label: "base", Score: 1}},
			state:   "comparable",
		},
		{
			name:    "base unqualified",
			results: []discovery.SweepResult{{Label: "variant_1", Score: 2}, {Label: "base", Score: math.Inf(-1)}},
			state:   "base_unqualified",
		},
		{
			name:    "all unqualified",
			results: []discovery.SweepResult{{Label: "base", Score: math.Inf(-1)}, {Label: "variant_1", Score: math.Inf(-1)}},
			state:   "all_unqualified",
		},
		{
			name:    "missing base",
			results: []discovery.SweepResult{{Label: "variant_1", Score: 2}},
			state:   "missing_base",
			wantErr: true,
		},
		{
			name:    "nan base",
			results: []discovery.SweepResult{{Label: "variant_1", Score: 2}, {Label: "base", Score: math.NaN()}},
			state:   "invalid_scores",
			wantErr: true,
		},
		{
			name:    "positive infinite best",
			results: []discovery.SweepResult{{Label: "variant_1", Score: math.Inf(1)}, {Label: "base", Score: 1}},
			state:   "invalid_scores",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, state, err := classifyResweepScores(tt.results)
			if state != tt.state {
				t.Fatalf("state = %q, want %q", state, tt.state)
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestOptionsScanTickersNormalizesDeduplicatesAndCapsWatchlist(t *testing.T) {
	t.Parallel()

	watchlist := []universe.TrackedTicker{{Ticker: " aapl "}, {Ticker: "AAPL"}, {Ticker: ""}}
	for i := 0; i < optionsScanWatchlistLimit+5; i++ {
		watchlist = append(watchlist, universe.TrackedTicker{Ticker: fmt.Sprintf("t%03d", i)})
	}

	got := optionsScanTickers(watchlist)
	if len(got) != optionsScanWatchlistLimit {
		t.Fatalf("ticker count = %d, want %d", len(got), optionsScanWatchlistLimit)
	}
	if got[0] != "AAPL" || got[1] != "T000" || got[len(got)-1] != "T098" {
		t.Fatalf("normalized capped tickers = %#v", got)
	}
}

func TestSummarizePipelineRunsSeparatesStatusFromDecision(t *testing.T) {
	t.Parallel()

	got := summarizePipelineRuns([]domain.PipelineRun{
		{Status: domain.PipelineStatusCompleted, Signal: domain.PipelineSignalBuy},
		{Status: domain.PipelineStatusCompleted, Signal: domain.PipelineSignalHold},
		{Status: domain.PipelineStatusCompleted},
		{Status: domain.PipelineStatusFailed, Signal: domain.PipelineSignalHold},
		{Status: domain.PipelineStatusRunning},
	})

	want := map[string]int{
		"runs": 5, "completed": 3, "failed": 1, "running": 1,
		"buy": 1, "hold": 1, "completed_without_signal": 1,
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("summary[%q] = %d, want %d (summary=%v)", key, got[key], value, got)
		}
	}
	if got["sell"] != 0 {
		t.Fatalf("summary[sell] = %d, want 0", got["sell"])
	}
}
