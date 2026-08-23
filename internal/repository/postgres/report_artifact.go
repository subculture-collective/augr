package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// ReportArtifact represents a persisted report row.
type ReportArtifact struct {
	ID               uuid.UUID       `json:"id"`
	StrategyID       uuid.UUID       `json:"strategy_id"`
	ScopeID          *uuid.UUID      `json:"scope_id,omitempty"`
	ScopeLabel       string          `json:"scope_label"`
	AccountID        *uuid.UUID      `json:"account_id,omitempty"`
	BacktestRunID    *uuid.UUID      `json:"backtest_run_id,omitempty"`
	ReportType       string          `json:"report_type"`
	TimeBucket       time.Time       `json:"time_bucket"`
	Status           string          `json:"status"`
	ReportJSON       json.RawMessage `json:"report_json,omitempty"`
	ReportBytes      []byte          `json:"report_bytes,omitempty"`
	ReportSHA256     string          `json:"report_sha256,omitempty"`
	Provider         string          `json:"provider,omitempty"`
	Model            string          `json:"model,omitempty"`
	PromptTokens     int             `json:"prompt_tokens"`
	CompletionTokens int             `json:"completion_tokens"`
	LatencyMs        int             `json:"latency_ms"`
	ErrorMessage     string          `json:"error_message,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	CompletedAt      *time.Time      `json:"completed_at,omitempty"`
}

// ReportArtifactFilter defines supported filters when listing report artifacts.
type ReportArtifactFilter struct {
	StrategyID    *uuid.UUID
	ScopeID       *uuid.UUID
	AccountID     *uuid.UUID
	IncludeLegacy bool
	ReportType    string
	Status        string
}

// ReportArtifactRepo persists report artifacts to PostgreSQL.
type ReportArtifactRepo struct {
	pool *pgxpool.Pool
}

// NewReportArtifactRepo returns a new ReportArtifactRepo.
func NewReportArtifactRepo(pool *pgxpool.Pool) *ReportArtifactRepo {
	return &ReportArtifactRepo{pool: pool}
}

// Upsert inserts or updates a report artifact keyed on
// (strategy_id, report_type, time_bucket).
func (r *ReportArtifactRepo) Upsert(ctx context.Context, a *ReportArtifact) error {
	if a.ScopeID == nil {
		return fmt.Errorf("postgres: upsert report artifact: scope_id is required for new evidence")
	}
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	conflict := `(scope_id, strategy_id, report_type, time_bucket) WHERE scope_id IS NOT NULL`
	reportBytes := a.ReportBytes
	if reportBytes == nil && a.ReportJSON != nil {
		reportBytes = []byte(a.ReportJSON)
	}
	row := r.pool.QueryRow(ctx, fmt.Sprintf(
		`INSERT INTO report_artifacts
			(id, strategy_id, scope_id, backtest_run_id, report_type, time_bucket, status, report_json, report_bytes, report_sha256,
			 provider, model, prompt_tokens, completion_tokens, latency_ms,
			 error_message, completed_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		 ON CONFLICT %s
		 DO UPDATE SET
			status            = CASE WHEN report_artifacts.status='completed' THEN report_artifacts.status ELSE EXCLUDED.status END,
			report_json       = CASE WHEN report_artifacts.status='completed' THEN report_artifacts.report_json ELSE EXCLUDED.report_json END,
			report_bytes      = CASE WHEN report_artifacts.status='completed' THEN report_artifacts.report_bytes ELSE EXCLUDED.report_bytes END,
			report_sha256     = CASE WHEN report_artifacts.status='completed' THEN report_artifacts.report_sha256 ELSE EXCLUDED.report_sha256 END,
			backtest_run_id   = CASE WHEN report_artifacts.status='completed' THEN report_artifacts.backtest_run_id ELSE EXCLUDED.backtest_run_id END,
			provider          = CASE WHEN report_artifacts.status='completed' THEN report_artifacts.provider ELSE EXCLUDED.provider END,
			model             = CASE WHEN report_artifacts.status='completed' THEN report_artifacts.model ELSE EXCLUDED.model END,
			prompt_tokens     = CASE WHEN report_artifacts.status='completed' THEN report_artifacts.prompt_tokens ELSE EXCLUDED.prompt_tokens END,
			completion_tokens = CASE WHEN report_artifacts.status='completed' THEN report_artifacts.completion_tokens ELSE EXCLUDED.completion_tokens END,
			latency_ms        = CASE WHEN report_artifacts.status='completed' THEN report_artifacts.latency_ms ELSE EXCLUDED.latency_ms END,
			error_message     = CASE WHEN report_artifacts.status='completed' THEN report_artifacts.error_message ELSE EXCLUDED.error_message END,
			completed_at      = CASE WHEN report_artifacts.status='completed' THEN report_artifacts.completed_at ELSE EXCLUDED.completed_at END
		 WHERE report_artifacts.status <> 'completed' OR
		       (report_artifacts.scope_id IS NOT DISTINCT FROM EXCLUDED.scope_id AND
		        report_artifacts.backtest_run_id IS NOT DISTINCT FROM EXCLUDED.backtest_run_id AND
		        report_artifacts.report_sha256 IS NOT DISTINCT FROM EXCLUDED.report_sha256 AND
		        report_artifacts.status=EXCLUDED.status)
		 RETURNING id, created_at`, conflict),
		a.ID, a.StrategyID, a.ScopeID, a.BacktestRunID, a.ReportType, a.TimeBucket,
		a.Status, a.ReportJSON, reportBytes, nullString(a.ReportSHA256),
		nullString(a.Provider), nullString(a.Model),
		a.PromptTokens, a.CompletionTokens, a.LatencyMs,
		nullString(a.ErrorMessage), a.CompletedAt,
	)
	if err := row.Scan(&a.ID, &a.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("postgres: report artifact mutation: %w", repository.ErrIdempotencyConflict)
		}
		return fmt.Errorf("postgres: upsert report artifact: %w", err)
	}
	return nil
}

// GetLatest returns the most recently completed report artifact for a
// strategy and report type. Returns repository.ErrNotFound when none exist.
func (r *ReportArtifactRepo) GetLatest(ctx context.Context, accountID, scopeID, strategyID uuid.UUID, reportType string) (*ReportArtifact, error) {
	row := r.pool.QueryRow(ctx,
		reportArtifactSelectSQL+`
		 WHERE a.strategy_id = $1 AND a.report_type = $2 AND a.status = 'completed'
		   AND a.scope_id = $3 AND s.account_id = $4
		 ORDER BY a.completed_at DESC
		 LIMIT 1`,
		strategyID, reportType, scopeID, accountID,
	)
	a, err := scanReportArtifact(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("postgres: get latest report artifact: %w", ErrNotFound)
		}
		return nil, fmt.Errorf("postgres: get latest report artifact: %w", err)
	}
	return a, nil
}

// List returns report artifacts matching the filter, newest first.
func (r *ReportArtifactRepo) List(ctx context.Context, filter ReportArtifactFilter, limit, offset int) ([]ReportArtifact, error) {
	if filter.ScopeID == nil && !filter.IncludeLegacy {
		return nil, fmt.Errorf("postgres: list report artifacts: scope_id required unless legacy is explicit")
	}
	if filter.ScopeID != nil && filter.AccountID == nil {
		return nil, fmt.Errorf("postgres: list report artifacts: account_id required for scoped read")
	}
	if limit <= 0 {
		limit = 50
	}

	query := reportArtifactSelectSQL + ` WHERE 1=1`
	var args []any
	argN := 0
	nextArg := func(v any) string {
		argN++
		args = append(args, v)
		return fmt.Sprintf("$%d", argN)
	}

	if filter.StrategyID != nil {
		query += fmt.Sprintf(" AND a.strategy_id = %s", nextArg(*filter.StrategyID))
	}
	if filter.ScopeID != nil {
		query += fmt.Sprintf(" AND a.scope_id = %s", nextArg(*filter.ScopeID))
		query += fmt.Sprintf(" AND s.account_id = %s", nextArg(*filter.AccountID))
	} else {
		query += " AND a.scope_id IS NULL"
	}
	if filter.ReportType != "" {
		query += fmt.Sprintf(" AND a.report_type = %s", nextArg(filter.ReportType))
	}
	if filter.Status != "" {
		query += fmt.Sprintf(" AND a.status = %s", nextArg(filter.Status))
	}

	query += " ORDER BY a.time_bucket DESC"
	query += fmt.Sprintf(" LIMIT %s OFFSET %s", nextArg(limit), nextArg(offset))

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: list report artifacts: %w", err)
	}
	defer rows.Close()

	var artifacts []ReportArtifact
	for rows.Next() {
		a, err := scanReportArtifact(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scan report artifact: %w", err)
		}
		artifacts = append(artifacts, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list report artifacts rows: %w", err)
	}
	return artifacts, nil
}

const reportArtifactSelectSQL = `SELECT a.id, a.strategy_id, a.scope_id, s.account_id, a.backtest_run_id,
 a.report_type, a.time_bucket, a.status, a.report_json, a.report_bytes, a.report_sha256,
 a.provider, a.model, a.prompt_tokens, a.completion_tokens, a.latency_ms,
 a.error_message, a.created_at, a.completed_at
 FROM report_artifacts a LEFT JOIN paper_evaluation_scopes s ON s.id=a.scope_id`

func scanReportArtifact(sc scanner) (*ReportArtifact, error) {
	var (
		a            ReportArtifact
		provider     *string
		model        *string
		errorMessage *string
		reportSHA    *string
		completedAt  *time.Time
		reportJSON   []byte
	)
	err := sc.Scan(
		&a.ID, &a.StrategyID, &a.ScopeID, &a.AccountID, &a.BacktestRunID,
		&a.ReportType, &a.TimeBucket, &a.Status, &reportJSON, &a.ReportBytes, &reportSHA,
		&provider, &model,
		&a.PromptTokens, &a.CompletionTokens, &a.LatencyMs,
		&errorMessage, &a.CreatedAt, &completedAt,
	)
	if err != nil {
		return nil, err
	}
	if provider != nil {
		a.Provider = *provider
	}
	if model != nil {
		a.Model = *model
	}
	if errorMessage != nil {
		a.ErrorMessage = *errorMessage
	}
	if reportSHA != nil {
		a.ReportSHA256 = *reportSHA
	}
	if completedAt != nil {
		a.CompletedAt = completedAt
	}
	if reportJSON != nil {
		a.ReportJSON = reportJSON
	}
	if a.ScopeID == nil {
		a.ScopeLabel = "legacy_unscoped"
	} else {
		a.ScopeLabel = "scoped"
	}
	return &a, nil
}
