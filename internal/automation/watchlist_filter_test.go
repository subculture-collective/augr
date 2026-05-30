package automation

import (
	"context"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/universe"
)

type fakeWatchlistRepo struct {
	watchlist []universe.TrackedTicker
}

func (f fakeWatchlistRepo) GetWatchlist(context.Context, int) ([]universe.TrackedTicker, error) {
	return f.watchlist, nil
}

type fakeRecentOHLCVSource struct {
	bars map[string][]domain.OHLCV
}

func (f fakeRecentOHLCVSource) GetOHLCV(_ context.Context, _ domain.MarketType, ticker string, _ data.Timeframe, _, _ time.Time) ([]domain.OHLCV, error) {
	return f.bars[ticker], nil
}

func TestTradeableWatchlistTickers_FallsBackWithoutDataService(t *testing.T) {
	t.Parallel()

	watchlist := []universe.TrackedTicker{{Ticker: "A"}, {Ticker: "B"}, {Ticker: "C"}}
	got, err := tradeableWatchlistTickers(context.Background(), nil, fakeWatchlistRepo{watchlist: watchlist}, nil, 10, 2)
	if err != nil {
		t.Fatalf("tradeableWatchlistTickers error = %v", err)
	}
	if len(got) != 2 || got[0].Ticker != "A" || got[1].Ticker != "B" {
		t.Fatalf("got %#v, want first two raw watchlist tickers", got)
	}
}

func TestTradeableWatchlistTickers_FiltersByClosePrice(t *testing.T) {
	t.Parallel()

	watchlist := []universe.TrackedTicker{{Ticker: "LOW"}, {Ticker: "OK"}, {Ticker: "MISS"}}
	got, err := tradeableWatchlistTickers(
		context.Background(),
		nil,
		fakeWatchlistRepo{watchlist: watchlist},
		fakeRecentOHLCVSource{bars: map[string][]domain.OHLCV{
			"LOW":  {{Close: 4.99}},
			"OK":   {{Close: 5.25}},
			"MISS": nil,
		}},
		10,
		5,
	)
	if err != nil {
		t.Fatalf("tradeableWatchlistTickers error = %v", err)
	}
	if len(got) != 1 || got[0].Ticker != "OK" {
		t.Fatalf("got %#v, want only OK", got)
	}
}
