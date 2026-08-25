package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/runcontrol"
	"github.com/google/uuid"
)

const staleRunUpdateTimeout = 10 * time.Second

// StaleRunMetrics captures the single stale-run metric emitted by the reconciler.
type StaleRunMetrics interface {
	RecordStaleRunReconciled()
}

// StaleRunReconcilerConfig defines the stale-run watchdog cadence and clock source.
type StaleRunReconcilerConfig struct {
	TTL          time.Duration
	Interval     time.Duration
	Clock        func() time.Time
	AuditTimeout time.Duration
}

// StaleRunReconciler marks abandoned running pipeline runs as failed.
type StaleRunReconciler struct {
	runs         repository.PipelineRunRepository
	auditLog     repository.AuditLogRepository
	registry     *RunContextRegistry
	metrics      StaleRunMetrics
	logger       *slog.Logger
	ttl          time.Duration
	interval     time.Duration
	clock        func() time.Time
	auditTimeout time.Duration
	mu           sync.Mutex
	cancel       context.CancelFunc
	wg           sync.WaitGroup
}

// NewStaleRunReconciler constructs a stale-run watchdog.
func NewStaleRunReconciler(
	runs repository.PipelineRunRepository,
	auditLog repository.AuditLogRepository,
	registry *RunContextRegistry,
	metrics StaleRunMetrics,
	logger *slog.Logger,
	cfg StaleRunReconcilerConfig,
) *StaleRunReconciler {
	if logger == nil {
		logger = slog.Default()
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	auditTimeout := cfg.AuditTimeout
	if auditTimeout <= 0 {
		auditTimeout = staleRunUpdateTimeout
	}
	return &StaleRunReconciler{
		runs:         runs,
		auditLog:     auditLog,
		registry:     registry,
		metrics:      metrics,
		logger:       logger,
		ttl:          cfg.TTL,
		interval:     interval,
		clock:        clock,
		auditTimeout: auditTimeout,
	}
}

// Start begins the periodic stale-run sweep until ctx is cancelled.
func (r *StaleRunReconciler) Start(ctx context.Context) {
	if r == nil || r.runs == nil || r.ttl <= 0 {
		return
	}
	r.mu.Lock()
	if r.cancel != nil {
		r.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.wg.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			if _, err := r.Reconcile(runCtx); err != nil {
				r.logger.Warn("stale run reconciler sweep failed", slog.Any("error", err))
			}
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// Stop cancels the periodic reconciler.
func (r *StaleRunReconciler) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Wait joins the periodic reconciler.
func (r *StaleRunReconciler) Wait() {
	if r != nil {
		r.wg.Wait()
	}
}

// StopAndWait cancels and joins the periodic reconciler.
func (r *StaleRunReconciler) StopAndWait() {
	r.Stop()
	r.Wait()
}

// Reconcile performs one stale-run sweep and returns the number of repaired runs.
func (r *StaleRunReconciler) Reconcile(ctx context.Context) (int, error) {
	if r == nil || r.runs == nil || r.ttl <= 0 {
		return 0, nil
	}
	now := r.clock().UTC()
	cutoff := now.Add(-r.ttl)
	runs, err := r.runs.List(ctx, repository.PipelineRunFilter{
		Status:        domain.PipelineStatusRunning,
		StartedBefore: &cutoff,
	}, 500, 0)
	if err != nil {
		return 0, err
	}

	reconciled := 0
	for _, run := range runs {
		if err := ctx.Err(); err != nil {
			return reconciled, err
		}
		message := "stale run: exceeded TTL"
		event := &domain.AgentEvent{PipelineRunID: &run.ID, StrategyID: &run.StrategyID, EventKind: AgentEventKindPipelineFailed.String(), Title: "Pipeline failed", Summary: message, Tags: []string{"pipeline", "failed", "stale"}}
		updateCtx, cancel := context.WithTimeout(ctx, staleRunUpdateTimeout)
		receipt, err := r.runs.Finalize(updateCtx, run.ID, run.TradeDate, repository.PipelineRunFinalization{Status: domain.PipelineStatusFailed, CompletedAt: now, ErrorMessage: message, Event: event})
		cancel()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return reconciled, ctxErr
		}
		if err != nil {
			r.logger.Warn("stale run reconciler failed to update run status",
				slog.String("run_id", run.ID.String()),
				slog.Any("error", err),
			)
			continue
		}
		if !receipt.Applied {
			continue
		}

		cancelled := r.registry != nil && r.registry.Cancel(run.ID, run.TradeDate, runcontrol.Stale)
		r.writeAuditLog(ctx, run, now, cancelled)
		if err := ctx.Err(); err != nil {
			return reconciled, err
		}
		if r.metrics != nil {
			r.metrics.RecordStaleRunReconciled()
		}
		reconciled++
	}
	return reconciled, nil
}

func (r *StaleRunReconciler) writeAuditLog(ctx context.Context, run domain.PipelineRun, now time.Time, cancelled bool) {
	if r.auditLog == nil {
		return
	}
	raw, err := json.Marshal(map[string]any{
		"reason":            "stale run: exceeded TTL",
		"started_at":        run.StartedAt.UTC(),
		"reconciled_at":     now,
		"stale_for":         now.Sub(run.StartedAt).String(),
		"context_cancelled": cancelled,
	})
	if err != nil {
		r.logger.Warn("stale run reconciler failed to marshal audit details", slog.Any("error", err))
		return
	}
	entityID := run.ID
	entry := &domain.AuditLogEntry{
		ID:         uuid.New(),
		EventType:  "pipeline_run.stale_reconciled",
		EntityType: "pipeline_run",
		EntityID:   &entityID,
		Actor:      "system",
		Details:    raw,
		CreatedAt:  now,
	}
	auditCtx, cancel := context.WithTimeout(ctx, r.auditTimeout)
	defer cancel()
	if err := r.auditLog.Create(auditCtx, entry); err != nil {
		r.logger.Warn("stale run reconciler audit log write failed",
			slog.String("run_id", run.ID.String()),
			slog.Any("error", err),
		)
	}
}
