package agent

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

func TestNoopPersisterReturnsInvocationReceiptWithoutSharedState(t *testing.T) {
	first := NoopPersister{}
	second := NoopPersister{}
	runID := uuid.New()
	tradeDate := time.Now().UTC()
	completedAt := tradeDate.Add(time.Minute)

	failed, err := first.FinalizeRun(context.Background(), runID, tradeDate, repository.PipelineRunFinalization{Status: domain.PipelineStatusFailed, CompletedAt: completedAt, ErrorMessage: "first"})
	if err != nil || !failed.Applied || failed.Run.Status != domain.PipelineStatusFailed {
		t.Fatalf("first FinalizeRun() = (%+v, %v)", failed, err)
	}
	cancelled, err := second.FinalizeRun(context.Background(), runID, tradeDate, repository.PipelineRunFinalization{Status: domain.PipelineStatusCancelled, CompletedAt: completedAt, ErrorMessage: "second"})
	if err != nil || !cancelled.Applied || cancelled.Run.Status != domain.PipelineStatusCancelled || cancelled.Run.ErrorMessage != "second" {
		t.Fatalf("second FinalizeRun() = (%+v, %v), want isolated invocation receipt", cancelled, err)
	}
}
