package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/capital"
	"github.com/PatrickFanella/get-rich-quick/internal/evaluation"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/lifecycle"
	"github.com/PatrickFanella/get-rich-quick/internal/generativestrategy"
)

type GeneratedEvaluationSource struct {
	pool     *pgxpool.Pool
	evidence *GeneratedExperimentEvidenceLoader
}

var _ generativestrategy.EligibleEvaluationSource = (*GeneratedEvaluationSource)(nil)

func NewGeneratedEvaluationSource(pool *pgxpool.Pool, evidence *GeneratedExperimentEvidenceLoader) (*GeneratedEvaluationSource, error) {
	if pool == nil || evidence == nil || evidence.pool != pool {
		return nil, fmt.Errorf("postgres: generated evaluation requires one database-bound experiment evidence loader")
	}
	return &GeneratedEvaluationSource{pool: pool, evidence: evidence}, nil
}

func (source *GeneratedEvaluationSource) ListEligibleGeneratedEvaluations(ctx context.Context, accountID, scopeID uuid.UUID, limit int) ([]generativestrategy.EligibleEvaluation, error) {
	if source == nil || source.pool == nil || source.evidence == nil || accountID == uuid.Nil || scopeID == uuid.Nil || limit <= 0 || limit > generativestrategy.MaximumResearchBatchSize {
		return nil, fmt.Errorf("postgres: exact generated evaluation account, scope, and limit are required")
	}
	rows, err := source.pool.Query(ctx, `
		SELECT result.id,experiment.id,scenario.id
		FROM paper_evaluation_scopes scope
		JOIN dataset_manifests manifest ON manifest.sha256=scope.manifest_sha256
		JOIN dataset_quality_results quality ON quality.manifest_id=manifest.id AND quality.sha256=scope.quality_sha256 AND NOT quality.quarantined
		JOIN simulation_policy_artifacts simulation ON simulation.sha256=scope.simulation_policy_sha256
		JOIN account_capital_policy_bindings binding ON binding.id=scope.capital_binding_id AND binding.account_id=scope.account_id
		JOIN capital_margin_policy_artifacts capital ON capital.id=binding.policy_artifact_id AND capital.sha256=scope.capital_policy_sha256
		JOIN research_experiments experiment ON experiment.account_id=scope.account_id AND experiment.capital_binding_id=binding.id
			AND experiment.manifest_id=manifest.id AND experiment.quality_result_id=quality.id
			AND experiment.simulation_policy_version=simulation.policy_version AND experiment.capital_policy_version=capital.policy_version
			AND experiment.evaluation_start=scope.evaluation_start AND experiment.evaluation_end=scope.evaluation_end AND NOT experiment.dataset_quarantined
		JOIN generated_strategy_compilation_receipts receipt ON receipt.version_id=experiment.version_id
		JOIN generated_strategy_scenarios scenario ON scenario.spec_id=receipt.spec_id AND scenario.manifest_id=manifest.id
			AND scenario.mode=experiment.mode AND scenario.evaluation_start=experiment.evaluation_start AND scenario.evaluation_end=experiment.evaluation_end
		JOIN experiment_run_results result ON result.experiment_id=experiment.id AND result.account_id=scope.account_id
			AND result.manifest_id=manifest.id AND result.quality_result_id=quality.id AND result.mode=experiment.mode
		WHERE scope.id=$1 AND scope.account_id=$2
			AND NOT EXISTS(SELECT 1 FROM trade_portfolio_evaluations report WHERE report.result_id=result.id)
		ORDER BY result.created_at,result.id,scenario.id
		LIMIT $3`, scopeID, accountID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("postgres: list exact generated evaluations: %w", err)
	}
	defer rows.Close()
	type candidate struct{ resultID, experimentID, scenarioID uuid.UUID }
	candidates := make([]candidate, 0, limit)
	seen := make(map[uuid.UUID]uuid.UUID, limit)
	for rows.Next() {
		var value candidate
		if err := rows.Scan(&value.resultID, &value.experimentID, &value.scenarioID); err != nil {
			return nil, fmt.Errorf("postgres: scan exact generated evaluation: %w", err)
		}
		if prior, duplicate := seen[value.resultID]; duplicate {
			if prior != value.scenarioID {
				return nil, fmt.Errorf("postgres: generated result %s has ambiguous scenarios", value.resultID)
			}
			continue
		}
		seen[value.resultID] = value.scenarioID
		candidates = append(candidates, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list exact generated evaluations: %w", err)
	}
	if len(candidates) > limit {
		return nil, fmt.Errorf("postgres: generated evaluation candidate set exceeded limit")
	}
	policy, err := evaluation.ReviewedPolicyV1()
	if err != nil {
		return nil, err
	}
	runRepo := NewExperimentRunRepo(source.pool)
	generatedRepo := NewGenerativeStrategyRepo(source.pool)
	items := make([]generativestrategy.EligibleEvaluation, 0, len(candidates))
	for _, candidate := range candidates {
		prepared, err := generatedRepo.restorePreparedResearch(ctx, candidate.experimentID, candidate.scenarioID)
		if err != nil {
			return nil, err
		}
		result, err := runRepo.GetResult(ctx, candidate.resultID)
		if err != nil {
			return nil, fmt.Errorf("postgres: load generated evaluation result: %w", err)
		}
		plan, err := runRepo.GetPlan(ctx, result.PlanID())
		if err != nil {
			return nil, fmt.Errorf("postgres: load generated evaluation plan: %w", err)
		}
		graph, err := source.evidence.LoadExperimentReferenceEvidence(ctx, candidate.experimentID)
		if err != nil {
			return nil, fmt.Errorf("postgres: load generated evaluation evidence: %w", err)
		}
		sealedState, err := capital.StateFromCanonical(
			plan.CapitalStateID(), plan.CapitalStateSHA256(), plan.CapitalStateBytes(), *graph.Account, *graph.CapitalBinding, graph.CapitalPolicy,
		)
		if err != nil {
			return nil, fmt.Errorf("postgres: restore generated evaluation plan capital state: %w", err)
		}
		if sealedState.ProjectionCheckpointID() != plan.CapitalProjectionCheckpointID() {
			return nil, fmt.Errorf("postgres: generated evaluation plan capital checkpoint differs")
		}
		graph.CapitalState = sealedState
		lifecycles := make(map[uuid.UUID]*lifecycle.Aggregate)
		for sequence, step := range plan.Steps() {
			if step.Intent == nil {
				continue
			}
			intentID := plan.IntentID(sequence)
			aggregate, loadErr := runRepo.GetExecutionLifecycle(ctx, accountID, intentID)
			if loadErr != nil {
				return nil, fmt.Errorf("postgres: load generated evaluation lifecycle %s: %w", intentID, loadErr)
			}
			lifecycles[intentID] = aggregate
		}
		material, err := generativestrategy.BuildEvaluationMaterial(generativestrategy.EvaluationMaterialInput{
			Prepared: prepared, Graph: graph, Plan: plan, Result: result, Lifecycles: lifecycles, Policy: policy,
		})
		if err != nil {
			return nil, fmt.Errorf("postgres: build generated evaluation material: %w", err)
		}
		items = append(items, generativestrategy.EligibleEvaluation{ScopeID: scopeID, AccountID: accountID, ResultID: result.ID(), ReportInput: material})
	}
	return items, nil
}
