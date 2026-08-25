package agent

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// DecisionPersister abstracts pipeline run and decision persistence.
type DecisionPersister interface {
	// RecordRunStart persists a new pipeline run record.
	RecordRunStart(ctx context.Context, run *domain.PipelineRun) error
	// FinalizeRun atomically persists a terminal run and its terminal event.
	FinalizeRun(ctx context.Context, runID uuid.UUID, tradeDate time.Time, finalization repository.PipelineRunFinalization) (repository.PipelineRunFinalizationReceipt, error)
	// SupportsSnapshots reports whether snapshot persistence is enabled.
	SupportsSnapshots() bool
	// PersistSnapshot persists a single pipeline input snapshot.
	PersistSnapshot(ctx context.Context, snapshot *domain.PipelineRunSnapshot) error
	// PersistDecision persists a single agent decision with optional LLM metadata.
	PersistDecision(ctx context.Context, runID uuid.UUID, node Node, roundNumber *int, output string, llmResponse *DecisionLLMResponse) error
	// PersistEvent persists a structured pipeline or agent event.
	PersistEvent(ctx context.Context, event *domain.AgentEvent) error
}
