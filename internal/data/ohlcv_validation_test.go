package data

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// Reproduce the failed September 3 after-hours scan: a nonempty response
// ending September 2 must not prevent a provider with September 3 coverage.
func TestValidatedOHLCVFreshnessFallback(t *testing.T) {
	now := time.Date(2026, 9, 4, 2, 0, 0, 0, time.UTC)
	freshAt := time.Date(2026, 9, 3, 13, 30, 0, 0, time.UTC)
	fresh := []domain.OHLCV{{Timestamp: freshAt, Close: 105}}
	stale := []domain.OHLCV{{Timestamp: freshAt.AddDate(0, 0, -1), Close: 100}}
	accept := func(bars []domain.OHLCV) bool {
		return len(bars) > 0 && bars[len(bars)-1].Timestamp.Equal(freshAt)
	}
	for _, tc := range []struct {
		name                            string
		cached, first, second           []domain.OHLCV
		secondErr                       error
		wantReject                      bool
		firstCalls, secondCalls, writes int
	}{
		{name: "stale cache and primary recover through fallback", cached: stale, first: stale, second: fresh, firstCalls: 1, secondCalls: 1, writes: 1},
		{name: "fresh cache avoids providers", cached: fresh, first: stale},
		{name: "fresh primary avoids fallback", first: fresh, second: stale, firstCalls: 1, writes: 1},
		{name: "all stale fail closed", cached: stale, first: stale, second: stale, wantReject: true, firstCalls: 1, secondCalls: 1},
		{name: "stale then empty fails closed", first: stale, wantReject: true, firstCalls: 1, secondCalls: 1},
		{name: "stale then outage fails closed", first: stale, secondErr: errors.New("provider outage"), wantReject: true, firstCalls: 1, secondCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := &fakeMarketDataCacheRepo{}
			if tc.cached != nil {
				payload, err := json.Marshal(tc.cached)
				if err != nil {
					t.Fatal(err)
				}
				cache.getResult = &domain.MarketData{Data: payload}
			}
			first := &serviceStubProvider{ohlcv: tc.first}
			second := &serviceStubProvider{ohlcv: tc.second, ohlcvErr: tc.secondErr}
			service := &DataService{stockChain: NewProviderChain(discardLogger(), first, second), cacheRepo: cache, logger: discardLogger(), now: func() time.Time { return now }}
			bars, err := service.GetOHLCVValidated(context.Background(), domain.MarketTypeStock, "AAPL", Timeframe1d, now.AddDate(0, 0, -5), now, accept)
			if tc.wantReject {
				if !errors.Is(err, ErrOHLCVRejected) || len(bars) != 0 {
					t.Fatalf("bars=%v error=%v, want rejection", bars, err)
				}
			} else if err != nil || !accept(bars) {
				t.Fatalf("bars=%v error=%v, want current session", bars, err)
			}
			if first.ohlcvCalls != tc.firstCalls || second.ohlcvCalls != tc.secondCalls || cache.setCalls != tc.writes {
				t.Fatalf("calls=%d/%d writes=%d, want %d/%d/%d", first.ohlcvCalls, second.ohlcvCalls, cache.setCalls, tc.firstCalls, tc.secondCalls, tc.writes)
			}
			if cache.setData != nil {
				var stored []domain.OHLCV
				if err := json.Unmarshal(cache.setData.Data, &stored); err != nil || !accept(stored) {
					t.Fatalf("cached rejected data: %s", cache.setData.Data)
				}
			}
		})
	}
}
