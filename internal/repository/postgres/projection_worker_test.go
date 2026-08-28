package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
)

type projectionWorkerStoreStub struct {
	item       *ProjectionOutboxItem
	claims     int
	heartbeats int
	completed  int
	retried    int
	released   int
	lastCode   string
	processing bool
}

func (stub *projectionWorkerStoreStub) Claim(context.Context, string, time.Time, time.Duration) (*ProjectionOutboxItem, error) {
	stub.claims++
	item := stub.item
	stub.item = nil
	stub.processing = item != nil
	return item, nil
}
func (stub *projectionWorkerStoreStub) Heartbeat(context.Context, uuid.UUID, string, time.Time, time.Duration) error {
	stub.heartbeats++
	return nil
}
func (stub *projectionWorkerStoreStub) Complete(context.Context, uuid.UUID, string, time.Time) error {
	stub.completed++
	stub.processing = false
	return nil
}
func (stub *projectionWorkerStoreStub) Release(context.Context, uuid.UUID, string, time.Time, string) error {
	stub.released++
	stub.processing = false
	return nil
}
func (stub *projectionWorkerStoreStub) RetryOrDegrade(_ context.Context, _ uuid.UUID, _ string, _ time.Time, _ int, code string) (string, error) {
	stub.retried++
	stub.processing = false
	stub.lastCode = code
	return "retry", nil
}

type projectionRebuilderStub struct {
	request ledger.ProjectionRequest
	err     error
}

type blockingProjectionRebuilder struct {
	started chan struct{}
	unblock chan struct{}
}

func (stub *blockingProjectionRebuilder) RebuildPortfolioProjection(ctx context.Context, _ ledger.ProjectionRequest) (*ledger.PortfolioProjection, error) {
	close(stub.started)
	<-ctx.Done()
	<-stub.unblock
	return nil, ctx.Err()
}

func (stub *projectionRebuilderStub) RebuildPortfolioProjection(_ context.Context, request ledger.ProjectionRequest) (*ledger.PortfolioProjection, error) {
	stub.request = request
	if stub.err != nil {
		return nil, stub.err
	}
	return &ledger.PortfolioProjection{}, nil
}

func TestProjectionWorkerRebuildsStoredFrontierAndCompletes(t *testing.T) {
	now := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	item := &ProjectionOutboxItem{ID: uuid.New(), AccountID: uuid.New(), ThroughTransactionID: uuid.New(), AsOf: now, RequestKind: ProjectionRequestEconomicFill}
	store := &projectionWorkerStoreStub{item: item}
	rebuilder := &projectionRebuilderStub{}
	worker, err := NewProjectionWorker(store, rebuilder, ProjectionWorkerConfig{WorkerID: "worker-1", Lease: time.Minute, RetryLimit: 3, MarkSource: "kalshi", MarkNamespace: "account/marks", MaxMarkAge: time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := worker.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessOne()=%t,%v", processed, err)
	}
	if rebuilder.request.AccountID != item.AccountID || rebuilder.request.ThroughTransactionID != item.ThroughTransactionID || !rebuilder.request.AsOf.Equal(item.AsOf) {
		t.Fatalf("rebuild request=%+v", rebuilder.request)
	}
	if store.completed != 1 || store.retried != 0 {
		t.Fatalf("completed/retried=%d/%d", store.completed, store.retried)
	}
}

func TestProjectionWorkerPersistsRetryAndStopsClaiming(t *testing.T) {
	now := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	store := &projectionWorkerStoreStub{item: &ProjectionOutboxItem{ID: uuid.New(), AccountID: uuid.New(), ThroughTransactionID: uuid.New(), AsOf: now, RequestKind: ProjectionRequestEconomicFill}}
	worker, err := NewProjectionWorker(store, &projectionRebuilderStub{err: errors.New("attestation unavailable")}, ProjectionWorkerConfig{WorkerID: "worker-1", Lease: time.Minute, RetryLimit: 3, MarkSource: "kalshi", MarkNamespace: "account/marks", MaxMarkAge: time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := worker.ProcessOne(context.Background()); !processed || err == nil {
		t.Fatalf("failed ProcessOne()=%t,%v", processed, err)
	}
	if store.retried != 1 || store.completed != 0 || store.lastCode != "projection_rebuild_failed" {
		t.Fatalf("retry state=%d/%d/%q", store.retried, store.completed, store.lastCode)
	}
	worker.StopAccepting()
	if processed, err := worker.ProcessOne(context.Background()); processed || err != nil || store.claims != 1 {
		t.Fatalf("stopped ProcessOne()=%t,%v claims=%d", processed, err, store.claims)
	}
	if err := worker.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestProjectionWorkerDrainTimeoutReleasesInflightClaim(t *testing.T) {
	now := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	store := &projectionWorkerStoreStub{item: &ProjectionOutboxItem{ID: uuid.New(), AccountID: uuid.New(), ThroughTransactionID: uuid.New(), AsOf: now, RequestKind: ProjectionRequestEconomicFill}}
	rebuilder := &blockingProjectionRebuilder{started: make(chan struct{}), unblock: make(chan struct{})}
	worker, err := NewProjectionWorker(store, rebuilder, ProjectionWorkerConfig{WorkerID: "worker-1", Lease: time.Minute, RetryLimit: 3, MarkSource: "kalshi", MarkNamespace: "account/marks", MaxMarkAge: time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_, _ = worker.ProcessOne(context.Background())
		close(done)
	}()
	<-rebuilder.started
	drainContext, cancelDrain := context.WithCancel(context.Background())
	cancelDrain()
	if err := worker.Drain(drainContext); err == nil {
		t.Fatal("Drain() error = nil, want canceled deadline")
	}
	close(rebuilder.unblock)
	<-done
	if store.released != 1 {
		t.Fatalf("released claims = %d, want 1", store.released)
	}
	if store.processing {
		t.Fatal("drain left a processing projection claim stranded")
	}
}
