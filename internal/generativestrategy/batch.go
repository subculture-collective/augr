package generativestrategy

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const MaximumResearchBatchSize = 100

type EligibleResearch struct {
	ScopeID    uuid.UUID
	Prepared   *PreparedResearch
	AttemptID  uuid.UUID
	StartedAt  time.Time
	FinishedAt time.Time
}

type EligibleResearchSource interface {
	ListEligibleGeneratedResearch(context.Context, uuid.UUID, uuid.UUID, int, time.Time) ([]EligibleResearch, error)
}

type BatchSummary struct {
	Eligible  int
	Completed int
	Failed    int
}

type BatchService struct {
	source   EligibleResearchSource
	executor *Executor
}

func NewBatchService(source EligibleResearchSource, executor *Executor) (*BatchService, error) {
	if source == nil || executor == nil {
		return nil, fmt.Errorf("generated strategy batch source and executor are required")
	}
	return &BatchService{source: source, executor: executor}, nil
}

// RunEligible executes only work already bound by the source to the explicit
// canonical account and evaluation scope. One invalid or failed item fails the
// scheduled batch so research cannot silently skip incomplete evidence.
func (service *BatchService) RunEligible(ctx context.Context, accountID, scopeID uuid.UUID, limit int, now time.Time) (BatchSummary, error) {
	summary := BatchSummary{}
	if service == nil || service.source == nil || service.executor == nil || accountID == uuid.Nil || scopeID == uuid.Nil ||
		limit <= 0 || limit > MaximumResearchBatchSize || !canonicalExecutionTime(now) {
		return summary, fmt.Errorf("generated strategy batch requires exact account, scope, bounded limit, and UTC microsecond time")
	}
	items, err := service.source.ListEligibleGeneratedResearch(ctx, accountID, scopeID, limit, now)
	if err != nil {
		return summary, fmt.Errorf("list eligible generated research: %w", err)
	}
	if len(items) > limit {
		return summary, fmt.Errorf("generated strategy source exceeded requested batch limit")
	}
	summary.Eligible = len(items)
	seenExperiments := make(map[uuid.UUID]struct{}, len(items))
	seenAttempts := make(map[uuid.UUID]struct{}, len(items))
	for index, item := range items {
		if err := validateEligibleResearch(item, accountID, scopeID, now, seenExperiments, seenAttempts); err != nil {
			summary.Failed++
			return summary, fmt.Errorf("generated strategy work item %d: %w", index, err)
		}
		_, err = service.executor.Execute(ctx, ExecutionRequest{
			Prepared: item.Prepared, AttemptID: item.AttemptID, StartedAt: item.StartedAt, FinishedAt: item.FinishedAt,
		})
		if err != nil {
			summary.Failed++
			return summary, fmt.Errorf("generated strategy work item %d: %w", index, err)
		}
		summary.Completed++
	}
	return summary, nil
}

func validateEligibleResearch(item EligibleResearch, accountID, scopeID uuid.UUID, now time.Time, experiments, attempts map[uuid.UUID]struct{}) error {
	if item.ScopeID != scopeID || item.Prepared == nil || item.Prepared.Experiment == nil || item.Prepared.Experiment.AccountID() != accountID ||
		item.AttemptID == uuid.Nil || !canonicalExecutionTime(item.StartedAt) || !canonicalExecutionTime(item.FinishedAt) ||
		item.FinishedAt.Before(item.StartedAt) || item.StartedAt.After(now) || item.FinishedAt.After(now) {
		return fmt.Errorf("account, scope, attempt, or execution boundary does not match")
	}
	experimentID := item.Prepared.Experiment.ID()
	if _, duplicate := experiments[experimentID]; duplicate {
		return fmt.Errorf("experiment %s is duplicated", experimentID)
	}
	if _, duplicate := attempts[item.AttemptID]; duplicate {
		return fmt.Errorf("attempt %s is duplicated", item.AttemptID)
	}
	experiments[experimentID] = struct{}{}
	attempts[item.AttemptID] = struct{}{}
	return nil
}
