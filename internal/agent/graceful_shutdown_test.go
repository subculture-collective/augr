package agent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// --------------------------------------------------------------------------
// Graceful-shutdown invariant: pipeline run status is never left at "running"
// --------------------------------------------------------------------------

func TestRepoPersister_FinalizeRunUsesCallerDetachedContext(t *testing.T) {
	t.Parallel()

	repo := &captureUpdateRunRepo{}

	persister := NewRepoPersister(repo, nil, nil, nil, nil)

	runID := uuid.New()
	tradeDate := time.Now().UTC().Truncate(24 * time.Hour)
	detachedCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := persister.FinalizeRun(detachedCtx, runID, tradeDate, repository.PipelineRunFinalization{Status: domain.PipelineStatusFailed, CompletedAt: time.Now(), ErrorMessage: "context canceled"})
	if err != nil {
		t.Fatalf("FinalizeRun with cancelled context returned error: %v; pipeline run would be stuck at 'running'", err)
	}

	if !repo.updateCalled.Load() {
		t.Fatal("UpdateStatus was not called; pipeline run status was not persisted")
	}
	if repo.lastStatus != domain.PipelineStatusFailed {
		t.Fatalf("persisted status = %q, want %q", repo.lastStatus, domain.PipelineStatusFailed)
	}
}

func TestRepoPersister_FinalizeRunCompletedObservesCallerCancellation(t *testing.T) {
	t.Parallel()

	repo := &captureUpdateRunRepo{}

	persister := NewRepoPersister(repo, nil, nil, nil, nil)

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	runID := uuid.New()
	tradeDate := time.Now().UTC().Truncate(24 * time.Hour)

	_, err := persister.FinalizeRun(cancelledCtx, runID, tradeDate, repository.PipelineRunFinalization{Status: domain.PipelineStatusCompleted, CompletedAt: time.Now()})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("FinalizeRun() error = %v, want caller cancellation", err)
	}
	if repo.updateCalled.Load() {
		t.Fatal("completed finalization ignored caller cancellation")
	}
}

// --------------------------------------------------------------------------
// captureUpdateRunRepo records UpdateStatus calls for assertions.
// --------------------------------------------------------------------------

type captureUpdateRunRepo struct {
	updateCalled atomic.Bool
	lastStatus   domain.PipelineStatus
	updateErr    error
}

func (r *captureUpdateRunRepo) Create(_ context.Context, _ *domain.PipelineRun) error { return nil }

func (r *captureUpdateRunRepo) Get(_ context.Context, _ uuid.UUID, _ time.Time) (*domain.PipelineRun, error) {
	return nil, repository.ErrNotFound
}

func (r *captureUpdateRunRepo) GetByID(_ context.Context, _ uuid.UUID) (*domain.PipelineRun, error) {
	return nil, repository.ErrNotFound
}

func (r *captureUpdateRunRepo) List(_ context.Context, _ repository.PipelineRunFilter, _, _ int) ([]domain.PipelineRun, error) {
	return nil, nil
}

func (r *captureUpdateRunRepo) Count(_ context.Context, _ repository.PipelineRunFilter) (int, error) {
	return 0, nil
}

func (r *captureUpdateRunRepo) Finalize(ctx context.Context, id uuid.UUID, tradeDate time.Time, finalization repository.PipelineRunFinalization) (repository.PipelineRunFinalizationReceipt, error) {
	if ctx.Err() != nil {
		return repository.PipelineRunFinalizationReceipt{}, ctx.Err()
	}
	if r.updateErr != nil {
		return repository.PipelineRunFinalizationReceipt{}, r.updateErr
	}
	r.updateCalled.Store(true)
	r.lastStatus = finalization.Status
	return repository.PipelineRunFinalizationReceipt{Applied: true, Run: domain.PipelineRun{ID: id, TradeDate: tradeDate, Status: finalization.Status, CompletedAt: &finalization.CompletedAt}}, nil
}

func (*captureUpdateRunRepo) RefineCompletedSignal(context.Context, uuid.UUID, time.Time, domain.PipelineSignal, domain.PipelineSignal) (repository.PipelineRunFinalizationReceipt, error) {
	return repository.PipelineRunFinalizationReceipt{}, nil
}

func TestRepoPersister_FinalizeRunPropagatesFailure(t *testing.T) {
	t.Parallel()

	repo := &captureUpdateRunRepo{updateErr: errors.New("database unavailable")}
	persister := NewRepoPersister(repo, nil, nil, nil, nil)
	_, err := persister.FinalizeRun(context.Background(), uuid.New(), time.Now().UTC(), repository.PipelineRunFinalization{Status: domain.PipelineStatusCompleted, CompletedAt: time.Now().UTC()})
	if err == nil || !strings.Contains(err.Error(), "finalize run") {
		t.Fatalf("FinalizeRun() error = %v, want propagated failure", err)
	}
}
