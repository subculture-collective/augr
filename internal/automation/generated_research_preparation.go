package automation

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

const generatedResearchPreparationBatchLimit = 20

var generatedResearchPreparationSpec = scheduler.ScheduleSpec{
	Type: scheduler.ScheduleTypeCron, Cron: "5 6 * * 2-6", SkipWeekends: false, SkipHolidays: false,
}

func (o *JobOrchestrator) registerGeneratedResearchPreparationJob() {
	if !o.stockDiscoveryReady() {
		return
	}
	if o.deps.GeneratedResearchPreparation == nil || o.deps.CanonicalAccountID == uuid.Nil || o.deps.DiscoveryScopeID == uuid.Nil {
		o.unavailableJobs = append(o.unavailableJobs, UnavailableJob{
			Name: "generated_research_prepare", Reason: "generated research preparation requires canonical account, exact scope, and immutable scenario preparer",
		})
		return
	}
	o.Register("generated_research_prepare", "Prepare exact-scope generated strategy scenarios", generatedResearchPreparationSpec, func(ctx context.Context) error {
		summary, err := o.deps.GeneratedResearchPreparation.RunEligible(ctx, o.deps.CanonicalAccountID, o.deps.DiscoveryScopeID, generatedResearchPreparationBatchLimit)
		o.SetLastSummary("generated_research_prepare", map[string]int{
			"eligible": summary.Eligible, "completed": summary.Completed, "failed": summary.Failed,
		})
		if err != nil {
			return fmt.Errorf("generated_research_prepare: %w", err)
		}
		return nil
	}, "generated_proposal")
}
