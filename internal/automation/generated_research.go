package automation

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

const generatedResearchBatchLimit = 20

var generatedResearchSpec = scheduler.ScheduleSpec{
	Type: scheduler.ScheduleTypeCron, Cron: "10 6 * * 2-6", SkipWeekends: false, SkipHolidays: false,
}

func (o *JobOrchestrator) registerGeneratedResearchJob() {
	if !o.stockDiscoveryReady() {
		return
	}
	if o.deps.GeneratedResearch == nil || o.deps.GeneratedResearchPreparation == nil || o.deps.CanonicalAccountID == uuid.Nil || o.deps.DiscoveryScopeID == uuid.Nil {
		o.unavailableJobs = append(o.unavailableJobs, UnavailableJob{
			Name: "generated_research", Reason: "generated research requires canonical account, exact scope, immutable scenario preparation, and experiment runner",
		})
		return
	}
	o.Register("generated_research", "Execute exact-scope generated strategy experiments", generatedResearchSpec, func(ctx context.Context) error {
		now := o.currentTime().UTC().Truncate(time.Microsecond)
		summary, err := o.deps.GeneratedResearch.RunEligible(ctx, o.deps.CanonicalAccountID, o.deps.DiscoveryScopeID, generatedResearchBatchLimit, now)
		o.SetLastSummary("generated_research", map[string]int{
			"eligible": summary.Eligible, "completed": summary.Completed, "failed": summary.Failed,
		})
		if err != nil {
			return fmt.Errorf("generated_research: %w", err)
		}
		return nil
	}, "generated_research_prepare")
}
