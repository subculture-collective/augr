package automation

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

type dailyFallbackStub struct {
	calls int
	get   func(context.Context, string, time.Time, time.Time) (data.ExactHistoricalResult, error)
}

func (s *dailyFallbackStub) GetDailyBars(ctx context.Context, ticker string, from, to time.Time) (data.ExactHistoricalResult, error) {
	s.calls++
	return s.get(ctx, ticker, from, to)
}

func dailyExactFixture(bars []domain.OHLCV) data.ExactHistoricalResult {
	r := data.ExactHistoricalResult{
		Receipt: data.HistoricalFetchReceipt{Provider: "alpaca", Feed: "sip", AdjustmentPolicy: "split", Pages: 1, Entitled: true, PaginationComplete: true},
		Pages:   []data.HistoricalSourcePage{{Body: []byte(`{"synthetic":true}`)}},
	}
	for _, b := range bars {
		price := fmt.Sprint(b.Close)
		r.Bars = append(r.Bars, data.ExactHistoricalBar{Timestamp: b.Timestamp, Open: price, High: price, Low: price, Close: price, Volume: fmt.Sprint(b.Volume)})
	}
	return r
}

func TestDeepScanSIPFallbackReceipts(t *testing.T) {
	for _, pages := range []int{0, 1} {
		t.Run(fmt.Sprint(pages), func(t *testing.T) {
			now := time.Date(2026, 9, 10, 16, 25, 0, 0, easternTime)
			primary := &partialResultProvider{ohlcv: func(string, time.Time, time.Time) ([]domain.OHLCV, error) {
				return []domain.OHLCV{{Timestamp: now.AddDate(0, 0, -7), Close: .1, Volume: 9999}}, nil
			}}
			orch := partialResultOrchestrator([]string{"ALP"}, partialResultDataService(primary, nil))
			orch.deps.OperationalDailyProvider = &dailyFallbackStub{get: func(_ context.Context, _ string, _, to time.Time) (data.ExactHistoricalResult, error) {
				if !to.Equal(time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)) {
					t.Fatalf("post-close completed session excluded: %v", to)
				}
				var bars []domain.OHLCV
				for _, day := range []int{3, 4, 8, 9, 10} {
					bars = append(bars, domain.OHLCV{Timestamp: time.Date(2026, 9, day, 0, 0, 0, 0, easternTime), Close: 5, Volume: 100})
				}
				r := dailyExactFixture(bars)
				r.Receipt.Pages = pages
				return r, nil
			}}
			summary := map[string]int{}
			got, err := orch.deepScanDailyBars(context.Background(), "ALP", now.AddDate(0, -1, 0), now, summary)
			if err != nil || len(got) != 5 || got[0].Close != 5 || summary["daily_fallback_accepted"] != 1 || summary["daily_fallback_provider_pages"] != pages || summary["daily_fallback_cache_hits"] != 1-pages {
				t.Fatalf("mixed series or incorrect receipt: bars=%v summary=%v err=%v", got, summary, err)
			}
		})
	}
}

func TestOperationalDailyScoringGuards(t *testing.T) {
	now := time.Date(2026, 9, 10, 10, 25, 0, 0, easternTime)
	for _, tc := range []struct {
		name   string
		mutate func(*data.ExactHistoricalResult)
		valid  bool
	}{
		{"valid", func(*data.ExactHistoricalResult) {}, true},
		{"cache receipt", func(r *data.ExactHistoricalResult) { r.Receipt.Pages = 0 }, true},
		{"wrong feed", func(r *data.ExactHistoricalResult) { r.Receipt.Feed = "iex" }, false},
		{"wrong adjustment", func(r *data.ExactHistoricalResult) { r.Receipt.AdjustmentPolicy = "raw" }, false},
		{"no source", func(r *data.ExactHistoricalResult) { r.Pages = nil }, false},
		{"short", func(r *data.ExactHistoricalResult) { r.Bars = r.Bars[1:] }, false},
		{"stale", func(r *data.ExactHistoricalResult) {
			for i := range r.Bars {
				r.Bars[i].Timestamp = r.Bars[i].Timestamp.AddDate(0, 0, -7)
			}
		}, false},
		{"provisional", func(r *data.ExactHistoricalResult) { r.Bars[4].Timestamp = now }, false},
		{"nonfinite", func(r *data.ExactHistoricalResult) { r.Bars[4].Volume = "NaN" }, false},
		{"overflow", func(r *data.ExactHistoricalResult) { r.Bars[4].Close = "1e400" }, false},
		{"negative volume", func(r *data.ExactHistoricalResult) { r.Bars[4].Volume = "-1" }, false},
		{"bad bounds", func(r *data.ExactHistoricalResult) { r.Bars[4].High = "1" }, false},
		{"duplicate", func(r *data.ExactHistoricalResult) { r.Bars[4].Timestamp = r.Bars[3].Timestamp }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var bars []domain.OHLCV
			for _, day := range []int{2, 3, 4, 8, 9} {
				bars = append(bars, domain.OHLCV{Timestamp: time.Date(2026, 9, day, 0, 0, 0, 0, easternTime), Close: 5, Volume: 100})
			}
			r := dailyExactFixture(bars)
			tc.mutate(&r)
			got, err := operationalDailyBars(r, now)
			if (err == nil) != tc.valid || (!tc.valid && len(got) != 0) {
				t.Fatalf("valid=%v got=%v err=%v", tc.valid, got, err)
			}
		})
	}
}
