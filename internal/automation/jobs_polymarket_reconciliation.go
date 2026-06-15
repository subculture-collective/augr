package automation

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

var polymarketReconcileSpec = scheduler.ScheduleSpec{
	Type: scheduler.ScheduleTypeCron,
	Cron: "*/10 * * * *",
}

func (o *JobOrchestrator) registerPolymarketReconciliationJobs() {
	o.Register(
		"polymarket_reconcile",
		"Audit Polymarket broker positions against local open positions",
		polymarketReconcileSpec,
		o.polymarketReconcile,
	)
}

func (o *JobOrchestrator) polymarketReconcile(ctx context.Context) error {
	if o.deps.PolymarketReconciler == nil {
		o.logger.Info("polymarket_reconcile: skipped — reconciler not configured")
		return nil
	}

	o.logger.Info("polymarket_reconcile: starting")
	summary, err := o.deps.PolymarketReconciler.Reconcile(ctx)
	if err != nil {
		return fmt.Errorf("polymarket_reconcile: %w", err)
	}
	o.SetLastSummary("polymarket_reconcile", summary.Map())
	o.logger.Info("polymarket_reconcile: complete",
		slog.Int("broker_positions", summary.BrokerPositions),
		slog.Int("local_positions", summary.LocalPositions),
		slog.Int("drifts", summary.Drifts),
	)
	return nil
}
