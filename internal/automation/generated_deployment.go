package automation

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

const generatedDeploymentBatchLimit = 20

func (o *JobOrchestrator) registerGeneratedDeploymentJob() {
	if !o.stockDiscoveryReady() {
		return
	}
	if o.deps.GeneratedDeployment == nil || o.deps.CanonicalAccountID == uuid.Nil || o.deps.DiscoveryScopeID == uuid.Nil {
		o.unavailableJobs = append(o.unavailableJobs, UnavailableJob{
			Name: "generated_deployment", Reason: "generated deployment requires canonical account, exact scope, and completed reviewed robustness",
		})
		return
	}
	o.Register("generated_deployment", "Propose reviewed generated strategies for promotion", scheduler.ScheduleSpec{
		Type: scheduler.ScheduleTypeCron, Cron: "30 6 * * 2-6", SkipWeekends: false, SkipHolidays: false,
	}, func(ctx context.Context) error {
		summary, err := o.deps.GeneratedDeployment.RunEligible(ctx, o.deps.CanonicalAccountID, o.deps.DiscoveryScopeID, generatedDeploymentBatchLimit)
		o.SetLastSummary("generated_deployment", map[string]int{
			"eligible": summary.Eligible, "completed": summary.Completed, "failed": summary.Failed,
		})
		if err != nil {
			return fmt.Errorf("generated_deployment: %w", err)
		}
		return nil
	}, "generated_robustness")
}
