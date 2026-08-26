package automation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
	"github.com/PatrickFanella/get-rich-quick/internal/universe"
)

// Schedule specs for market-hours jobs.
var (
	currentDataRefreshSpec = scheduler.ScheduleSpec{
		Type:                  scheduler.ScheduleTypeMarketHours,
		Cron:                  "30 * * * 1-5", // 09:30–15:30 hourly plus 16:30 ET closing refresh
		SkipWeekends:          true,
		SkipHolidays:          true,
		PostCloseGraceMinutes: 30,
	}
	hotScanSpec = scheduler.ScheduleSpec{
		Type:         scheduler.ScheduleTypeMarketHours,
		Cron:         "0 * * * 1-5", // hourly after the preceding :30 refresh
		SkipWeekends: true,
		SkipHolidays: true,
	}
	deepScanSpec = scheduler.ScheduleSpec{
		Type:         scheduler.ScheduleTypeMarketHours,
		Cron:         "25 * * * 1-5", // after the current hour's hot scan
		SkipWeekends: true,
		SkipHolidays: true,
	}
)

func (o *JobOrchestrator) registerMarketJobs() {
	o.Register("current_data_refresh", "Refresh intraday OHLCV for holdings, active strategies, and top watchlist", currentDataRefreshSpec, o.currentDataRefresh)
	o.Register("hot_scan", "Quick scan tickers refreshed by the current market-data run", hotScanSpec, o.hotScan, "current_data_refresh")
	o.Register("deep_scan", "Operational holdings, strategies, and watchlist score update", deepScanSpec, o.deepScan, "hot_scan")
}

// currentDataRefresh refreshes recent intraday OHLCV for the most relevant stock tickers.
func (o *JobOrchestrator) currentDataRefresh(ctx context.Context) error {
	summary := map[string]int{
		"tickers":                 0,
		"batches":                 0,
		"updated":                 0,
		"empty":                   0,
		"cache_only":              0,
		"stale":                   0,
		"provider_requests":       0,
		"provider_failures":       0,
		"fresh_bars":              0,
		"daily_updated":           0,
		"daily_empty":             0,
		"daily_cache_only":        0,
		"daily_stale":             0,
		"daily_provider_requests": 0,
		"daily_provider_failures": 0,
		"daily_fresh_bars":        0,
		"errors":                  0,
		"positions":               0,
		"strategies":              0,
		"watchlist":               0,
		"selected":                0,
	}
	defer func() {
		o.SetLastSummary("current_data_refresh", summary)
	}()
	selection, err := o.selectOperationalStockTickers(ctx)
	if err != nil {
		summary["errors"]++
		return fmt.Errorf("current_data_refresh: select operational tickers: %w", err)
	}
	tickers := selection.Tickers
	summary["positions"] = selection.Positions
	summary["strategies"] = selection.Strategies
	summary["watchlist"] = selection.Watchlist
	summary["selected"] = len(tickers)
	summary["tickers"] = len(tickers)
	if len(tickers) == 0 {
		return fmt.Errorf("current_data_refresh: no operational stock tickers selected")
	}
	if o.deps.DataService == nil {
		return fmt.Errorf("current_data_refresh: data service unavailable for %d tickers", len(tickers))
	}

	const batchSize = 10
	now := time.Now().UTC()
	intradayFrom := now.Add(-48 * time.Hour)
	dailyFrom := now.AddDate(0, 0, -10)
	freshTickers := make([]string, 0, len(tickers))
	for start := 0; start < len(tickers); start += batchSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := start + batchSize
		if end > len(tickers) {
			end = len(tickers)
		}
		batch := tickers[start:end]
		summary["batches"]++

		refresh := func(timeframe data.Timeframe, from time.Time, prefix string, fresh func(time.Time, []domain.OHLCV) bool) error {
			updatedKey := prefix + "updated"
			emptyKey := prefix + "empty"
			cacheOnlyKey := prefix + "cache_only"
			staleKey := prefix + "stale"
			providerRequestsKey := prefix + "provider_requests"
			providerFailuresKey := prefix + "provider_failures"
			freshBarsKey := prefix + "fresh_bars"
			download, err := o.deps.DataService.DownloadHistoricalOHLCVWithStats(ctx, domain.MarketTypeStock, batch, timeframe, from, now, false)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return err
				}
				o.logger.Warn("current_data_refresh: batch refresh failed",
					slog.Int("batch", summary["batches"]),
					slog.Int("tickers", len(batch)),
					slog.String("timeframe", timeframe.String()),
					slog.Any("error", err),
				)
				if download == nil {
					summary["errors"]++
					return nil
				}
			}
			for _, ticker := range batch {
				summary[providerRequestsKey] += download.ProviderRequests[ticker]
				summary[providerFailuresKey] += download.ProviderFailures[ticker]
				summary[freshBarsKey] += download.FreshBars[ticker]
				if download.ProviderFailures[ticker] > 0 {
					continue
				}
				if download.ProviderRequests[ticker] == 0 {
					summary[cacheOnlyKey]++
					continue
				}
				bars := download.Bars[ticker]
				if download.FreshBars[ticker] == 0 || len(bars) == 0 {
					summary[emptyKey]++
					continue
				}
				if !fresh(now, bars) {
					summary[staleKey]++
					continue
				}
				summary[updatedKey]++
				if prefix == "" {
					freshTickers = append(freshTickers, ticker)
				}
			}
			return nil
		}
		if err := refresh(data.Timeframe5m, intradayFrom, "", func(now time.Time, bars []domain.OHLCV) bool {
			return len(bars) > 0 && intradayBarFresh(now, bars[len(bars)-1].Timestamp)
		}); err != nil {
			return err
		}
		if err := refresh(data.Timeframe1d, dailyFrom, "daily_", dailySeriesFresh); err != nil {
			return err
		}

		if end < len(tickers) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(150 * time.Millisecond):
			}
		}
	}

	o.logger.Info("current_data_refresh: complete",
		slog.Int("tickers", summary["tickers"]),
		slog.Int("batches", summary["batches"]),
		slog.Int("updated", summary["updated"]),
		slog.Int("empty", summary["empty"]),
		slog.Int("provider_failures", summary["provider_failures"]),
		slog.Int("daily_updated", summary["daily_updated"]),
		slog.Int("daily_empty", summary["daily_empty"]),
		slog.Int("daily_provider_failures", summary["daily_provider_failures"]),
		slog.Int("errors", summary["errors"]),
	)
	err = o.completeCurrentDataRefresh(summary, freshTickers)
	return err
}

func (o *JobOrchestrator) completeCurrentDataRefresh(summary map[string]int, freshTickers []string) error {
	err := currentDataRefreshCompletionError(summary)
	if err == nil || IsDegraded(err) {
		o.setRefreshedTickers(freshTickers)
	}
	return err
}

// hotScan scores the exact fresh intraday output from current_data_refresh.
func (o *JobOrchestrator) hotScan(ctx context.Context) error {
	summary := map[string]int{"selected": 0, "scored": 0, "fetch_errors": 0, "insufficient": 0, "stale": 0, "score_errors": 0, "significant_tickers": 0, "trigger_requests": 0, "strategy_list_failed": 0}
	defer func() { o.SetLastSummary("hot_scan", summary) }()
	if o.deps.Universe == nil || o.deps.DataService == nil {
		return fmt.Errorf("hot_scan: universe and data service are required")
	}
	tickers := o.getRefreshedTickers()
	summary["selected"] = len(tickers)
	if len(tickers) == 0 {
		return fmt.Errorf("hot_scan: no fresh ticker selection from current_data_refresh")
	}

	type mover struct {
		ticker    string
		changePct float64
	}
	var topMovers []mover

	now := time.Now()
	from := now.Add(-2 * time.Hour)

	for _, ticker := range tickers {
		bars, fetchErr := o.deps.DataService.GetOHLCV(ctx, "stock", ticker, data.Timeframe5m, from, now)
		if fetchErr != nil {
			summary["fetch_errors"]++
			continue
		}
		if len(bars) < 2 {
			summary["insufficient"]++
			continue
		}

		lastBar := bars[len(bars)-1]
		if !intradayBarFresh(now, lastBar.Timestamp) {
			summary["stale"]++
			continue
		}
		prevBar := bars[len(bars)-2]
		changePct := 0.0
		if prevBar.Close > 0 {
			changePct = (lastBar.Close - prevBar.Close) / prevBar.Close * 100
		}

		score := scoreFromSnapshot(changePct, lastBar.Volume, prevBar.Volume, lastBar.Close) * universe.IndexBoost(ticker)
		if err := o.deps.Universe.UpdateScore(ctx, ticker, score); err != nil {
			summary["score_errors"]++
			o.logger.Warn("hot_scan: update score failed",
				slog.String("ticker", ticker),
				slog.Any("error", err),
			)
		} else {
			summary["scored"]++
		}
		topMovers = append(topMovers, mover{ticker: ticker, changePct: changePct})
	}

	// Sort movers by absolute change pct descending.
	sort.Slice(topMovers, func(i, j int) bool {
		return math.Abs(topMovers[i].changePct) > math.Abs(topMovers[j].changePct)
	})

	logCount := 10
	if logCount > len(topMovers) {
		logCount = len(topMovers)
	}
	for _, m := range topMovers[:logCount] {
		o.logger.Info("hot_scan: top mover",
			slog.String("ticker", m.ticker),
			slog.Float64("change_pct", m.changePct),
		)
	}

	// Trigger active strategies for significant movers (|change| > 3%).
	if o.deps.StrategyTrigger != nil {
		significantTickers := make(map[string]float64)
		for _, m := range topMovers {
			if math.Abs(m.changePct) > 3.0 {
				significantTickers[m.ticker] = m.changePct
			}
		}
		if len(significantTickers) > 0 {
			summary["significant_tickers"] = len(significantTickers)
			var strategies []domain.Strategy
			var listErr error
			if o.deps.StrategyRepo == nil {
				listErr = fmt.Errorf("strategy repository unavailable")
			} else {
				strategies, listErr = listAllStrategies(ctx, o.deps.StrategyRepo, repository.StrategyFilter{
					Status: domain.StrategyStatusActive,
				})
			}
			if listErr == nil {
				for _, s := range canonicalTriggeredStrategies(strategies) {
					if changePct, ok := significantTickers[s.Ticker]; ok {
						o.logger.Info("hot_scan: requesting strategy trigger for significant move",
							slog.String("ticker", s.Ticker),
							slog.String("strategy_id", s.ID.String()),
							slog.Float64("change_pct", changePct),
						)
						o.deps.StrategyTrigger.TriggerStrategy(s)
						summary["trigger_requests"]++
					}
				}
			} else {
				summary["strategy_list_failed"]++
				o.logger.Warn("hot_scan: failed to list strategies for triggers", slog.Any("error", listErr))
			}
		}
	}

	o.logger.Info("hot_scan: complete", slog.Int("scanned", len(tickers)))
	return marketScanCompletionError("hot_scan", summary, 80)
}

func canonicalTriggeredStrategies(strategies []domain.Strategy) []domain.Strategy {
	canonical := make(map[string]domain.Strategy, len(strategies))
	for _, strategy := range strategies {
		marketType := strategy.MarketType.Normalize()
		if marketType != domain.MarketTypeStock {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(strategy.Ticker)) + "\x00" + string(marketType) + "\x00" + strings.TrimSpace(strategy.ScheduleCron)
		current, ok := canonical[key]
		if !ok || strategy.ID.String() < current.ID.String() {
			canonical[key] = strategy
		}
	}
	result := make([]domain.Strategy, 0, len(canonical))
	for _, strategy := range canonical {
		result = append(result, strategy)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Ticker != result[j].Ticker {
			return result[i].Ticker < result[j].Ticker
		}
		if result[i].ScheduleCron != result[j].ScheduleCron {
			return result[i].ScheduleCron < result[j].ScheduleCron
		}
		return result[i].ID.String() < result[j].ID.String()
	})
	return result
}

// deepScan scores operational stock tickers using locally stored daily OHLCV.
func (o *JobOrchestrator) deepScan(ctx context.Context) error {
	summary := map[string]int{"selected": 0, "positions": 0, "strategies": 0, "watchlist": 0, "scored": 0, "fetch_errors": 0, "insufficient": 0, "stale": 0, "score_errors": 0}
	defer func() { o.SetLastSummary("deep_scan", summary) }()
	if o.deps.Universe == nil || o.deps.DataService == nil {
		return fmt.Errorf("deep_scan: universe and data service are required")
	}
	selection, err := o.selectOperationalStockTickers(ctx)
	if err != nil {
		return fmt.Errorf("deep_scan: select operational tickers: %w", err)
	}
	allSymbols := selection.Tickers
	summary["positions"] = selection.Positions
	summary["strategies"] = selection.Strategies
	summary["watchlist"] = selection.Watchlist
	summary["selected"] = len(allSymbols)
	if len(allSymbols) == 0 {
		return fmt.Errorf("deep_scan: no operational stock tickers selected")
	}

	var totalScored int
	var scoreSum float64

	type scored struct {
		ticker string
		score  float64
	}
	var allScored []scored

	now := time.Now()
	from := now.AddDate(0, -1, 0) // 1 month of recent bars for scoring

	for i, ticker := range allSymbols {
		bars, fetchErr := o.deps.DataService.GetOHLCV(ctx, "stock", ticker, data.Timeframe1d, from, now)
		if fetchErr != nil {
			summary["fetch_errors"]++
			continue
		}
		bars = completedDailyBars(now, bars)
		if len(bars) < 5 {
			summary["insufficient"]++
			continue
		}

		// Score from recent bars: volatility + volume + momentum.
		lastBar := bars[len(bars)-1]
		if !dailyBarFresh(now, lastBar.Timestamp) {
			summary["stale"]++
			continue
		}
		prevBar := bars[len(bars)-2]
		changePct := 0.0
		if prevBar.Close > 0 {
			changePct = (lastBar.Close - prevBar.Close) / prevBar.Close * 100
		}

		score := scoreFromSnapshot(changePct, lastBar.Volume, prevBar.Volume, lastBar.Close) * universe.IndexBoost(ticker)
		if err := o.deps.Universe.UpdateScore(ctx, ticker, score); err != nil {
			summary["score_errors"]++
			o.logger.Warn("deep_scan: update score failed",
				slog.String("ticker", ticker),
				slog.Any("error", err),
			)
		} else {
			summary["scored"]++
		}
		totalScored++
		scoreSum += score
		allScored = append(allScored, scored{ticker: ticker, score: score})

		if (i+1)%500 == 0 {
			o.logger.Info("deep_scan: progress",
				slog.Int("scored", i+1),
				slog.Int("total", len(allSymbols)),
			)
		}
	}

	// Log summary with top 10.
	avgScore := 0.0
	if totalScored > 0 {
		avgScore = scoreSum / float64(totalScored)
	}

	sort.Slice(allScored, func(i, j int) bool {
		return allScored[i].score > allScored[j].score
	})

	logCount := 10
	if logCount > len(allScored) {
		logCount = len(allScored)
	}

	o.logger.Info("deep_scan: summary",
		slog.Int("total_scanned", totalScored),
		slog.Float64("avg_score", avgScore),
	)
	for _, s := range allScored[:logCount] {
		o.logger.Info("deep_scan: top ticker",
			slog.String("ticker", s.ticker),
			slog.Float64("score", s.score),
		)
	}

	return marketScanCompletionError("deep_scan", summary, 50)
}

func currentDataRefreshCompletionError(summary map[string]int) error {
	selected := summary["selected"]
	if selected == 0 {
		selected = summary["tickers"]
	}
	updated := summary["updated"]
	coverage := 0
	if selected > 0 {
		coverage = updated * 100 / selected
	}
	if summary["errors"] > 0 || (selected > 0 && (updated == 0 || updated*100 < selected*80)) {
		return fmt.Errorf("current_data_refresh: unusable intraday coverage: updated=%d selected=%d coverage=%d%% minimum=80%% errors=%d intraday(provider_failures=%d empty=%d cache_only=%d stale=%d) daily(provider_failures=%d empty=%d cache_only=%d stale=%d)",
			updated, selected, coverage, summary["errors"], summary["provider_failures"], summary["empty"], summary["cache_only"], summary["stale"],
			summary["daily_provider_failures"], summary["daily_empty"], summary["daily_cache_only"], summary["daily_stale"])
	}
	findings := summary["provider_failures"] + summary["empty"] + summary["cache_only"] + summary["stale"] +
		summary["daily_provider_failures"] + summary["daily_empty"] + summary["daily_cache_only"] + summary["daily_stale"]
	if selected == 0 || (updated == selected && findings == 0) {
		return nil
	}
	return Degradedf("current_data_refresh: partial intraday coverage: updated=%d selected=%d coverage=%d%% intraday(provider_failures=%d empty=%d cache_only=%d stale=%d) daily(provider_failures=%d empty=%d cache_only=%d stale=%d)",
		updated, selected, coverage, summary["provider_failures"], summary["empty"], summary["cache_only"], summary["stale"],
		summary["daily_provider_failures"], summary["daily_empty"], summary["daily_cache_only"], summary["daily_stale"])
}

func marketScanCompletionError(job string, summary map[string]int, minimumCoveragePercent int) error {
	selected := summary["selected"]
	scored := summary["scored"]
	coverage := 0
	if selected > 0 {
		coverage = scored * 100 / selected
	}
	if summary["score_errors"] > 0 || summary["strategy_list_failed"] > 0 {
		return fmt.Errorf("%s: infrastructure failure: score_errors=%d strategy_list_failed=%d", job, summary["score_errors"], summary["strategy_list_failed"])
	}
	if selected == 0 || scored == 0 || scored*100 < selected*minimumCoveragePercent {
		return fmt.Errorf("%s: unusable coverage: scored=%d selected=%d coverage=%d%% minimum=%d%%", job, scored, selected, coverage, minimumCoveragePercent)
	}
	incomplete := summary["fetch_errors"] + summary["insufficient"] + summary["stale"]
	if incomplete == 0 && scored == selected {
		return nil
	}
	return Degradedf("%s: partial coverage: scored=%d selected=%d coverage=%d%% fetch_errors=%d insufficient=%d stale=%d", job,
		scored, selected, coverage, summary["fetch_errors"], summary["insufficient"], summary["stale"])
}

func intradayBarFresh(now, latest time.Time) bool {
	if !scheduler.IsRegularMarketOpen(now, domain.MarketTypeStock) {
		return false
	}
	nowET := now.In(easternTime)
	latestET := latest.In(easternTime)
	if !sameMarketDate(nowET, latestET) {
		return false
	}
	open := time.Date(nowET.Year(), nowET.Month(), nowET.Day(), 9, 30, 0, 0, easternTime)
	if latestET.Before(open) || latestET.After(nowET.Add(5*time.Minute)) {
		return false
	}
	return nowET.Sub(latestET) <= 20*time.Minute
}

func dailyBarFresh(now, latest time.Time) bool {
	expected := expectedCompletedNYSESession(now)
	return sameMarketDate(expected, latest.In(easternTime))
}

func expectedCompletedNYSESession(now time.Time) time.Time {
	expected := now.In(easternTime)
	minutes := expected.Hour()*60 + expected.Minute()
	// Daily providers publish completed candles. During an open session, today's
	// candle is still provisional (and some providers omit it entirely), so the
	// latest trustworthy daily bar is the prior NYSE session. After the close,
	// today's completed candle may be used.
	if !scheduler.IsNYSETradingDay(expected) || minutes < 16*60 {
		expected = expected.AddDate(0, 0, -1)
		for !scheduler.IsNYSETradingDay(expected) {
			expected = expected.AddDate(0, 0, -1)
		}
	}
	return expected
}

func completedDailyBars(now time.Time, bars []domain.OHLCV) []domain.OHLCV {
	expected := expectedCompletedNYSESession(now)
	completed := make([]domain.OHLCV, 0, len(bars))
	for _, bar := range bars {
		barDate := bar.Timestamp.In(easternTime)
		if barDate.After(expected) && !sameMarketDate(barDate, expected) {
			continue
		}
		completed = append(completed, bar)
	}
	return completed
}

func dailySeriesFresh(now time.Time, bars []domain.OHLCV) bool {
	completed := completedDailyBars(now, bars)
	return len(completed) > 0 && dailyBarFresh(now, completed[len(completed)-1].Timestamp)
}

func sameMarketDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// scoreFromSnapshot computes a watch score combining momentum, volume surge,
// and dollar volume (liquidity). Dollar volume prevents penny stocks from
// dominating — a $0.50 stock needs 400x the share volume of a $200 stock
// to score equivalently on the liquidity component.
func scoreFromSnapshot(changePct, todayVol, prevVol, closePrice float64) float64 {
	volRatio := 1.0
	if prevVol > 0 {
		volRatio = todayVol / prevVol
	}

	momentum := math.Abs(changePct)
	volSurge := math.Log1p(math.Max(0, volRatio-1))           // only reward above-average volume
	dollarVol := math.Log10(math.Max(1, closePrice*todayVol)) // log10 of dollar volume

	// Weights: liquidity matters most, then momentum, then volume surge.
	return 0.4*dollarVol + 0.35*momentum + 0.25*volSurge
}
