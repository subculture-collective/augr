package postgres

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
)

type portfolioProjectionRebuilder interface {
	RebuildPortfolioProjection(context.Context, ledger.ProjectionRequest) (*ledger.PortfolioProjection, error)
}

type projectionOutboxStore interface {
	Claim(context.Context, string, time.Time, time.Duration) (*ProjectionOutboxItem, error)
	Heartbeat(context.Context, uuid.UUID, string, time.Time, time.Duration) error
	Complete(context.Context, uuid.UUID, string, time.Time) error
	Release(context.Context, uuid.UUID, string, time.Time, string) error
	RetryOrDegrade(context.Context, uuid.UUID, string, time.Time, int, string) (string, error)
}

type ProjectionWorkerConfig struct {
	WorkerID      string
	Lease         time.Duration
	RetryLimit    int
	MarkSource    string
	MarkNamespace string
	MaxMarkAge    time.Duration
	Now           func() time.Time
}

// ProjectionWorker reads durable work with the runtime pool and performs the
// controlled checkpoint write with the separately privileged projection repo.
type ProjectionWorker struct {
	store     projectionOutboxStore
	rebuilder portfolioProjectionRebuilder
	config    ProjectionWorkerConfig
	stopping  atomic.Bool
	inflight  sync.WaitGroup
	activeMu  sync.Mutex
	active    map[uuid.UUID]context.CancelFunc
}

func NewProjectionWorker(store projectionOutboxStore, rebuilder portfolioProjectionRebuilder, config ProjectionWorkerConfig) (*ProjectionWorker, error) {
	config.WorkerID = strings.TrimSpace(config.WorkerID)
	config.MarkSource = strings.ToLower(strings.TrimSpace(config.MarkSource))
	config.MarkNamespace = strings.TrimSpace(config.MarkNamespace)
	config.Lease = config.Lease.Truncate(time.Microsecond)
	config.MaxMarkAge = config.MaxMarkAge.Truncate(time.Microsecond)
	if store == nil || rebuilder == nil || config.WorkerID == "" || len(config.WorkerID) > 256 || config.Lease <= 0 || config.RetryLimit <= 0 || config.MarkSource == "" || config.MarkNamespace == "" || config.MaxMarkAge <= 0 {
		return nil, fmt.Errorf("postgres: projection worker requires stores, identity, lease, retry policy, and mark policy")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &ProjectionWorker{store: store, rebuilder: rebuilder, config: config, active: make(map[uuid.UUID]context.CancelFunc)}, nil
}

// ProcessOne claims and completes at most one durable request. A nil error with
// processed=false means there was no eligible work or shutdown has started.
func (worker *ProjectionWorker) ProcessOne(ctx context.Context) (processed bool, err error) {
	if worker == nil || worker.stopping.Load() {
		return false, nil
	}
	now := worker.config.Now().UTC().Truncate(time.Microsecond)
	item, err := worker.store.Claim(ctx, worker.config.WorkerID, now, worker.config.Lease)
	if err != nil || item == nil {
		return false, err
	}
	if worker.stopping.Load() {
		_, releaseErr := worker.store.RetryOrDegrade(ctx, item.ID, worker.config.WorkerID, worker.config.Now(), worker.config.RetryLimit, "worker_stopping")
		return true, releaseErr
	}
	worker.inflight.Add(1)
	defer worker.inflight.Done()
	rebuildContext, cancelRebuild := context.WithCancel(ctx)
	worker.activeMu.Lock()
	worker.active[item.ID] = cancelRebuild
	worker.activeMu.Unlock()
	defer func() {
		cancelRebuild()
		worker.activeMu.Lock()
		delete(worker.active, item.ID)
		worker.activeMu.Unlock()
	}()

	source, namespace, maxAge := worker.config.MarkSource, worker.config.MarkNamespace, worker.config.MaxMarkAge
	if item.RequestKind == ProjectionRequestMarkRebuild {
		source, namespace, maxAge = item.MarkSource, item.MarkNamespace, item.MaxMarkAge
	}
	heartbeatContext, cancelHeartbeat := context.WithCancel(rebuildContext)
	heartbeatErrors := make(chan error, 1)
	go worker.heartbeat(heartbeatContext, item.ID, heartbeatErrors)
	_, rebuildErr := worker.rebuilder.RebuildPortfolioProjection(rebuildContext, ledger.ProjectionRequest{
		AccountID: item.AccountID, ThroughTransactionID: item.ThroughTransactionID, AsOf: item.AsOf,
		MarkSource: source, MarkNamespace: namespace, MaxMarkAge: maxAge,
	})
	cancelHeartbeat()
	// Wait for the heartbeat goroutine to stop before completing or retrying.
	// Otherwise a late lease-loss result could race past completion and allow a
	// worker that no longer owns the row to finalize it.
	if heartbeatErr := <-heartbeatErrors; heartbeatErr != nil {
		return true, heartbeatErr
	}
	finishedAt := worker.config.Now().UTC().Truncate(time.Microsecond)
	if rebuildErr != nil {
		finishContext, cancelFinish := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancelFinish()
		_, persistErr := worker.store.RetryOrDegrade(finishContext, item.ID, worker.config.WorkerID, finishedAt, worker.config.RetryLimit, "projection_rebuild_failed")
		if persistErr != nil {
			return true, fmt.Errorf("projection rebuild failed (%v) and retry state failed: %w", rebuildErr, persistErr)
		}
		return true, rebuildErr
	}
	finishContext, cancelFinish := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelFinish()
	if err := worker.store.Complete(finishContext, item.ID, worker.config.WorkerID, finishedAt); err != nil {
		return true, err
	}
	return true, nil
}

func (worker *ProjectionWorker) heartbeat(ctx context.Context, id uuid.UUID, result chan<- error) {
	interval := worker.config.Lease / 3
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			result <- nil
			return
		case <-ticker.C:
			if err := worker.store.Heartbeat(ctx, id, worker.config.WorkerID, worker.config.Now(), worker.config.Lease); err != nil {
				result <- err
				return
			}
		}
	}
}

func (worker *ProjectionWorker) StopAccepting() {
	if worker != nil {
		worker.stopping.Store(true)
	}
}

func (worker *ProjectionWorker) Drain(ctx context.Context) error {
	if worker == nil {
		return nil
	}
	worker.StopAccepting()
	done := make(chan struct{})
	go func() {
		worker.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		worker.activeMu.Lock()
		active := make([]uuid.UUID, 0, len(worker.active))
		for id, cancel := range worker.active {
			cancel()
			active = append(active, id)
		}
		worker.activeMu.Unlock()
		releaseContext, cancelRelease := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelRelease()
		for _, id := range active {
			_ = worker.store.Release(releaseContext, id, worker.config.WorkerID, worker.config.Now(), "worker_drain_timeout")
		}
		return fmt.Errorf("postgres: drain projection worker: %w", ctx.Err())
	}
}
