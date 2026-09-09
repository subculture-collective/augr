package automation

import (
	"context"
	"fmt"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/kalshi"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
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
		_, err = o.deps.KalshiProjectionRepo.RebuildPortfolioProjection(ctx, ledger.ProjectionRequest{
			AccountID: accountID, ThroughTransactionID: frontier, AsOf: asOf,
			MarkSource: kalshi.KalshiMarkSource, MarkNamespace: kalshi.KalshiAccountMarkNamespace(accountID), MaxMarkAge: o.deps.KalshiMarkMaxAge,
		})
		if err != nil {
			return fmt.Errorf("portfolio_projection_refresh: rebuild: %w", err)
		}
		o.SetLastSummary("portfolio_projection_refresh", map[string]int{"accounts_rebuilt": 1})
		return nil
	})
}
