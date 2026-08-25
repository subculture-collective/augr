package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/google/uuid"
)

type staleRunRepoStub struct {
	runs            []domain.PipelineRun
	updates         []repository.PipelineRunFinalization
	ids             []uuid.UUID
	filter          repository.PipelineRunFilter
	err             error
	applied         *bool
	listStarted     chan struct{}
	finishList      chan struct{}
	finalize        func()
	finalizeStarted chan struct{}
	blockFinalize   bool
}

func (s *staleRunRepoStub) Create(context.Context, *domain.PipelineRun) error { return nil }
func (s *staleRunRepoStub) GetByID(context.Context, uuid.UUID) (*domain.PipelineRun, error) {
	return nil, repository.ErrNotFound
}

func (s *staleRunRepoStub) Get(context.Context, uuid.UUID, time.Time) (*domain.PipelineRun, error) {
	return nil, repository.ErrNotFound
}

func (s *staleRunRepoStub) List(_ context.Context, filter repository.PipelineRunFilter, _, _ int) ([]domain.PipelineRun, error) {
	if s.listStarted != nil {
		close(s.listStarted)
		<-s.finishList
	}
	s.filter = filter
	if s.err != nil {
		return nil, s.err
	}
	return s.runs, nil
}

func TestStaleRunReconcilerStopWaitsForSweep(t *testing.T) {
	repo := &staleRunRepoStub{listStarted: make(chan struct{}), finishList: make(chan struct{})}
	reconciler := NewStaleRunReconciler(repo, nil, nil, nil, nil, StaleRunReconcilerConfig{TTL: time.Minute, Interval: time.Hour})
	reconciler.Start(context.Background())
	<-repo.listStarted
	stopped := make(chan struct{})
	go func() { reconciler.StopAndWait(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("StopAndWait returned while sweep was running")
	case <-time.After(10 * time.Millisecond):
	}
	close(repo.finishList)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("StopAndWait did not join sweep")
	}
}

func (s *staleRunRepoStub) Count(context.Context, repository.PipelineRunFilter) (int, error) {
	return len(s.runs), nil
}

func (s *staleRunRepoStub) Finalize(ctx context.Context, id uuid.UUID, tradeDate time.Time, update repository.PipelineRunFinalization) (repository.PipelineRunFinalizationReceipt, error) {
	s.ids = append(s.ids, id)
	s.updates = append(s.updates, update)
	if s.finalizeStarted != nil {
		close(s.finalizeStarted)
	}
	if s.blockFinalize {
		<-ctx.Done()
		return repository.PipelineRunFinalizationReceipt{}, ctx.Err()
	}
	if s.finalize != nil {
		s.finalize()
	}
	run := domain.PipelineRun{ID: id, TradeDate: tradeDate, Status: update.Status, CompletedAt: &update.CompletedAt, ErrorMessage: update.ErrorMessage}
	applied := true
	if s.applied != nil {
		applied = *s.applied
	}
	return repository.PipelineRunFinalizationReceipt{Applied: applied, Run: run}, nil
}

func TestStaleRunReconcilerStopAndWaitCancelsBlockedFinalization(t *testing.T) {
	now := time.Now().UTC()
	repo := &staleRunRepoStub{
		runs:            []domain.PipelineRun{{ID: uuid.New(), TradeDate: now, Status: domain.PipelineStatusRunning, StartedAt: now.Add(-time.Hour)}},
		finalizeStarted: make(chan struct{}),
		blockFinalize:   true,
	}
	reconciler := NewStaleRunReconciler(repo, nil, nil, nil, nil, StaleRunReconcilerConfig{TTL: time.Minute, Interval: time.Hour, Clock: func() time.Time { return now }})
	reconciler.Start(context.Background())
	<-repo.finalizeStarted

	stopped := make(chan struct{})
	go func() {
		reconciler.StopAndWait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("StopAndWait hung on blocked finalization")
	}
}

func TestStaleRunReconcilerCancellationStopsMultiRowSweep(t *testing.T) {
	now := time.Now().UTC()
	runs := make([]domain.PipelineRun, 500)
	for i := range runs {
		runs[i] = domain.PipelineRun{ID: uuid.New(), TradeDate: now, Status: domain.PipelineStatusRunning, StartedAt: now.Add(-time.Hour)}
	}
	ctx, cancel := context.WithCancel(context.Background())
	repo := &staleRunRepoStub{runs: runs, finalize: cancel}
	reconciler := NewStaleRunReconciler(repo, nil, nil, nil, nil, StaleRunReconcilerConfig{TTL: time.Minute, Clock: func() time.Time { return now }})

	started := time.Now()
	count, err := reconciler.Reconcile(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Reconcile() error = %v, want context cancellation", err)
	}
	if count != 0 || len(repo.updates) != 1 {
		t.Fatalf("Reconcile() count=%d finalizations=%d, want one bounded finalization and no remaining rows", count, len(repo.updates))
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Reconcile() cancellation exit took %v", elapsed)
	}
}

func TestStaleRunReconcilerLoserHasNoEffects(t *testing.T) {
	now := time.Now().UTC()
	run := domain.PipelineRun{ID: uuid.New(), TradeDate: now, Status: domain.PipelineStatusRunning, StartedAt: now.Add(-time.Hour)}
	loser := false
	repo := &staleRunRepoStub{runs: []domain.PipelineRun{run}, applied: &loser}
	audit := &staleAuditLogStub{}
	metrics := &staleMetricStub{}
	registry := NewRunContextRegistry()
	cancelled := false
	_ = registry.Register(run.ID, run.TradeDate, func(error) { cancelled = true })
	count, err := NewStaleRunReconciler(repo, audit, registry, metrics, nil, StaleRunReconcilerConfig{TTL: 30 * time.Minute, Clock: func() time.Time { return now }}).Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 || cancelled || metrics.count != 0 || len(audit.entries) != 0 {
		t.Fatalf("loser effects: count=%d cancel=%v metrics=%d audit=%d", count, cancelled, metrics.count, len(audit.entries))
	}
}

func (*staleRunRepoStub) RefineCompletedSignal(context.Context, uuid.UUID, time.Time, domain.PipelineSignal, domain.PipelineSignal) (repository.PipelineRunFinalizationReceipt, error) {
	return repository.PipelineRunFinalizationReceipt{}, nil
}

type staleAuditLogStub struct {
	entries []*domain.AuditLogEntry
	err     error
	started chan struct{}
	block   bool
}

func (s *staleAuditLogStub) Create(ctx context.Context, entry *domain.AuditLogEntry) error {
	if s.started != nil {
		close(s.started)
	}
	if s.block {
		<-ctx.Done()
		return ctx.Err()
	}
	if s.err != nil {
		return s.err
	}
	s.entries = append(s.entries, entry)
	return nil
}

func TestStaleRunReconcilerStopAndWaitBoundsBlockedAudit(t *testing.T) {
	now := time.Now().UTC()
	repo := &staleRunRepoStub{runs: []domain.PipelineRun{{ID: uuid.New(), TradeDate: now.Truncate(24 * time.Hour), Status: domain.PipelineStatusRunning, StartedAt: now.Add(-time.Hour)}}}
	audit := &staleAuditLogStub{started: make(chan struct{}), block: true}
	reconciler := NewStaleRunReconciler(repo, audit, nil, nil, nil, StaleRunReconcilerConfig{
		TTL:          time.Minute,
		Interval:     time.Hour,
		Clock:        func() time.Time { return now },
		AuditTimeout: time.Hour,
	})
	reconciler.Start(context.Background())
	<-audit.started
	stopped := make(chan struct{})
	go func() {
		reconciler.StopAndWait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("StopAndWait hung on blocked audit persistence")
	}
}

func (s *staleAuditLogStub) Query(context.Context, repository.AuditLogFilter, int, int) ([]domain.AuditLogEntry, error) {
	return nil, nil
}

func (s *staleAuditLogStub) Count(context.Context, repository.AuditLogFilter) (int, error) {
	return len(s.entries), nil
}

type staleMetricStub struct{ count int }

func (s *staleMetricStub) RecordStaleRunReconciled() { s.count++ }

func TestStaleRunReconcilerReconcile_MarksRunsFailedAndCancels(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 11, 12, 0, 0, 0, time.UTC)
	staleRun := domain.PipelineRun{
		ID:        uuid.New(),
		TradeDate: now.Truncate(24 * time.Hour),
		Status:    domain.PipelineStatusRunning,
		StartedAt: now.Add(-45 * time.Minute),
	}
	repo := &staleRunRepoStub{runs: []domain.PipelineRun{staleRun}}
	audit := &staleAuditLogStub{}
	metrics := &staleMetricStub{}
	registry := NewRunContextRegistry()
	cancelled := false
	_ = registry.Register(staleRun.ID, staleRun.TradeDate, func(error) { cancelled = true })

	reconciler := NewStaleRunReconciler(repo, audit, registry, metrics, nil, StaleRunReconcilerConfig{
		TTL:      30 * time.Minute,
		Interval: time.Minute,
		Clock:    func() time.Time { return now },
	})

	count, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("Reconcile() count = %d, want 1", count)
	}
	if repo.filter.Status != domain.PipelineStatusRunning {
		t.Fatalf("filter.Status = %q, want %q", repo.filter.Status, domain.PipelineStatusRunning)
	}
	if repo.filter.StartedBefore == nil || !repo.filter.StartedBefore.Equal(now.Add(-30*time.Minute)) {
		t.Fatalf("StartedBefore = %v, want %v", repo.filter.StartedBefore, now.Add(-30*time.Minute))
	}
	if len(repo.updates) != 1 {
		t.Fatalf("updates = %d, want 1", len(repo.updates))
	}
	if repo.updates[0].Status != domain.PipelineStatusFailed {
		t.Fatalf("update.Status = %q, want %q", repo.updates[0].Status, domain.PipelineStatusFailed)
	}
	if !repo.updates[0].CompletedAt.Equal(now) {
		t.Fatalf("CompletedAt = %v, want %v", repo.updates[0].CompletedAt, now)
	}
	if repo.updates[0].ErrorMessage != "stale run: exceeded TTL" {
		t.Fatalf("ErrorMessage = %q", repo.updates[0].ErrorMessage)
	}
	if !cancelled {
		t.Fatal("expected registry cancel func to be called")
	}
	if metrics.count != 1 {
		t.Fatalf("metrics count = %d, want 1", metrics.count)
	}
	if len(audit.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(audit.entries))
	}
	if audit.entries[0].EventType != "pipeline_run.stale_reconciled" {
		t.Fatalf("audit event = %q", audit.entries[0].EventType)
	}
	var details map[string]any
	if err := json.Unmarshal(audit.entries[0].Details, &details); err != nil {
		t.Fatalf("unmarshal audit details: %v", err)
	}
	if details["reason"] != "stale run: exceeded TTL" {
		t.Fatalf("audit reason = %v", details["reason"])
	}
}

func TestStaleRunReconcilerReconcile_SkipsWhenRepoFails(t *testing.T) {
	t.Parallel()

	reconciler := NewStaleRunReconciler(&staleRunRepoStub{err: errors.New("boom")}, nil, nil, nil, nil, StaleRunReconcilerConfig{
		TTL:   time.Minute,
		Clock: time.Now,
	})

	count, err := reconciler.Reconcile(context.Background())
	if err == nil {
		t.Fatal("Reconcile() error = nil, want error")
	}
	if count != 0 {
		t.Fatalf("Reconcile() count = %d, want 0", count)
	}
}
