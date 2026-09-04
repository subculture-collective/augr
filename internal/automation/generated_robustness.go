package automation

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

const generatedRobustnessBatchLimit = 20

var generatedRobustnessSpec = scheduler.ScheduleSpec{
	Type: scheduler.ScheduleTypeCron, Cron: "25 6 * * 2-6", SkipWeekends: false, SkipHolidays: false,
}

func (o *JobOrchestrator) registerGeneratedRobustnessJob() {
	if !o.stockDiscoveryReady() {
		return
	}
	if o.deps.GeneratedRobustness == nil || o.deps.CanonicalAccountID == uuid.Nil || o.deps.DiscoveryScopeID == uuid.Nil {
		o.unavailableJobs = append(o.unavailableJobs, UnavailableJob{
			Name: "generated_robustness", Reason: "generated robustness requires canonical account, exact scope, and four reviewed evaluation reports",
		})
		return
	}
	o.Register("generated_robustness", "Assess exact generated fold and cost-up evidence", generatedRobustnessSpec, func(ctx context.Context) error {
		summary, err := o.deps.GeneratedRobustness.RunEligible(ctx, o.deps.CanonicalAccountID, o.deps.DiscoveryScopeID, generatedRobustnessBatchLimit)
		o.SetLastSummary("generated_robustness", map[string]int{
			"eligible": summary.Eligible, "completed": summary.Completed, "failed": summary.Failed,
		})
		if err != nil {
			return fmt.Errorf("generated_robustness: %w", err)
		}
		return nil
	}, "generated_evaluation")
}
