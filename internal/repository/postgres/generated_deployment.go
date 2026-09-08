package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/generativestrategy"
	"github.com/PatrickFanella/get-rich-quick/internal/portfolio"
	"github.com/PatrickFanella/get-rich-quick/internal/robustness"
	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

const (
	generatedStockScheduleCron = "5 10 * * 1-5"
	generatedStockTimezone     = "America/New_York"
)

var _ generativestrategy.EligibleDeploymentSource = (*GenerativeStrategyRepo)(nil)

func (r *GenerativeStrategyRepo) ListEligibleGeneratedDeployments(
	ctx context.Context,
	accountID, scopeID uuid.UUID,
	limit int,
) ([]generativestrategy.EligibleDeploymentProposal, error) {
	if r == nil || r.pool == nil || accountID == uuid.Nil || scopeID == uuid.Nil || limit <= 0 || limit > generativestrategy.MaximumResearchBatchSize {
		return nil, fmt.Errorf("postgres: exact generated deployment account, scope, and bounded limit are required")
	}
	readiness, err := NewReportArtifactRepo(r.pool).DiscoveryDeploymentReadinessForScope(ctx, scopeID, accountID)
	if err != nil {
		return nil, fmt.Errorf("postgres: generated deployment scope: %w", err)
	}
	if !readiness.Stock.Ready {
		return nil, fmt.Errorf("postgres: generated deployment stock evidence: %s", readiness.Stock.Reason)
	}
	reviewedStrategyFamily, err := generativestrategy.ReviewedDailyStockFamily()
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT assessment.id,spec.id,receipt.version_id,scope.capital_binding_id,
		trim_scale(binding.starting_capital)::text,scope.created_at
		FROM paper_evaluation_scopes scope
		JOIN account_capital_policy_bindings binding ON binding.id=scope.capital_binding_id AND binding.account_id=scope.account_id
		JOIN generated_strategy_specs spec ON spec.family_id=$3
		JOIN generated_strategy_compilation_receipts receipt ON receipt.spec_id=spec.id
		JOIN robustness_assessment_candidates candidate ON candidate.version_id=receipt.version_id
		JOIN statistical_robustness_assessments assessment ON assessment.id=candidate.assessment_id
		  AND assessment.scope_id=scope.id AND assessment.mode='paper_scored' AND assessment.state='completed'
		WHERE scope.id=$1 AND scope.account_id=$2
		ORDER BY assessment.created_at,assessment.id
		LIMIT $4`, scopeID, accountID, reviewedStrategyFamily.ID(), limit+1)
	if err != nil {
		return nil, fmt.Errorf("postgres: list generated deployment assessments: %w", err)
	}
	defer rows.Close()
	type candidate struct {
		assessmentID, specID, versionID, capitalBindingID uuid.UUID
		startingCapital                                   string
		riskEffectiveAt                                   time.Time
	}
	candidates := make([]candidate, 0, limit)
	for rows.Next() {
		var value candidate
		if err := rows.Scan(&value.assessmentID, &value.specID, &value.versionID, &value.capitalBindingID, &value.startingCapital, &value.riskEffectiveAt); err != nil {
			return nil, fmt.Errorf("postgres: scan generated deployment assessment: %w", err)
		}
		candidates = append(candidates, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list generated deployment assessments: %w", err)
	}
	if len(candidates) > limit {
		return nil, fmt.Errorf("postgres: generated deployment candidate set exceeded limit")
	}
	riskPolicy, err := portfolio.ReviewedPortfolioRiskPolicyV1()
	if err != nil {
		return nil, err
	}
	robustnessPolicy, err := robustness.ReviewedPolicyV1()
	if err != nil {
		return nil, err
	}
	items := make([]generativestrategy.EligibleDeploymentProposal, 0, len(candidates))
	for _, candidate := range candidates {
		spec, version, _, err := r.GetCompilation(ctx, candidate.specID)
		if err != nil {
			return nil, fmt.Errorf("postgres: load generated deployment compilation: %w", err)
		}
		if generativestrategy.ValidateReviewedDailyStockSpec(spec, scopeID) != nil || spec.FamilyID() != reviewedStrategyFamily.ID() || version.ID() != candidate.versionID {
			return nil, fmt.Errorf("postgres: generated deployment compilation is outside the reviewed scope")
		}
		expectedRobustnessFamily, err := robustness.NewFamily(robustness.FamilyInput{
			Name: "generated/" + spec.SpecKey(), HypothesisSHA256: spec.Digest(), CandidateVersionIDs: []uuid.UUID{candidate.versionID},
		})
		if err != nil {
			return nil, err
		}
		assessment, err := NewRobustnessRepo(r.pool).GetAssessment(ctx, candidate.assessmentID)
		if err != nil {
			return nil, fmt.Errorf("postgres: load generated deployment assessment: %w", err)
		}
		assessmentCandidates := assessment.Candidates()
		if assessment.ScopeID() != scopeID || assessment.Mode() != strategycatalog.ExperimentPaperScored || assessment.PolicyID() != robustnessPolicy.ID() ||
			assessment.FamilyID() != expectedRobustnessFamily.ID() || len(assessmentCandidates) != 1 || assessmentCandidates[0].VersionID != candidate.versionID.String() {
			return nil, fmt.Errorf("postgres: generated deployment assessment is not the exact reviewed assessment")
		}
		capitalValue, err := decimal.NewFromString(candidate.startingCapital)
		if err != nil || !capitalValue.IsPositive() {
			return nil, fmt.Errorf("postgres: generated deployment capital is invalid")
		}
		budget := capitalValue.Mul(decimal.RequireFromString("0.02")).String()
		deployment, err := strategycatalog.NewDeployment(strategycatalog.DeploymentInput{
			VersionID: candidate.versionID, AccountID: accountID, CapitalBindingID: candidate.capitalBindingID,
			Budget: budget, ScheduleCron: generatedStockScheduleCron, Timezone: generatedStockTimezone,
			RiskPolicyVersion: riskPolicy.Reference(), Mode: strategycatalog.ExperimentPaperScored,
		})
		if err != nil {
			return nil, fmt.Errorf("postgres: construct generated deployment: %w", err)
		}
		var exactExists bool
		var deploymentCount int
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM strategy_deployments WHERE id=$1),
			count(*) FROM strategy_deployments WHERE version_id=$2 AND account_id=$3`, deployment.ID(), candidate.versionID, accountID).Scan(&exactExists, &deploymentCount); err != nil {
			return nil, fmt.Errorf("postgres: inspect generated deployment proposals: %w", err)
		}
		if exactExists {
			continue
		}
		if deploymentCount != 0 {
			return nil, fmt.Errorf("postgres: generated version has a conflicting deployment proposal")
		}
		items = append(items, generativestrategy.EligibleDeploymentProposal{
			Key: candidate.assessmentID.String(), ScopeID: scopeID, RiskPolicy: riskPolicy,
			RiskEffectiveAt: candidate.riskEffectiveAt.UTC().Truncate(time.Microsecond), Deployment: deployment,
		})
	}
	return items, nil
}
