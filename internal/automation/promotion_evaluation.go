package automation

import (
	"context"
	"fmt"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/promotion"
	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
	"github.com/google/uuid"
)

const promotionOperationalEvidenceMaxAge = 5 * time.Minute

func (o *JobOrchestrator) registerPromotionEvaluationJob() {
	if o.deps.DiscoveryScopeID == uuid.Nil {
		return
	}
	if o.deps.PromotionEvaluation == nil || o.deps.PromotionAccountSource == nil || o.deps.PromotionProjectionSource == nil ||
		o.deps.PromotionEvidenceSource == nil || o.deps.CanonicalAccountID == uuid.Nil {
		o.unavailableJobs = append(o.unavailableJobs, UnavailableJob{Name: "promotion_evaluation", Reason: "promotion evaluation requires canonical account, exact scope, projection, and immutable evidence readers"})
		return
	}
	o.Register("promotion_evaluation", "Evaluate exact-scope deployments into authoritative promotion decisions", scheduler.ScheduleSpec{
		Type: scheduler.ScheduleTypeCron, Cron: "2-59/5 * * * *",
	}, o.runPromotionEvaluation)
}

func (o *JobOrchestrator) runPromotionEvaluation(ctx context.Context) error {
	accountID, scopeID := o.deps.CanonicalAccountID, o.deps.DiscoveryScopeID
	generatedAt := time.Now().UTC()
	account, err := o.deps.PromotionAccountSource.GetByID(ctx, accountID)
	if err != nil {
		return fmt.Errorf("promotion_evaluation: load canonical account: %w", err)
	}
	inventory, err := o.deps.PromotionEvidenceSource.GetCutoverEvidenceInventoryForScope(ctx, accountID, scopeID)
	if err != nil {
		return fmt.Errorf("promotion_evaluation: load exact-scope evidence: %w", err)
	}
	if inventory == nil {
		return fmt.Errorf("promotion_evaluation: exact-scope evidence is unavailable")
	}
	snapshot, err := o.deps.PromotionProjectionSource.GetLatestPortfolioProjection(ctx, accountID, generatedAt)
	if err != nil {
		return fmt.Errorf("promotion_evaluation: load canonical projection: %w", err)
	}
	if snapshot == nil || snapshot.Checkpoint == nil || snapshot.Valuation == nil {
		return fmt.Errorf("promotion_evaluation: canonical projection is incomplete")
	}
	unavailable := make([]string, 0, 1)
	if snapshot.ProjectionWorkPending != 0 || snapshot.ProjectionWorkProcessing != 0 || snapshot.ProjectionWorkRetrying != 0 || snapshot.ProjectionWorkDegraded != 0 {
		unavailable = append(unavailable, "projection_work_not_terminal")
	}
	readiness := promotion.EvaluateReadiness(promotion.ReadinessInput{
		ConfiguredAccountID: accountID, Account: account, ScopeID: scopeID, ScopeAccountID: inventory.ScopeAccountID,
		EvidenceImmutable: inventory.ScopedArtifacts > 0, ScopedArtifacts: inventory.ScopedArtifacts, LegacyArtifacts: inventory.LegacyArtifacts,
		ScopeMismatchCount: inventory.ScopeMismatchCount, MissingCanonicalLinks: inventory.MissingCanonicalLinks,
		OpenLots: snapshot.FreshMarks + snapshot.StaleMarks + snapshot.UnavailableMarks, FreshMarks: snapshot.FreshMarks,
		StaleMarks: snapshot.StaleMarks, UnavailableMarks: snapshot.UnavailableMarks,
		ReconciliationAvailable: snapshot.ReconciliationAvailable, ReconciliationPassed: snapshot.ReconciliationPassed,
		ReconciliationAccountID: snapshot.ReconciliationAccountID, ReconciliationVenue: snapshot.ReconciliationProvider,
		ReconciliationExternalAccountID: snapshot.ReconciliationExternalAccountID,
		GeneratedAt:                     generatedAt, CheckpointGeneratedAt: snapshot.Checkpoint.AsOf, ReconciliationGeneratedAt: snapshot.ReconciliationGeneratedAt,
		CheckpointMaxAge: promotionOperationalEvidenceMaxAge, ReconciliationMaxAge: promotionOperationalEvidenceMaxAge, UnavailableReasons: unavailable,
	})
	if !readiness.Ready() {
		o.SetLastSummary("promotion_evaluation", map[string]int{"ready": 0, "blocked_reasons": len(readiness.BlockReasons())})
		return fmt.Errorf("promotion_evaluation: readiness blocked: %v", readiness.BlockReasons())
	}
	summary, err := o.deps.PromotionEvaluation.EvaluateEligiblePromotions(ctx, accountID, scopeID, readiness)
	o.SetLastSummary("promotion_evaluation", map[string]int{"ready": 1, "eligible": summary.Eligible, "approved": summary.Approved, "held": summary.Held})
	return err
}
