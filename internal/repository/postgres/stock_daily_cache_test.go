package postgres

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

func TestStockDailyCacheSourceIsolation(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newMarketDataCacheIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewMarketDataCacheRepo(pool)
	now := time.Now().UTC()
	to := now.Truncate(24 * time.Hour)
	from := to.AddDate(0, 0, -30)
	for _, source := range []struct{ provider, kind, body string }{
		{"stock-chain", "ohlcv", `{"sentinel":"original generic cache"}`},
		{"alpaca-sip-split-daily-v1", "stock_daily_source", `{"symbol":"ALP","bars":[{"c":5.4141,"v":152208}],"next_page_token":null}`},
	} {
		md := &domain.MarketData{Ticker: "ALP", Provider: source.provider, DataType: source.kind, Timeframe: "1d", DateFrom: &from, DateTo: &to, Data: json.RawMessage(source.body), FetchedAt: now, ExpiresAt: now.Add(time.Hour)}
		if err := repo.Set(ctx, md); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range []struct{ provider, kind, body string }{
		{"stock-chain", "ohlcv", `{"sentinel":"original generic cache"}`},
		{"alpaca-sip-split-daily-v1", "stock_daily_source", `{"symbol":"ALP","bars":[{"c":5.4141,"v":152208}],"next_page_token":null}`},
	} {
		got, err := repo.Get(ctx, repository.MarketDataCacheKey{Ticker: "ALP", Provider: source.provider, DataType: source.kind, Timeframe: "1d", DateFrom: &from, DateTo: &to})
		if err != nil {
			t.Fatal(err)
		}
		if got.Provider != source.provider || !jsonBytesEqual(got.Data, []byte(source.body)) || !got.DateFrom.Equal(from) || !got.DateTo.Equal(to) {
			t.Fatalf("source identity lost: %+v", got)
		}
	}
}
