package automation

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

const generatedProposalBatchLimit = 1

var generatedProposalSpec = scheduler.ScheduleSpec{
	Type: scheduler.ScheduleTypeCron, Cron: "0 6 * * 2-6", SkipWeekends: false, SkipHolidays: false,
}

func (o *JobOrchestrator) registerGeneratedProposalJob() {
	if !o.stockDiscoveryReady() {
		return
	}
	if o.deps.GeneratedProposal == nil || o.deps.CanonicalAccountID == uuid.Nil || o.deps.DiscoveryScopeID == uuid.Nil {
		o.unavailableJobs = append(o.unavailableJobs, UnavailableJob{
			Name: "generated_proposal", Reason: "generated proposals require canonical account, exact scope, immutable evidence, and typed model generator",
		})
		return
	}
	o.Register("generated_proposal", "Generate one typed proposal from exact immutable scope evidence", generatedProposalSpec, func(ctx context.Context) error {
		summary, err := o.deps.GeneratedProposal.RunEligible(ctx, o.deps.CanonicalAccountID, o.deps.DiscoveryScopeID, generatedProposalBatchLimit)
		o.SetLastSummary("generated_proposal", map[string]int{
			"eligible": summary.Eligible, "completed": summary.Completed, "failed": summary.Failed,
		})
		if err != nil {
			return fmt.Errorf("generated_proposal: %w", err)
		}
		return nil
	})
}
