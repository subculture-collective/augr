package automation

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/kalshi"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	pgrepo "github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

// A checkpoint is needed even before the first fill, and must be refreshed when
// no new fills or marks arrive. This rebuild uses the runtime worker's existing
// mark policy; it does not manufacture marks or make reconciliation pass.
func (o *JobOrchestrator) registerProjectionRefreshJob() {
	binding := o.deps.ExecutionAccount
	if binding.Validate() != nil || binding.AccountID() != o.deps.CanonicalAccountID || binding.Environment() != domain.AccountEnvironmentPaperScored {
		return
	}
	if o.deps.KalshiProjectionRepo == nil || o.deps.KalshiProjectionOutbox == nil || o.deps.KalshiMarkMaxAge <= 0 {
		return
	}
	o.Register("portfolio_projection_refresh", "Refresh canonical paper portfolio checkpoint even without new fills or marks", scheduler.ScheduleSpec{
		Type: scheduler.ScheduleTypeCron, Cron: "* * * * *",
	}, func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		// One stable identity per minute lets concurrent/retried occurrences
		// converge through the repository's immutable checkpoint contract.
		asOf := o.currentTime().UTC().Truncate(time.Minute)
		accountID := binding.AccountID()
		frontier, err := o.deps.KalshiProjectionOutbox.LatestProjectionFrontier(ctx, accountID, asOf)
		if err != nil {
			return fmt.Errorf("portfolio_projection_refresh: resolve frontier: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		projection, err := o.deps.KalshiProjectionRepo.RebuildPortfolioProjection(ctx, ledger.ProjectionRequest{
			AccountID: accountID, ThroughTransactionID: frontier, AsOf: asOf,
			MarkSource: kalshi.KalshiMarkSource, MarkNamespace: kalshi.KalshiAccountMarkNamespace(accountID), MaxMarkAge: o.deps.KalshiMarkMaxAge,
		})
		if err != nil {
			return fmt.Errorf("portfolio_projection_refresh: rebuild: %w", err)
		}
		summary := map[string]int{"accounts_rebuilt": 1}
		reconciled, err := o.recordInternalLedgerReconciliation(ctx, accountID, projection, asOf)
		if err != nil {
			return err
		}
		if reconciled {
			summary["internal_reconciliations"] = 1
		}
		o.SetLastSummary("portfolio_projection_refresh", summary)
		return nil
	})
}

// recordInternalLedgerReconciliation attests the refreshed checkpoint for the
// internal ledger venue so promotion readiness sees a passing, fresh
// reconciliation bound to the checkpoint. Broker-backed accounts are rejected
// by the recorder and keep their fail-closed reconciliation requirement; that
// rejection is logged rather than failing the refresh.
func (o *JobOrchestrator) recordInternalLedgerReconciliation(ctx context.Context, accountID uuid.UUID, projection *ledger.PortfolioProjection, asOf time.Time) (bool, error) {
	recorder, ok := o.deps.KalshiProjectionRepo.(pgrepo.InternalLedgerReconciliationRecorder)
	if !ok || projection == nil || projection.CheckpointID == uuid.Nil {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	now := o.currentTime().UTC()
	if now.Before(asOf) {
		now = asOf
	}
	run, err := recorder.RecordInternalLedgerReconciliation(ctx, accountID, projection.CheckpointID, now)
	if err != nil {
		o.logger.Warn("portfolio_projection_refresh: internal ledger reconciliation not recorded", slog.String("error", err.Error()))
		return false, nil
	}
	if !run.Clean {
		o.logger.Warn("portfolio_projection_refresh: internal ledger reconciliation is not clean", slog.String("run_id", run.ID.String()), slog.Int("incidents", len(run.Incidents)))
	}
	return true, nil
}
