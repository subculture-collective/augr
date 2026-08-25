package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/runcontrol"
	"github.com/google/uuid"
)

type runRepoStub struct {
	run              domain.PipelineRun
	applied          bool
	finalized        bool
	getHook          func()
	resultErr        error
	finalizeErr      error
	finalizeDeadline time.Time
	hasDeadline      bool
}

func (*runRepoStub) Create(context.Context, *domain.PipelineRun) error { return nil }
func (r *runRepoStub) GetByID(context.Context, uuid.UUID) (*domain.PipelineRun, error) {
	if r.getHook != nil {
		r.getHook()
	}
	value := r.run
	return &value, nil
}
func (r *runRepoStub) Get(context.Context, uuid.UUID, time.Time) (*domain.PipelineRun, error) {
	value := r.run
	return &value, nil
}
func (*runRepoStub) List(context.Context, repository.PipelineRunFilter, int, int) ([]domain.PipelineRun, error) {
	return nil, nil
}
func (*runRepoStub) Count(context.Context, repository.PipelineRunFilter) (int, error) { return 0, nil }
func (r *runRepoStub) Finalize(ctx context.Context, _ uuid.UUID, _ time.Time, value repository.PipelineRunFinalization) (repository.PipelineRunFinalizationReceipt, error) {
	r.finalizeErr = ctx.Err()
	r.finalizeDeadline, r.hasDeadline = ctx.Deadline()
	if r.applied {
		r.run.Status = value.Status
		r.run.CompletedAt = &value.CompletedAt
		r.run.ErrorMessage = value.ErrorMessage
		r.finalized = true
	}
	return repository.PipelineRunFinalizationReceipt{Applied: r.applied, Run: r.run}, r.resultErr
}

func TestRunServiceCancelFinalizesAfterRequestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	repo := &runRepoStub{run: domain.PipelineRun{ID: uuid.New(), StrategyID: uuid.New(), TradeDate: time.Now().UTC(), Status: domain.PipelineStatusRunning}, applied: true, getHook: cancel}
	canceller := &orderedRunCanceller{repo: repo}

	if err := NewRunService(repo, canceller).Cancel(ctx, repo.run.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.finalizeErr; err != nil {
		t.Fatalf("Finalize() context error = %v, want cancellation detached", err)
	}
	if !repo.hasDeadline || time.Until(repo.finalizeDeadline) <= 0 || time.Until(repo.finalizeDeadline) > 10*time.Second {
		t.Fatalf("Finalize() deadline = %v, want active deadline within 10s", repo.finalizeDeadline)
	}
	if !canceller.called || canceller.cause != runcontrol.Operator {
		t.Fatalf("cancel = %+v", canceller)
	}
}
func (*runRepoStub) RefineCompletedSignal(context.Context, uuid.UUID, time.Time, domain.PipelineSignal, domain.PipelineSignal) (repository.PipelineRunFinalizationReceipt, error) {
	return repository.PipelineRunFinalizationReceipt{}, nil
}

type orderedRunCanceller struct {
	repo   *runRepoStub
	called bool
	cause  error
}

func (c *orderedRunCanceller) Cancel(_ uuid.UUID, _ time.Time, cause error) bool {
	if !c.repo.finalized {
		panic("cancel propagated before finalization commit")
	}
	c.called, c.cause = true, cause
	return true
}

func TestRunServiceCancelFinalizesBeforeCausePropagation(t *testing.T) {
	repo := &runRepoStub{run: domain.PipelineRun{ID: uuid.New(), StrategyID: uuid.New(), TradeDate: time.Now().UTC(), Status: domain.PipelineStatusRunning}, applied: true}
	canceller := &orderedRunCanceller{repo: repo}
	if err := NewRunService(repo, canceller).Cancel(context.Background(), repo.run.ID); err != nil {
		t.Fatal(err)
	}
	if !canceller.called || canceller.cause != runcontrol.Operator {
		t.Fatalf("cancel = %+v", canceller)
	}
}

func TestRunServiceCancelLoserDoesNotPropagate(t *testing.T) {
	repo := &runRepoStub{run: domain.PipelineRun{ID: uuid.New(), StrategyID: uuid.New(), TradeDate: time.Now().UTC(), Status: domain.PipelineStatusRunning}}
	canceller := &orderedRunCanceller{repo: repo}
	err := NewRunService(repo, canceller).Cancel(context.Background(), repo.run.ID)
	serviceErr, ok := err.(*ServiceError)
	if !ok || serviceErr.Status != 409 {
		t.Fatalf("error = %v", err)
	}
	if canceller.called {
		t.Fatal("CAS loser propagated cancellation")
	}
}

func TestRunServiceCancelFinalizationFailureDoesNotPropagate(t *testing.T) {
	wantErr := errors.New("finalization failed")
	repo := &runRepoStub{run: domain.PipelineRun{ID: uuid.New(), StrategyID: uuid.New(), TradeDate: time.Now().UTC(), Status: domain.PipelineStatusRunning}, applied: true, resultErr: wantErr}
	canceller := &orderedRunCanceller{repo: repo}

	err := NewRunService(repo, canceller).Cancel(context.Background(), repo.run.ID)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Cancel() error = %v, want %v", err, wantErr)
	}
	if canceller.called {
		t.Fatal("failed DB finalization propagated cancellation")
	}
}
