package domain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestParseActivePromotionExecutionLineage(t *testing.T) {
	accountID := uuid.New()
	want := PromotionExecutionLineage{
		Stage: "shadow", Activation: "promotion_evaluator_v1", DeploymentID: uuid.New(), PromotionDecisionID: uuid.New(),
		EvaluationScopeID: uuid.New(), AccountID: accountID, ManifestID: uuid.New(), QualityResultID: uuid.New(),
		CapitalBindingID: uuid.New(), DeploymentBudgetUSD: 2500, RiskPolicyVersion: "portfolio-risk-policy-v1@sha256:" + strings.Repeat("a", 64),
	}
	raw, err := json.Marshal(map[string]any{"research_lifecycle": want})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseActivePromotionExecutionLineage(raw, accountID)
	if err != nil || got != want {
		t.Fatalf("ParseActivePromotionExecutionLineage() = %+v, %v; want %+v", got, err, want)
	}
}

func TestParseActivePromotionExecutionLineageRejectsInactiveOrWrongAccount(t *testing.T) {
	accountID := uuid.New()
	base := PromotionExecutionLineage{
		Stage: "shadow", Activation: "promotion_evaluator_v1", DeploymentID: uuid.New(), PromotionDecisionID: uuid.New(),
		EvaluationScopeID: uuid.New(), AccountID: accountID, ManifestID: uuid.New(), QualityResultID: uuid.New(),
		CapitalBindingID: uuid.New(), DeploymentBudgetUSD: 1, RiskPolicyVersion: "policy",
	}
	for _, test := range []struct {
		name      string
		lineage   PromotionExecutionLineage
		accountID uuid.UUID
	}{
		{name: "held", lineage: func() PromotionExecutionLineage { value := base; value.Stage = "held"; return value }(), accountID: accountID},
		{name: "wrong account", lineage: base, accountID: uuid.New()},
		{name: "missing manifest", lineage: func() PromotionExecutionLineage { value := base; value.ManifestID = uuid.Nil; return value }(), accountID: accountID},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"research_lifecycle": test.lineage})
			if _, err := ParseActivePromotionExecutionLineage(raw, test.accountID); err == nil {
				t.Fatal("ParseActivePromotionExecutionLineage() error = nil")
			}
		})
	}
}
