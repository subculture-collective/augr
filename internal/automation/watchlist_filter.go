package automation

import (
	"context"
	"log/slog"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/universe"
)

const minTradeableWatchlistPrice = 5.0

type watchlistSource interface {
	GetWatchlist(context.Context, int) ([]universe.TrackedTicker, error)
}

type recentOHLCVSource interface {
	GetOHLCV(context.Context, domain.MarketType, string, data.Timeframe, time.Time, time.Time) ([]domain.OHLCV, error)
}

func tradeableWatchlistTickers(ctx context.Context, logger *slog.Logger, u watchlistSource, ds recentOHLCVSource, sourceLimit, desired int) ([]universe.TrackedTicker, error) {
	if logger == nil {
		logger = slog.Default()
	}
	watchlist, err := u.GetWatchlist(ctx, sourceLimit)
	if err != nil {
		return nil, err
	}
	if len(watchlist) == 0 {
		return nil, nil
	}
	if desired <= 0 || desired > len(watchlist) {
		desired = len(watchlist)
	}

	if ds == nil {
		tickers := make([]universe.TrackedTicker, 0, desired)
		for i := 0; i < desired; i++ {
			tickers = append(tickers, watchlist[i])
		}
		return tickers, nil
	}

	filtered := make([]universe.TrackedTicker, 0, desired)
	var lowPriceCount, insufficientBarsCount int
	now := time.Now()
	from := now.AddDate(0, 0, -14)

	for _, item := range watchlist {
		if len(filtered) >= desired {
			break
		}

		bars, err := ds.GetOHLCV(ctx, domain.MarketTypeStock, item.Ticker, data.Timeframe1d, from, now)
		if err != nil || len(bars) == 0 {
			insufficientBarsCount++
			continue
		}

		if bars[len(bars)-1].Close >= minTradeableWatchlistPrice {
			filtered = append(filtered, item)
			continue
		}
		lowPriceCount++
	}

	if lowPriceCount > 0 || insufficientBarsCount > 0 {
		logger.Info("automation: filtered tradeable watchlist",
			slog.Int("requested", desired),
			slog.Int("source", len(watchlist)),
			slog.Int("selected", len(filtered)),
			slog.Int("low_price", lowPriceCount),
			slog.Int("insufficient_bars", insufficientBarsCount),
		)
	}

	if len(filtered) == 0 {
		logger.Info("automation: no tradeable watchlist tickers found",
			slog.Int("source", len(watchlist)),
			slog.Int("requested", desired),
		)
		return nil, nil
	}

	return filtered, nil
}
