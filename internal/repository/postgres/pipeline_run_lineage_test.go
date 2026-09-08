package postgres

import (
	"testing"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestValidatePipelineRunPromotionLineage(t *testing.T) {
	versionID := uuid.New()
	run := &domain.PipelineRun{
		Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(),
		ExecutionVersionID: versionID, EvaluationScopeID: uuid.New(), ManifestID: uuid.New(), QualityResultID: uuid.New(),
		DeploymentID: uuid.New(), PromotionDecisionID: uuid.New(), CapitalBindingID: uuid.New(), RiskPolicyVersion: "policy-v1",
	}
	if err := validatePipelineRunPromotionLineage(run); err != nil {
		t.Fatalf("validatePipelineRunPromotionLineage() error = %v", err)
	}
	run.ManifestID = uuid.Nil
	if err := validatePipelineRunPromotionLineage(run); err == nil {
		t.Fatal("partial promotion lineage was accepted")
	}
}

func TestValidatePipelineRunPromotionLineageAllowsLegacyUnscopedRun(t *testing.T) {
	if err := validatePipelineRunPromotionLineage(&domain.PipelineRun{}); err != nil {
		t.Fatalf("legacy run rejected: %v", err)
	}
}
