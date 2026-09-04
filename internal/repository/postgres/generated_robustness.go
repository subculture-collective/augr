package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/PatrickFanella/get-rich-quick/internal/evaluation"
	"github.com/PatrickFanella/get-rich-quick/internal/generativestrategy"
	"github.com/PatrickFanella/get-rich-quick/internal/robustness"
	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

var _ generativestrategy.EligibleRobustnessSource = (*GenerativeStrategyRepo)(nil)

// ListEligibleGeneratedRobustness returns a candidate only after all four
// exact reviewed fold reports exist. Partial evidence remains pending; extra,
// duplicate, or unreviewed evidence fails closed.
func (r *GenerativeStrategyRepo) ListEligibleGeneratedRobustness(
	ctx context.Context,
	accountID, scopeID uuid.UUID,
	limit int,
) ([]generativestrategy.EligibleRobustnessAssessment, error) {
	if r == nil || r.pool == nil || accountID == uuid.Nil || scopeID == uuid.Nil || limit <= 0 || limit > generativestrategy.MaximumResearchBatchSize {
		return nil, fmt.Errorf("postgres: exact generated robustness account, scope, and bounded limit are required")
	}
	report, err := NewReportArtifactRepo(r.pool).DiscoveryDeploymentReadinessForScope(ctx, scopeID, accountID)
	if err != nil {
		return nil, fmt.Errorf("postgres: generated robustness scope: %w", err)
	}
	if !report.Stock.Ready {
		return nil, fmt.Errorf("postgres: generated robustness stock evidence: %s", report.Stock.Reason)
	}
	reviewedFamily, err := generativestrategy.ReviewedDailyStockFamily()
	if err != nil {
		return nil, err
	}
	expectedSpecKey := "daily_stock_" + strings.ReplaceAll(scopeID.String(), "-", "")
	var specID, versionID uuid.UUID
	var baselineSimulationPolicyVersion string
	err = r.pool.QueryRow(ctx, `SELECT spec.id,receipt.version_id,simulation.policy_version
		FROM paper_evaluation_scopes scope
		JOIN dataset_manifests manifest ON manifest.sha256=scope.manifest_sha256
		JOIN dataset_quality_results quality ON quality.manifest_id=manifest.id AND quality.sha256=scope.quality_sha256 AND NOT quality.quarantined
		JOIN simulation_policy_artifacts simulation ON simulation.sha256=scope.simulation_policy_sha256
		JOIN account_capital_policy_bindings binding ON binding.id=scope.capital_binding_id AND binding.account_id=scope.account_id
		JOIN capital_margin_policy_artifacts capital ON capital.id=binding.policy_artifact_id AND capital.sha256=scope.capital_policy_sha256
		JOIN generated_strategy_specs spec ON spec.spec_key=$3 AND spec.family_id=$4
		JOIN generated_strategy_compilation_receipts receipt ON receipt.spec_id=spec.id
		WHERE scope.id=$1 AND scope.account_id=$2
		  AND NOT EXISTS(SELECT 1 FROM statistical_robustness_assessments assessment
		    JOIN robustness_assessment_candidates candidate ON candidate.assessment_id=assessment.id AND candidate.version_id=receipt.version_id
		    WHERE assessment.scope_id=scope.id)`, scopeID, accountID, expectedSpecKey, reviewedFamily.ID()).Scan(
		&specID, &versionID, &baselineSimulationPolicyVersion,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: load generated robustness candidate: %w", err)
	}
	spec, compiledVersion, _, err := r.GetCompilation(ctx, specID)
	if err != nil {
		return nil, fmt.Errorf("postgres: load generated robustness compilation: %w", err)
	}
	if compiledVersion.ID() != versionID || spec.SpecKey() != expectedSpecKey || spec.FamilyID() != reviewedFamily.ID() {
		return nil, fmt.Errorf("postgres: generated robustness compilation is outside the reviewed scope")
	}
	folds, err := generativestrategy.PlanReviewedResearchFolds(report.EvaluationStart, report.EvaluationEnd)
	if err != nil {
		return nil, err
	}
	type reportCandidate struct {
		reportID, experimentID, scenarioID uuid.UUID
	}
	rows, err := r.pool.Query(ctx, `SELECT evaluation.id,experiment.id,scenario.id
		FROM paper_evaluation_scopes scope
		JOIN dataset_manifests manifest ON manifest.sha256=scope.manifest_sha256
		JOIN dataset_quality_results quality ON quality.manifest_id=manifest.id AND quality.sha256=scope.quality_sha256 AND NOT quality.quarantined
		JOIN account_capital_policy_bindings binding ON binding.id=scope.capital_binding_id AND binding.account_id=scope.account_id
		JOIN capital_margin_policy_artifacts capital ON capital.id=binding.policy_artifact_id AND capital.sha256=scope.capital_policy_sha256
		JOIN research_experiments experiment ON experiment.version_id=$3 AND experiment.account_id=scope.account_id
		  AND experiment.capital_binding_id=binding.id AND experiment.manifest_id=manifest.id AND experiment.quality_result_id=quality.id
		  AND experiment.capital_policy_version=capital.policy_version AND experiment.mode='paper_scored' AND NOT experiment.dataset_quarantined
		JOIN generated_strategy_scenarios scenario ON scenario.spec_id=$4 AND scenario.manifest_id=manifest.id
		  AND scenario.mode=experiment.mode AND scenario.evaluation_start=experiment.evaluation_start AND scenario.evaluation_end=experiment.evaluation_end
		JOIN trade_portfolio_evaluations evaluation ON evaluation.experiment_id=experiment.id AND evaluation.account_id=scope.account_id
		  AND evaluation.manifest_id=manifest.id AND evaluation.quality_result_id=quality.id AND evaluation.mode=experiment.mode
		WHERE scope.id=$1 AND scope.account_id=$2
		ORDER BY experiment.evaluation_start,experiment.simulation_policy_version,evaluation.id`, scopeID, accountID, versionID, specID)
	if err != nil {
		return nil, fmt.Errorf("postgres: list generated robustness reports: %w", err)
	}
	defer rows.Close()
	slots := make(map[string]*evaluation.Report, 4)
	evaluationRepo := NewEvaluationRepo(r.pool)
	for rows.Next() {
		var candidate reportCandidate
		if err := rows.Scan(&candidate.reportID, &candidate.experimentID, &candidate.scenarioID); err != nil {
			return nil, fmt.Errorf("postgres: scan generated robustness report: %w", err)
		}
		prepared, err := r.restorePreparedResearch(ctx, candidate.experimentID, candidate.scenarioID)
		if err != nil {
			return nil, err
		}
		if err := r.validateReviewedPreparedResearch(ctx, report, scopeID, baselineSimulationPolicyVersion, prepared); err != nil {
			return nil, err
		}
		foldIndex := reviewedFoldIndex(folds, prepared.Experiment.EvaluationStart(), prepared.Experiment.EvaluationEnd())
		if foldIndex < 0 {
			return nil, fmt.Errorf("postgres: generated robustness report has an unreviewed fold")
		}
		variant := "cost_up"
		if prepared.Experiment.SimulationPolicyVersion() == baselineSimulationPolicyVersion {
			variant = "baseline"
		}
		key := fmt.Sprintf("%d/%s", foldIndex, variant)
		if slots[key] != nil {
			return nil, fmt.Errorf("postgres: generated robustness slot %s is duplicated", key)
		}
		evaluationReport, err := evaluationRepo.GetEvaluation(ctx, candidate.reportID)
		if err != nil {
			return nil, fmt.Errorf("postgres: load generated robustness report: %w", err)
		}
		if evaluationReport.ExperimentID() != candidate.experimentID || evaluationReport.AccountID() != accountID ||
			!evaluationReport.EvaluationStart().Equal(folds[foldIndex].TestStart) || !evaluationReport.EvaluationEnd().Equal(folds[foldIndex].TestEnd) {
			return nil, fmt.Errorf("postgres: generated robustness report identity does not reconstruct")
		}
		slots[key] = evaluationReport
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list generated robustness reports: %w", err)
	}
	if len(slots) < 4 {
		return nil, nil
	}
	policy, err := robustness.ReviewedPolicyV1()
	if err != nil {
		return nil, err
	}
	family, err := robustness.NewFamily(robustness.FamilyInput{
		Name: "generated/" + expectedSpecKey, HypothesisSHA256: spec.Digest(), CandidateVersionIDs: []uuid.UUID{versionID},
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: build generated robustness family: %w", err)
	}
	candidateFolds := make([]robustness.FoldInput, len(folds))
	for index, fold := range folds {
		candidateFolds[index] = robustness.FoldInput{
			TrainStart: fold.TrainStart, TrainEnd: fold.TrainEnd,
			Baseline: slots[fmt.Sprintf("%d/baseline", index)],
			Perturbations: []robustness.ScenarioInput{{
				Kind: "cost_up", Severity: "double_simulation_fees", Report: slots[fmt.Sprintf("%d/cost_up", index)],
			}},
		}
	}
	return []generativestrategy.EligibleRobustnessAssessment{{
		Key: versionID.String(), Family: family, Policy: policy, ScopeID: scopeID, Mode: strategycatalog.ExperimentPaperScored,
		Candidates: []robustness.CandidateInput{{VersionID: versionID, Folds: candidateFolds}},
	}}, nil
}

func reviewedFoldIndex(folds []generativestrategy.ResearchFold, start, end time.Time) int {
	for index, fold := range folds {
		if start.Equal(fold.TestStart) && end.Equal(fold.TestEnd) {
			return index
		}
	}
	return -1
}
