package automation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

var polymarketDiscoverySpec = scheduler.ScheduleSpec{Type: scheduler.ScheduleTypeCron, Cron: "0 */6 * * *"}

func (o *JobOrchestrator) registerPolymarketDiscoveryJob() {
	if o.deps.LLMProvider == nil || o.deps.StrategyRepo == nil || o.deps.PolymarketDiscoveryRuns == nil {
		return
	}
	o.Register("polymarket_strategy_discovery",
		"Auto-generate Polymarket paper strategies from open markets",
		polymarketDiscoverySpec, o.polymarketDiscovery)
}

func (o *JobOrchestrator) polymarketDiscovery(ctx context.Context) error {
	if o.deps.PolymarketDiscoveryRuns == nil {
		return fmt.Errorf("polymarket_strategy_discovery: progress repo not configured; enable polymarket discovery run storage")
	}
	chunker := newPolymarketDiscoveryChunker(o.deps, o.logger)
	if err := chunker.RunChunk(ctx); err != nil {
		return err
	}
	run, err := o.deps.PolymarketDiscoveryRuns.GetActive(ctx)
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			return err
		}
		return nil
	}
	o.logger.Info("polymarket_strategy_discovery: chunk processed",
		slog.String("phase", string(run.Phase)),
		slog.String("status", string(run.Status)),
		slog.Int("candidate_index", run.CandidateIndex),
		slog.Int("accepted", len(run.Accepted)),
		slog.Int("deployed", len(run.Deployed)),
	)
	return nil
}
