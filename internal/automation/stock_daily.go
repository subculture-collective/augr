package automation

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// OperationalDailyProvider supplies a whole, source-preserving historical series
// for operational scoring only. It is not a strategy or research dataset loader.
type OperationalDailyProvider interface {
	GetDailyBars(context.Context, string, time.Time, time.Time) (data.ExactHistoricalResult, error)
}

func (o *JobOrchestrator) deepScanDailyBars(ctx context.Context, ticker string, from, now time.Time, summary map[string]int) ([]domain.OHLCV, error) {
	// Preserve the existing generic acquisition policy. A stale successful Yahoo
	// response must not trigger additional Polygon acquisition through validation.
	bars, originalErr := o.deps.DataService.GetOHLCV(ctx, "stock", ticker, data.Timeframe1d, from, now)
	completed := completedDailyBars(now, bars)
	if originalErr == nil && len(completed) >= 5 && dailyBarFresh(now, completed[len(completed)-1].Timestamp) {
		return completed, nil
	}
	if o.deps.OperationalDailyProvider == nil || ctx.Err() != nil {
		return completed, originalErr
	}
	summary["daily_fallback_attempted"]++
	expected := expectedCompletedNYSESession(now)
	end := time.Date(expected.Year(), expected.Month(), expected.Day()+1, 0, 0, 0, 0, time.UTC)
	result, err := o.deps.OperationalDailyProvider.GetDailyBars(ctx, ticker, from.UTC().Truncate(24*time.Hour), end)
	if err == nil {
		var converted []domain.OHLCV
		converted, err = operationalDailyBars(result, now)
		if err == nil {
			summary["daily_fallback_accepted"]++
			summary["daily_fallback_provider_pages"] += result.Receipt.Pages
			if result.Receipt.Pages == 0 {
				summary["daily_fallback_cache_hits"]++
			}
			o.logger.Info("deep_scan: accepted source-separated daily fallback", slog.String("ticker", ticker), slog.String("provider", result.Receipt.Provider), slog.String("feed", result.Receipt.Feed), slog.String("adjustment", result.Receipt.AdjustmentPolicy))
			return converted, nil
		}
	}
	summary["daily_fallback_rejected"]++
	o.logger.Warn("deep_scan: daily fallback rejected", slog.String("ticker", ticker), slog.Any("error", err))
	// Keep the original failure classification; no partial fallback rows or mixed
	// sources are accepted when the alternate provider cannot meet the contract.
	return completed, originalErr
}

func operationalDailyBars(result data.ExactHistoricalResult, now time.Time) ([]domain.OHLCV, error) {
	r := result.Receipt
	if r.Provider != "alpaca" || r.Feed != "sip" || r.AdjustmentPolicy != "split" || !r.PaginationComplete || !r.Entitled || r.Pages < 0 || r.Pages > 1 || len(result.Pages) != 1 || len(result.Pages[0].Body) == 0 {
		return nil, fmt.Errorf("daily fallback: incomplete SIP split source")
	}
	bars := make([]domain.OHLCV, 0, len(result.Bars))
	for _, row := range result.Bars {
		values := make([]float64, 5)
		for i, raw := range []string{row.Open, row.High, row.Low, row.Close, row.Volume} {
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || (i < 4 && v == 0) {
				return nil, fmt.Errorf("daily fallback: invalid scoring value")
			}
			values[i] = v
		}
		if values[2] > values[1] || values[0] < values[2] || values[0] > values[1] || values[3] < values[2] || values[3] > values[1] || (len(bars) > 0 && !row.Timestamp.After(bars[len(bars)-1].Timestamp)) {
			return nil, fmt.Errorf("daily fallback: inconsistent scoring series")
		}
		bars = append(bars, domain.OHLCV{Timestamp: row.Timestamp, Open: values[0], High: values[1], Low: values[2], Close: values[3], Volume: values[4]})
	}
	completed := completedDailyBars(now, bars)
	if len(completed) != len(bars) || len(bars) < 5 || !dailyBarFresh(now, bars[len(bars)-1].Timestamp) {
		return nil, fmt.Errorf("daily fallback: insufficient or stale completed history")
	}
	last, prev := bars[len(bars)-1], bars[len(bars)-2]
	score := scoreFromSnapshot((last.Close-prev.Close)/prev.Close*100, last.Volume, prev.Volume, last.Close)
	if math.IsNaN(score) || math.IsInf(score, 0) {
		return nil, fmt.Errorf("daily fallback: nonfinite score")
	}
	return bars, nil
}
