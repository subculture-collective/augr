package data

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

type manifestSymbolLoaderStub struct {
	calls int
	bars  []domain.OHLCV
}

func (stub *manifestSymbolLoaderStub) LoadSymbol(_ context.Context, _ uuid.UUID, _ string, _ Timeframe, _, _ time.Time) ([]domain.OHLCV, ManifestBindingReceipt, error) {
	stub.calls++
	return stub.bars, ManifestBindingReceipt{EffectiveEnd: stub.bars[len(stub.bars)-1].Timestamp}, nil
}

func TestManifestBoundDataServiceNeverUsesCacheOrProviderForOHLCV(t *testing.T) {
	start := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	loader := &manifestSymbolLoaderStub{bars: []domain.OHLCV{{Timestamp: start}, {Timestamp: end}}}
	service, err := NewManifestBoundDataService(&DataService{now: time.Now}, uuid.New(), loader)
	if err != nil {
		t.Fatal(err)
	}
	bars, err := service.GetOHLCV(context.Background(), domain.MarketTypeStock, "AAPL", Timeframe1d, start, end)
	if err != nil || len(bars) != 2 || loader.calls != 1 {
		t.Fatalf("GetOHLCV() = %#v, %v; calls=%d", bars, err, loader.calls)
	}
	download, err := service.DownloadHistoricalOHLCVWithStats(context.Background(), domain.MarketTypeStock, []string{"AAPL"}, Timeframe1d, start, end, true)
	if err != nil || len(download.Bars["AAPL"]) != 2 || download.ProviderRequests["AAPL"] != 0 || loader.calls != 2 {
		t.Fatalf("DownloadHistoricalOHLCVWithStats() = %+v, %v; calls=%d", download, err, loader.calls)
	}
	if _, err := service.GetOHLCV(context.Background(), domain.MarketTypeCrypto, "BTC", Timeframe1d, start, end); err == nil {
		t.Fatal("manifest-bound service accepted non-stock provider path")
	}
}
