package service

import (
	"context"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// RunService encapsulates operations on pipeline runs.
type RunService struct {
	runs repository.PipelineRunRepository
}

func NewRunService(runs repository.PipelineRunRepository) *RunService {
	return &RunService{runs: runs}
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
	update := repository.PipelineRunStatusUpdate{
		Status: domain.PipelineStatusCancelled,
	}
	return svc.runs.UpdateStatus(ctx, id, run.TradeDate, update)
}
