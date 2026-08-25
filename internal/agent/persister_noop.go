package agent

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// NoopPersister is a DecisionPersister that does nothing. Useful for tests
// that don't need persistence.
type NoopPersister struct{}

func (NoopPersister) RecordRunStart(context.Context, *domain.PipelineRun) error { return nil }

func (NoopPersister) FinalizeRun(_ context.Context, runID uuid.UUID, tradeDate time.Time, finalization repository.PipelineRunFinalization) (repository.PipelineRunFinalizationReceipt, error) {
	run := domain.PipelineRun{ID: runID, TradeDate: tradeDate, Status: finalization.Status, CompletedAt: &finalization.CompletedAt, ErrorMessage: finalization.ErrorMessage}
	run.PhaseTimings = append(run.PhaseTimings, finalization.PhaseTimings...)
	if finalization.Signal != nil {
		run.Signal = *finalization.Signal
	}
	return repository.PipelineRunFinalizationReceipt{Applied: true, Run: run}, nil
}

func (NoopPersister) SupportsSnapshots() bool { return false }

func (NoopPersister) PersistSnapshot(context.Context, *domain.PipelineRunSnapshot) error { return nil }

func (NoopPersister) PersistDecision(context.Context, uuid.UUID, Node, *int, string, *DecisionLLMResponse) error {
	return nil
}

func (NoopPersister) PersistEvent(context.Context, *domain.AgentEvent) error { return nil }
