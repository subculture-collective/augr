package service

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/runcontrol"
)

type RunCanceller interface {
	Cancel(uuid.UUID, time.Time, error) bool
}

// RunService encapsulates operations on pipeline runs.
type RunService struct {
	runs     repository.PipelineRunRepository
	registry RunCanceller
}

func NewRunService(runs repository.PipelineRunRepository, registry ...RunCanceller) *RunService {
	svc := &RunService{runs: runs}
	if len(registry) > 0 {
		svc.registry = registry[0]
	}
	return svc
}

// Cancel validates the state machine transition and cancels the run.
func (svc *RunService) Cancel(ctx context.Context, id uuid.UUID) error {
	run, err := svc.runs.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if run == nil {
		return repository.ErrNotFound
	}
	if !run.Status.CanTransitionTo(domain.PipelineStatusCancelled) {
		return &ServiceError{Status: 400, Message: "run cannot be cancelled in its current state"}
	}
	completedAt := time.Now().UTC()
	message := runcontrol.Operator.Error()
	event := &domain.AgentEvent{
		PipelineRunID: &run.ID,
		StrategyID:    &run.StrategyID,
		EventKind:     "pipeline_cancelled",
		Title:         "Pipeline cancelled",
		Summary:       message,
		Tags:          []string{"pipeline", "cancelled", "operator"},
	}
	finalizeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	receipt, err := svc.runs.Finalize(finalizeCtx, id, run.TradeDate, repository.PipelineRunFinalization{Status: domain.PipelineStatusCancelled, CompletedAt: completedAt, ErrorMessage: message, Event: event})
	if err != nil {
		return err
	}
	if !receipt.Applied {
		return &ServiceError{Status: 409, Message: "run already reached terminal state"}
	}
	if svc.registry != nil {
		svc.registry.Cancel(id, run.TradeDate, runcontrol.Operator)
	}
	return nil
}
