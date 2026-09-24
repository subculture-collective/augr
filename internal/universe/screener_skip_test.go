package universe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data/polygon"
)

func runScreenWithSnapshots(t *testing.T, snapshots []polygon.TickerSnapshot, tickers []string, maxSkipped float64) ([]ScoredTicker, error) {
	t.Helper()
	testNow := time.Date(2026, time.August, 6, 10, 30, 0, 0, mustEastern(t))
	body, _ := json.Marshal(map[string]any{"tickers": snapshots})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	client := polygon.NewClient("test-key", discardLogger())
	polygon.SetBaseURLForTest(client, server.URL)

	tracked := make([]TrackedTicker, 0, len(tickers))
	for _, ticker := range tickers {
		tracked = append(tracked, TrackedTicker{Ticker: ticker, Name: ticker, Active: true, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	}
	cfg := DefaultPreMarketConfig()
	cfg.TopN = 10
	cfg.MaxSkippedFraction = maxSkipped
	cfg.now = func() time.Time { return testNow }
	return RunPreMarketScreen(context.Background(), client, newMockRepo(tracked), cfg, discardLogger())
}

func liquidSnapshot(t *testing.T, ticker string, updated time.Time) polygon.TickerSnapshot {
	t.Helper()
	return polygon.TickerSnapshot{
		Ticker:          ticker,
		TodaysChangePct: 1.0,
		Day:             polygon.SnapshotBarForTest(100, 102, 99, 101, 5_000_000, 100.5),
		PrevDay:         polygon.SnapshotBarForTest(99, 100, 98, 100, 4_000_000, 99.5),
		Updated:         updated.UnixNano(),
	}
}

func TestRunPreMarketScreenSkipsMinorityOfBadTickers(t *testing.T) {
	t.Parallel()
	fresh := time.Date(2026, time.August, 6, 10, 30, 0, 0, mustEastern(t))
	stale := fresh.Add(-48 * time.Hour)
	tickers := []string{"AAA", "BBB", "CCC", "DDD", "EEE"}
	snapshots := []polygon.TickerSnapshot{
		liquidSnapshot(t, "AAA", fresh),
		liquidSnapshot(t, "BBB", fresh),
		liquidSnapshot(t, "CCC", fresh),
		liquidSnapshot(t, "DDD", fresh),
		liquidSnapshot(t, "EEE", stale), // stale: skipped
		liquidSnapshot(t, "ZZZ", fresh), // unexpected: skipped, not counted against requested
	}
	results, err := runScreenWithSnapshots(t, snapshots, tickers, 0)
	if err != nil {
		t.Fatalf("RunPreMarketScreen() error = %v, want stale minority skipped", err)
	}
	for _, result := range results {
		if result.Ticker == "EEE" || result.Ticker == "ZZZ" {
			t.Fatalf("skipped ticker %s appeared in results", result.Ticker)
		}
	}
	if len(results) != 4 {
		t.Fatalf("results = %d, want 4", len(results))
	}
}

func TestRunPreMarketScreenFailsWhenTooManyTickersSkipped(t *testing.T) {
	t.Parallel()
	fresh := time.Date(2026, time.August, 6, 10, 30, 0, 0, mustEastern(t))
	stale := fresh.Add(-48 * time.Hour)
	tickers := []string{"AAA", "BBB", "CCC", "DDD"}
	snapshots := []polygon.TickerSnapshot{
		liquidSnapshot(t, "AAA", fresh),
		liquidSnapshot(t, "BBB", fresh),
		liquidSnapshot(t, "CCC", stale),
		// DDD missing entirely.
	}
	_, err := runScreenWithSnapshots(t, snapshots, tickers, 0.25)
	if err == nil || !strings.Contains(err.Error(), "skipped") || !strings.Contains(err.Error(), "stale=1") || !strings.Contains(err.Error(), "missing=1") {
		t.Fatalf("error = %v, want skipped-fraction failure naming stale and missing counts", err)
	}

	// A more permissive threshold accepts the same payload.
	if _, err := runScreenWithSnapshots(t, snapshots, tickers, 0.5); err != nil {
		t.Fatalf("RunPreMarketScreen() with 50%% tolerance error = %v", err)
	}
}
