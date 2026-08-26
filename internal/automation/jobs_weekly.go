package automation

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/agent"
	"github.com/PatrickFanella/get-rich-quick/internal/agent/rules"
	"github.com/PatrickFanella/get-rich-quick/internal/backtest"
	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/eventmarkets"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

func (o *JobOrchestrator) registerWeeklyJobs() {
	o.Register("universe_refresh", "Reload universe constituents from Polygon", universeRefreshSpec, o.universeRefresh)
	o.Register("strategy_tournament", "Rank active strategies and recommend review candidates", strategyTournamentSpec, o.strategyTournament)
}

var (
	universeRefreshSpec    = scheduler.ScheduleSpec{Type: scheduler.ScheduleTypeCron, Cron: "0 12 * * 0", SkipWeekends: false, SkipHolidays: false}
	strategyTournamentSpec = scheduler.ScheduleSpec{Type: scheduler.ScheduleTypeCron, Cron: "0 14 * * 0", SkipWeekends: false, SkipHolidays: false}
)

// universeRefresh reloads all universe constituents from Polygon.
func (o *JobOrchestrator) universeRefresh(ctx context.Context) error {
	o.logger.Info("universe_refresh: starting")

	if o.deps.Universe == nil {
		return fmt.Errorf("universe_refresh: universe provider not configured")
	}

	count, err := o.deps.Universe.RefreshConstituents(ctx)
	if err != nil {
		return fmt.Errorf("universe_refresh: %w", err)
	}

	o.logger.Info("universe_refresh: completed", slog.Int("tickers_loaded", count))
	o.SetLastSummary("universe_refresh", map[string]int{"tickers_loaded": count})
	return universeRefreshCompletionError(count)
}

func universeRefreshCompletionError(count int) error {
	if count > 0 {
		return nil
	}
	return fmt.Errorf("universe_refresh: provider returned zero active constituents")
}

// strategyTournament backtests all active strategies over the same
// 1-year period and ranks them by Sharpe ratio.
func (o *JobOrchestrator) strategyTournament(ctx context.Context) error {
	o.logger.Info("strategy_tournament: starting")
	summary := map[string]int{"scanned": 0, "supported": 0, "ranked": 0, "coverage_bps": 0, "skipped": 0, "failed": 0, "provider_contacted": 0, "cache_only": 0, "stale": 0, "config_failed": 0, "fetch_failed": 0, "insufficient": 0, "backtest_failed": 0, "nonfinite": 0}
	defer func() { o.SetLastSummary("strategy_tournament", summary) }()
	if o.deps.StrategyRepo == nil {
		return fmt.Errorf("strategy_tournament: strategy repository is required")
	}

	strategies, err := listAllStrategies(ctx, o.deps.StrategyRepo, repository.StrategyFilter{Status: "active"})
	if err != nil {
		return fmt.Errorf("strategy_tournament: list strategies: %w", err)
	}
	summary["scanned"] = len(strategies)
	for _, strat := range strategies {
		if eventmarkets.SupportsOHLCVResweep(strat.MarketType) {
			summary["supported"]++
		} else {
			summary["skipped"]++
		}
	}
	if summary["supported"] == 0 {
		o.logger.Info("strategy_tournament: completed", slog.Int("strategies_ranked", 0), slog.Int("failed", 0), slog.Int("skipped", summary["skipped"]))
		return strategyTournamentCompletionError(summary)
	}
	if o.deps.DataService == nil {
		return fmt.Errorf("strategy_tournament: data service is required for supported strategies")
	}

	now := time.Now()
	histFrom := now.AddDate(-1, 0, 0)

	type ranked struct {
		name   string
		ticker string
		sharpe float64
	}
	var rankings []ranked

	for _, strat := range strategies {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !eventmarkets.SupportsOHLCVResweep(strat.MarketType) {
			continue
		}

		rulesConfig, err := extractRulesConfig(strat.Config)
		if err != nil {
			summary["failed"]++
			summary["config_failed"]++
			o.logger.Warn("strategy_tournament: bad config",
				slog.String("strategy", strat.Name),
				slog.Any("error", err),
			)
			continue
		}

		download, err := o.deps.DataService.DownloadHistoricalOHLCVWithStats(
			ctx, strat.MarketType,
			[]string{strat.Ticker},
			data.Timeframe1d, histFrom, now, true,
		)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			summary["failed"]++
			summary["fetch_failed"]++
			o.logger.Warn("strategy_tournament: download failed",
				slog.String("ticker", strat.Ticker),
				slog.Any("error", err),
			)
			continue
		}

		if download.ProviderRequests[strat.Ticker] == 0 {
			summary["failed"]++
			summary["cache_only"]++
			o.logger.Warn("strategy_tournament: provider freshness unavailable",
				slog.String("ticker", strat.Ticker),
			)
			continue
		}
		summary["provider_contacted"]++
		bars := download.Bars[strat.Ticker]
		if len(bars) < 50 {
			summary["failed"]++
			summary["insufficient"]++
			o.logger.Warn("strategy_tournament: insufficient bars",
				slog.String("ticker", strat.Ticker),
				slog.Int("bars", len(bars)),
			)
			continue
		}
		if !completedDailyBarFresh(strat.MarketType, now, bars[len(bars)-1].Timestamp) {
			summary["failed"]++
			summary["stale"]++
			o.logger.Warn("strategy_tournament: stale latest bar",
				slog.String("ticker", strat.Ticker),
				slog.Time("latest", bars[len(bars)-1].Timestamp),
			)
			continue
		}

		metrics, err := backtestStrategy(ctx, *rulesConfig, strat.Ticker, bars, o.logger)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			summary["failed"]++
			summary["backtest_failed"]++
			o.logger.Warn("strategy_tournament: backtest failed",
				slog.String("ticker", strat.Ticker),
				slog.Any("error", err),
			)
			continue
		}
		if !validTournamentSharpe(metrics.SharpeRatio) {
			summary["failed"]++
			summary["nonfinite"]++
			o.logger.Warn("strategy_tournament: non-finite Sharpe ratio", slog.String("ticker", strat.Ticker))
			continue
		}

		rankings = append(rankings, ranked{
			name:   strat.Name,
			ticker: strat.Ticker,
			sharpe: metrics.SharpeRatio,
		})
	}

	// Sort by Sharpe descending.
	for i := 0; i < len(rankings); i++ {
		for j := i + 1; j < len(rankings); j++ {
			if rankings[j].sharpe > rankings[i].sharpe {
				rankings[i], rankings[j] = rankings[j], rankings[i]
			}
		}
	}

	// Log ranking table.
	for rank, r := range rankings {
		o.logger.Info(fmt.Sprintf("strategy_tournament: #%d %s (%s) sharpe=%.3f",
			rank+1, r.name, r.ticker, r.sharpe),
		)
		if r.sharpe < -0.5 {
			o.logger.Warn("strategy_tournament: consider disabling",
				slog.String("strategy", r.name),
				slog.String("ticker", r.ticker),
				slog.Float64("sharpe", r.sharpe),
			)
		}
	}

	summary["ranked"] = len(rankings)
	summary["coverage_bps"] = coverageBasisPoints(summary["ranked"], summary["supported"])
	o.logger.Info("strategy_tournament: completed", slog.Int("strategies_ranked", len(rankings)), slog.Int("failed", summary["failed"]), slog.Int("skipped", summary["skipped"]))
	return strategyTournamentCompletionError(summary)
}

func validTournamentSharpe(sharpe float64) bool {
	return !math.IsNaN(sharpe) && !math.IsInf(sharpe, 0)
}

func strategyTournamentCompletionError(summary map[string]int) error {
	supported, ranked := summary["supported"], summary["ranked"]
	coverage := coverageBasisPoints(ranked, supported)
	summary["coverage_bps"] = coverage
	if supported == 0 {
		return nil
	}
	detail := fmt.Sprintf("supported=%d ranked=%d coverage_bps=%d provider_contacted=%d failed=%d config_failed=%d fetch_failed=%d cache_only=%d insufficient=%d stale=%d backtest_failed=%d nonfinite=%d",
		supported, ranked, coverage, summary["provider_contacted"], summary["failed"], summary["config_failed"], summary["fetch_failed"], summary["cache_only"], summary["insufficient"], summary["stale"], summary["backtest_failed"], summary["nonfinite"])
	if supported > 0 && (ranked == 0 || summary["provider_contacted"] == 0) {
		return fmt.Errorf("strategy_tournament: zero ranking or provider coverage: %s", detail)
	}
	if supported > 0 && coverage < 7000 {
		return fmt.Errorf("strategy_tournament: coverage below 70%%: %s", detail)
	}
	if summary["failed"] > 0 || coverage < 10_000 {
		return Degradedf("strategy_tournament: completed with findings: %s", detail)
	}
	return nil
}

// backtestStrategy runs a single backtest for the given rules config
// and bars, returning the computed metrics.
func backtestStrategy(
	ctx context.Context,
	cfg rules.RulesEngineConfig,
	ticker string,
	bars []domain.OHLCV,
	logger *slog.Logger,
) (*backtest.Metrics, error) {
	startDate := bars[0].Timestamp
	endDate := bars[len(bars)-1].Timestamp
	initialCash := 100_000.0

	pipeline := rules.NewRulesPipeline(
		cfg,
		bars,
		startDate,
		initialCash,
		agent.NoopPersister{},
		nil,
		logger,
	)

	orch, err := backtest.NewOrchestrator(
		backtest.OrchestratorConfig{
			StrategyID:  [16]byte{1},
			Ticker:      ticker,
			StartDate:   startDate,
			EndDate:     endDate,
			InitialCash: initialCash,
			FillConfig: backtest.FillConfig{
				Slippage: backtest.ProportionalSlippage{BasisPoints: 5},
			},
		},
		bars,
		pipeline,
		logger,
	)
	if err != nil {
		return nil, fmt.Errorf("create orchestrator: %w", err)
	}

	result, err := orch.Run(ctx)
	if err != nil {
		return nil, fmt.Errorf("run backtest: %w", err)
	}

	return &result.Metrics, nil
}
