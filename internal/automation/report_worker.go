package automation

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/backtest"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/eventmarkets"
	"github.com/PatrickFanella/get-rich-quick/internal/papervalidation"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	pgrepo "github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
)

// ReportArtifactWriter persists report artifacts.
type ReportArtifactWriter interface {
	Upsert(context.Context, *pgrepo.ReportArtifact) error
}

type reportWorkerDeps struct {
	StrategyRepo       repository.StrategyRepository
	BacktestConfigRepo repository.BacktestConfigRepository
	BacktestRunRepo    repository.BacktestRunRepository
	ReportArtifactRepo ReportArtifactWriter
}

// ReportWorker owns paper validation report generation and persistence.
type ReportWorker struct {
	deps        reportWorkerDeps
	logger      *slog.Logger
	metrics     ReportWorkerMetrics
	now         func() time.Time
	lastSummary map[string]int
}

type reportGenerationOutcome int

const (
	reportGenerationCompleted reportGenerationOutcome = iota
	reportGenerationPending
)

// NewReportWorker constructs a report worker with safe defaults.
func NewReportWorker(deps reportWorkerDeps, logger *slog.Logger, metrics ReportWorkerMetrics) *ReportWorker {
	if logger == nil {
		logger = slog.Default()
	}
	return &ReportWorker{
		deps:    deps,
		logger:  logger,
		metrics: metrics,
		now:     time.Now,
	}
}

func (w *ReportWorker) LastSummary() map[string]int {
	return cloneSummary(w.lastSummary)
}

// RunPaperValidationReport generates a report for each active paper strategy.
func (w *ReportWorker) RunPaperValidationReport(ctx context.Context) error {
	if w.deps.StrategyRepo == nil {
		return fmt.Errorf("paper_validation_report: strategy repo not configured")
	}
	if w.now == nil {
		w.now = time.Now
	}

	w.logger.Info("paper_validation_report: starting")

	strategies, err := listAllStrategies(ctx, w.deps.StrategyRepo, repository.StrategyFilter{Status: domain.StrategyStatusActive})
	if err != nil {
		return fmt.Errorf("paper_validation_report: list strategies: %w", err)
	}

	type paperEntry struct {
		Strategy domain.Strategy
		Config   domain.BacktestConfig
	}
	var paperStrategies []paperEntry
	skipped := 0
	for _, s := range strategies {
		if !s.IsPaper || eventmarkets.IsEventMarket(s.MarketType) {
			skipped++
			continue
		}
		if w.deps.BacktestConfigRepo == nil {
			return fmt.Errorf("paper_validation_report: backtest config repo not configured")
		}
		configs, listErr := w.deps.BacktestConfigRepo.List(ctx, repository.BacktestConfigFilter{StrategyID: &s.ID}, 10_000, 0)
		if listErr != nil {
			return fmt.Errorf("paper_validation_report: list configs for %s: %w", s.Name, listErr)
		}
		if len(configs) == 0 {
			skipped++
			continue
		}
		for _, config := range configs {
			if config.ScopeID == nil {
				skipped++
				continue
			}
			paperStrategies = append(paperStrategies, paperEntry{Strategy: s, Config: config})
		}
	}
	if len(paperStrategies) == 0 {
		w.lastSummary = map[string]int{"scanned": len(strategies), "eligible": 0, "skipped": skipped, "succeeded": 0, "pending": 0, "failed": 0}
		w.logger.Info("paper_validation_report: no active paper strategies")
		return nil
	}

	w.logger.Info("paper_validation_report: processing", slog.Int("strategies", len(paperStrategies)))

	now := w.now().UTC()
	timeBucket := now.Truncate(24 * time.Hour)
	var succeeded, pending, failed int

	for _, ps := range paperStrategies {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		outcome, err := w.generateConfigReport(ctx, ps.Strategy, ps.Config, timeBucket, timeBucket)
		switch {
		case err != nil:
			failed++
			if w.metrics != nil {
				w.metrics.RecordReportWorkerError(ps.Strategy.ID.String())
			}
			w.logger.Warn("paper_validation_report: strategy failed",
				slog.String("strategy", ps.Strategy.Name),
				slog.Any("error", err),
			)
		case outcome == reportGenerationPending:
			pending++
		default:
			succeeded++
			if w.metrics != nil {
				w.metrics.RecordReportWorkerSuccess(ps.Strategy.ID.String())
			}
		}
	}

	w.logger.Info("paper_validation_report: completed",
		slog.Int("succeeded", succeeded),
		slog.Int("pending", pending),
		slog.Int("failed", failed),
	)
	w.lastSummary = map[string]int{"scanned": len(strategies), "eligible": len(paperStrategies), "skipped": skipped, "succeeded": succeeded, "pending": pending, "failed": failed}
	if failed > 0 {
		return fmt.Errorf("paper_validation_report: %d of %d eligible strategies failed", failed, len(paperStrategies))
	}
	return nil
}

func (w *ReportWorker) generateConfigReport(
	ctx context.Context,
	strategy domain.Strategy,
	config domain.BacktestConfig,
	timeBucket, now time.Time,
) (reportGenerationOutcome, error) {
	start := time.Now()

	if w.deps.BacktestConfigRepo == nil || w.deps.BacktestRunRepo == nil {
		return reportGenerationCompleted, fmt.Errorf("backtest repos not configured")
	}

	if config.ScopeID == nil {
		return reportGenerationCompleted, fmt.Errorf("legacy_unscoped: backtest config %s", config.ID)
	}
	evaluationDate := now
	if evaluationDate.After(config.EndDate) {
		evaluationDate = config.EndDate
	}

	strategyID := strategy.ID
	configID := config.ID
	runs, err := w.deps.BacktestRunRepo.List(ctx, repository.BacktestRunFilter{BacktestConfigID: &configID, ScopeID: config.ScopeID}, 1, 0)
	if err != nil {
		return reportGenerationCompleted, w.persistErrorArtifact(ctx, strategyID, config.ScopeID, nil, timeBucket, fmt.Errorf("list backtest runs: %w", err))
	}
	if len(runs) == 0 {
		w.logger.Info("paper_validation_report: no backtest runs yet, persisting pending artifact",
			slog.String("strategy", strategy.Name),
		)
		return reportGenerationPending, w.persistPendingArtifact(ctx, strategyID, *config.ScopeID, timeBucket, time.Since(start))
	}

	var btMetrics backtest.Metrics
	var analytics backtest.TradeAnalytics
	latestRun := runs[0]
	if latestRun.ScopeID == nil || *latestRun.ScopeID != *config.ScopeID {
		return reportGenerationCompleted, fmt.Errorf("scope mismatch between backtest config and run")
	}
	if err := json.Unmarshal(latestRun.Metrics, &btMetrics); err != nil {
		return reportGenerationCompleted, w.persistErrorArtifact(ctx, strategyID, config.ScopeID, &latestRun.ID, timeBucket, fmt.Errorf("unmarshal metrics: %w", err))
	}
	if len(latestRun.TradeLog) > 0 {
		var trades []domain.Trade
		if err := json.Unmarshal(latestRun.TradeLog, &trades); err != nil {
			return reportGenerationCompleted, w.persistErrorArtifact(ctx, strategyID, config.ScopeID, &latestRun.ID, timeBucket, fmt.Errorf("unmarshal trade log: %w", err))
		}
		analytics = backtest.ComputeTradeAnalytics(trades, btMetrics.StartTime, btMetrics.EndTime)
	} else {
		analytics = backtest.ComputeTradeAnalytics(nil, btMetrics.StartTime, btMetrics.EndTime)
	}

	report := papervalidation.GenerateReport(btMetrics, analytics, papervalidation.DefaultThresholds(), config.StartDate, evaluationDate)

	reportJSON, err := json.Marshal(report)
	if err != nil {
		return reportGenerationCompleted, w.persistErrorArtifact(ctx, strategyID, config.ScopeID, &latestRun.ID, timeBucket, fmt.Errorf("marshal report: %w", err))
	}

	latencyMs := int(time.Since(start).Milliseconds())
	completed := time.Now().UTC()
	artifact := &pgrepo.ReportArtifact{
		StrategyID:    strategyID,
		ScopeID:       config.ScopeID,
		BacktestRunID: &latestRun.ID,
		ReportType:    reportTypePaperValidation,
		TimeBucket:    timeBucket,
		Status:        "completed",
		ReportJSON:    reportJSON,
		ReportSHA256:  fmt.Sprintf("%x", sha256.Sum256(reportJSON)),
		LatencyMs:     latencyMs,
		CompletedAt:   &completed,
	}
	if w.deps.ReportArtifactRepo == nil {
		return reportGenerationCompleted, fmt.Errorf("persist report: report artifact repo not configured")
	}
	if err := w.deps.ReportArtifactRepo.Upsert(ctx, artifact); err != nil {
		return reportGenerationCompleted, fmt.Errorf("persist report: %w", err)
	}

	w.logger.Info("paper_validation_report: generated",
		slog.String("strategy", strategy.Name),
		slog.String("decision", report.Decision),
		slog.Int("latency_ms", latencyMs),
	)
	return reportGenerationCompleted, nil
}

func (w *ReportWorker) generateOneReport(ctx context.Context, strategyID uuid.UUID, strategyName string, timeBucket, now time.Time) (reportGenerationOutcome, error) {
	strategy, err := w.deps.StrategyRepo.Get(ctx, strategyID)
	if err != nil {
		return reportGenerationCompleted, fmt.Errorf("get strategy: %w", err)
	}
	configs, err := w.deps.BacktestConfigRepo.List(ctx, repository.BacktestConfigFilter{StrategyID: &strategyID}, 10_000, 0)
	if err != nil {
		return reportGenerationCompleted, fmt.Errorf("list backtest configs: %w", err)
	}
	for _, config := range configs {
		if config.ScopeID != nil {
			return w.generateConfigReport(ctx, *strategy, config, timeBucket, now)
		}
	}
	return reportGenerationCompleted, fmt.Errorf("no scoped backtest configs found for strategy %s", strategyName)
}

func (w *ReportWorker) persistPendingArtifact(
	ctx context.Context,
	strategyID uuid.UUID,
	scopeID uuid.UUID,
	timeBucket time.Time,
	elapsed time.Duration,
) error {
	if w.deps.ReportArtifactRepo == nil {
		return fmt.Errorf("persist pending report: report artifact repo not configured")
	}
	reportJSON, err := json.Marshal(map[string]string{
		"state":  "pending",
		"reason": "no_backtest_runs",
	})
	if err != nil {
		return fmt.Errorf("marshal pending report: %w", err)
	}
	artifact := &pgrepo.ReportArtifact{
		StrategyID: strategyID,
		ScopeID:    &scopeID,
		ReportType: reportTypePaperValidation,
		TimeBucket: timeBucket,
		Status:     "pending",
		ReportJSON: reportJSON,
		LatencyMs:  int(elapsed.Milliseconds()),
	}
	if err := w.deps.ReportArtifactRepo.Upsert(ctx, artifact); err != nil {
		return fmt.Errorf("persist pending report: %w", err)
	}
	return nil
}

func (w *ReportWorker) persistErrorArtifact(
	ctx context.Context,
	strategyID uuid.UUID,
	scopeID *uuid.UUID,
	backtestRunID *uuid.UUID,
	timeBucket time.Time,
	origErr error,
) error {
	if w.deps.ReportArtifactRepo == nil {
		w.logger.Error("paper_validation_report: cannot persist error artifact (repo nil)", slog.Any("original_error", origErr))
		return origErr
	}
	completed := time.Now().UTC()
	artifact := &pgrepo.ReportArtifact{
		StrategyID:    strategyID,
		ScopeID:       scopeID,
		BacktestRunID: backtestRunID,
		ReportType:    reportTypePaperValidation,
		TimeBucket:    timeBucket,
		Status:        "error",
		ErrorMessage:  origErr.Error(),
		CompletedAt:   &completed,
	}
	if err := w.deps.ReportArtifactRepo.Upsert(ctx, artifact); err != nil {
		w.logger.Error("paper_validation_report: failed to persist error artifact",
			slog.String("original_error_type", fmt.Sprintf("%T", origErr)),
			slog.String("persist_error_type", fmt.Sprintf("%T", err)),
		)
		return errors.Join(origErr, fmt.Errorf("persist error artifact: %w", err))
	}
	return origErr
}
