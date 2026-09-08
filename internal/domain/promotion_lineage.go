package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// PromotionExecutionLineage is the exact immutable authority projected into a
// scheduled runtime strategy. It is copied onto every run and opportunity.
type PromotionExecutionLineage struct {
	Stage               string    `json:"stage"`
	Activation          string    `json:"activation"`
	AutoBlocked         bool      `json:"auto_activation_blocked"`
	DeploymentID        uuid.UUID `json:"deployment_id"`
	PromotionDecisionID uuid.UUID `json:"promotion_decision_id"`
	EvaluationScopeID   uuid.UUID `json:"evaluation_scope_id"`
	AccountID           uuid.UUID `json:"account_id"`
	ManifestID          uuid.UUID `json:"manifest_id"`
	QualityResultID     uuid.UUID `json:"quality_result_id"`
	CapitalBindingID    uuid.UUID `json:"capital_binding_id"`
	DeploymentBudgetUSD float64   `json:"deployment_budget_usd"`
	RiskPolicyVersion   string    `json:"risk_policy_version"`
}

// ParseActivePromotionExecutionLineage reconstructs the projected lifecycle
// block and rejects incomplete, held, or manually activated configurations.
func ParseActivePromotionExecutionLineage(config json.RawMessage, accountID uuid.UUID) (PromotionExecutionLineage, error) {
	var envelope struct {
		ResearchLifecycle *PromotionExecutionLineage `json:"research_lifecycle"`
	}
	if err := json.Unmarshal(config, &envelope); err != nil {
		return PromotionExecutionLineage{}, fmt.Errorf("parse strategy promotion lifecycle: %w", err)
	}
	if envelope.ResearchLifecycle == nil {
		return PromotionExecutionLineage{}, errors.New("scheduled strategy lacks promotion lifecycle evidence")
	}
	lineage := *envelope.ResearchLifecycle
	if lineage.Stage != "shadow" || lineage.Activation != "promotion_evaluator_v1" || lineage.AutoBlocked || lineage.AccountID != accountID ||
		lineage.DeploymentID == uuid.Nil || lineage.PromotionDecisionID == uuid.Nil || lineage.EvaluationScopeID == uuid.Nil ||
		lineage.ManifestID == uuid.Nil || lineage.QualityResultID == uuid.Nil || lineage.CapitalBindingID == uuid.Nil ||
		lineage.DeploymentBudgetUSD <= 0 || strings.TrimSpace(lineage.RiskPolicyVersion) == "" {
		return PromotionExecutionLineage{}, errors.New("scheduled strategy promotion lifecycle is incomplete or inactive")
	}
	return lineage, nil
}
