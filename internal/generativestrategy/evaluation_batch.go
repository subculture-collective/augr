package generativestrategy

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/evaluation"
)

// EligibleEvaluation is one complete, source-reconstructed evaluation input.
// The evaluation service deliberately reloads ResultID before calculating so
// a source cannot substitute caller-authored result bytes.
type EligibleEvaluation struct {
	ScopeID   uuid.UUID
	AccountID uuid.UUID
	ResultID  uuid.UUID
	evaluation.ReportInput
}

type EligibleEvaluationSource interface {
	ListEligibleGeneratedEvaluations(context.Context, uuid.UUID, uuid.UUID, int) ([]EligibleEvaluation, error)
}

type EvaluationRunner interface {
	Evaluate(context.Context, evaluation.Request) (*evaluation.Report, error)
}

type EvaluationBatchSummary struct {
	Eligible  int
	Completed int
	Failed    int
}

type EvaluationBatchService struct {
	source EligibleEvaluationSource
	runner EvaluationRunner
}

func NewEvaluationBatchService(source EligibleEvaluationSource, runner EvaluationRunner) (*EvaluationBatchService, error) {
	if source == nil || runner == nil {
		return nil, fmt.Errorf("generated evaluation source and runner are required")
	}
	return &EvaluationBatchService{source: source, runner: runner}, nil
}

// RunEligible evaluates a bounded exact-account/exact-scope batch. Any bad
// item or evaluation failure stops the batch; incomplete evidence is never
// counted as successful or silently skipped.
func (service *EvaluationBatchService) RunEligible(ctx context.Context, accountID, scopeID uuid.UUID, limit int) (EvaluationBatchSummary, error) {
	summary := EvaluationBatchSummary{}
	if service == nil || service.source == nil || service.runner == nil || accountID == uuid.Nil || scopeID == uuid.Nil || limit <= 0 || limit > MaximumResearchBatchSize {
		return summary, fmt.Errorf("generated evaluation batch requires exact account, scope, and bounded limit")
	}
	items, err := service.source.ListEligibleGeneratedEvaluations(ctx, accountID, scopeID, limit)
	if err != nil {
		return summary, fmt.Errorf("list eligible generated evaluations: %w", err)
	}
	if len(items) > limit {
		return summary, fmt.Errorf("generated evaluation source exceeded requested batch limit")
	}
	summary.Eligible = len(items)
	seen := make(map[uuid.UUID]struct{}, len(items))
	for index, item := range items {
		if item.ScopeID != scopeID || item.AccountID != accountID || item.ResultID == uuid.Nil || item.Policy == nil || item.Result != nil {
			summary.Failed++
			return summary, fmt.Errorf("generated evaluation work item %d does not match account, scope, result, or policy boundary", index)
		}
		if _, duplicate := seen[item.ResultID]; duplicate {
			summary.Failed++
			return summary, fmt.Errorf("generated evaluation result %s is duplicated", item.ResultID)
		}
		seen[item.ResultID] = struct{}{}
	}
	for index, item := range items {
		report, evaluateErr := service.runner.Evaluate(ctx, evaluation.Request{ResultID: item.ResultID, ReportInput: item.ReportInput})
		if evaluateErr != nil {
			summary.Failed++
			return summary, fmt.Errorf("generated evaluation work item %d: %w", index, evaluateErr)
		}
		if report == nil || report.ResultID() != item.ResultID || report.AccountID() != accountID {
			summary.Failed++
			return summary, fmt.Errorf("generated evaluation work item %d returned mismatched report", index)
		}
		summary.Completed++
	}
	return summary, nil
}
