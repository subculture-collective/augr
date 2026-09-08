package automation

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

const generatedEvaluationBatchLimit = 20

var generatedEvaluationSpec = scheduler.ScheduleSpec{
	Type: scheduler.ScheduleTypeCron, Cron: "20 6 * * 2-6", SkipWeekends: false, SkipHolidays: false,
}

func (o *JobOrchestrator) registerGeneratedEvaluationJob() {
	if !o.stockDiscoveryReady() {
		return
	}
	if o.deps.GeneratedEvaluation == nil || o.deps.CanonicalAccountID == uuid.Nil || o.deps.DiscoveryScopeID == uuid.Nil {
		o.unavailableJobs = append(o.unavailableJobs, UnavailableJob{
			Name: "generated_evaluation", Reason: "generated evaluation requires canonical account, exact scope, and reconstructable result evidence",
		})
		return
	}
	o.Register("generated_evaluation", "Evaluate exact generated experiment results", generatedEvaluationSpec, func(ctx context.Context) error {
		summary, err := o.deps.GeneratedEvaluation.RunEligible(ctx, o.deps.CanonicalAccountID, o.deps.DiscoveryScopeID, generatedEvaluationBatchLimit)
		o.SetLastSummary("generated_evaluation", map[string]int{
			"eligible": summary.Eligible, "completed": summary.Completed, "failed": summary.Failed,
		})
		if err != nil {
			return fmt.Errorf("generated_evaluation: %w", err)
		}
		return nil
	}, "generated_research")
}
