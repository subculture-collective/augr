package automation

import (
	"context"
	"log/slog"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/polymarketdiscovery"
	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

var polymarketDiscoverySpec = scheduler.ScheduleSpec{Type: scheduler.ScheduleTypeCron, Cron: "0 */6 * * *"}

func (o *JobOrchestrator) registerPolymarketDiscoveryJob() {
	if o.deps.LLMProvider == nil || o.deps.StrategyRepo == nil {
		return
	}
	o.Register("polymarket_strategy_discovery",
		"Auto-generate Polymarket paper strategies from open markets",
		polymarketDiscoverySpec, o.polymarketDiscovery)
}

func (o *JobOrchestrator) polymarketDiscovery(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	cfg := polymarketdiscovery.Config{
		Screener:       polymarketdiscovery.DefaultScreenerConfig(),
		MaxDeployments: 3,
		AutoWatchSlug:  true,
	}
	deps := polymarketdiscovery.Deps{
		LLMProvider:           o.deps.LLMProvider,
		Strategies:            o.deps.StrategyRepo,
		PolymarketAccountRepo: o.deps.PolymarketAccountRepo,
		PolymarketWatchedRepo: o.deps.PolymarketWatchedRepo,
		Logger:                o.logger,
	}
	res, err := polymarketdiscovery.Run(ctx, cfg, deps)
	if err != nil {
		return err
	}
	o.logger.Info("polymarket_strategy_discovery: done",
		slog.Int("screened", res.Screened),
		slog.Int("deployed", len(res.Deployed)),
		slog.Int("skipped", res.Skipped),
	)
	return nil
}
