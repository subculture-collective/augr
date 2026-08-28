package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// PipelineRunRepo implements repository.PipelineRunRepository using PostgreSQL.
type PipelineRunRepo struct {
	pool      *pgxpool.Pool
	accountID uuid.UUID
}

// Compile-time check that PipelineRunRepo satisfies PipelineRunRepository.
var _ repository.PipelineRunRepository = (*PipelineRunRepo)(nil)

const pipelineRunSelectColumns = `id, account_id, environment, origin_type, origin_id, strategy_id, ticker, trade_date, status, signal, started_at, completed_at, error_message, config_snapshot, phase_timings`

// NewPipelineRunRepo returns a PipelineRunRepo backed by the given connection
// pool.
func NewPipelineRunRepo(pool *pgxpool.Pool, accountID uuid.UUID) *PipelineRunRepo {
	return &PipelineRunRepo{pool: pool, accountID: accountID}
}

// Create inserts a new pipeline run, generating an ID only when one was not
// supplied by the caller.
func (r *PipelineRunRepo) Create(ctx context.Context, run *domain.PipelineRun) error {
	if run.AccountID != uuid.Nil && run.AccountID != r.accountID {
		return fmt.Errorf("postgres: create pipeline run: account mismatch")
	}
	run.AccountID = r.accountID
	configSnapshot, err := marshalConfigSnapshot(run.ConfigSnapshot)
	if err != nil {
		return err
	}

	if run.ID == uuid.Nil {
		run.ID = uuid.New()
	}

	query := `INSERT INTO pipeline_runs (
			id, strategy_id, ticker, trade_date, status, signal, started_at, completed_at, error_message, config_snapshot, phase_timings,
			account_id, environment, origin_type, origin_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`
	args := []any{
		run.ID,
		run.StrategyID,
		run.Ticker,
		run.TradeDate,
		run.Status,
		run.Signal,
		run.StartedAt,
		run.CompletedAt,
		run.ErrorMessage,
		configSnapshot,
		run.PhaseTimings,
		r.accountID, run.Environment, run.OriginType, run.OriginID,
	}
	_, err = r.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("postgres: create pipeline run: %w", err)
	}

	return nil
}

// Get retrieves a pipeline run by its composite key. It returns ErrNotFound
// when no row matches.
func (r *PipelineRunRepo) Get(ctx context.Context, ref domain.PipelineRunRef) (*domain.PipelineRun, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT `+pipelineRunSelectColumns+`
		 FROM pipeline_runs
		 WHERE id = $1 AND trade_date = $2::date AND account_id = $3`,
		ref.ID, ref.TradeDate, r.accountID,
	)

	run, err := scanPipelineRun(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("postgres: get pipeline run %s on %s: %w", ref.ID, ref.TradeDate.Format("2006-01-02"), ErrNotFound)
		}
		return nil, fmt.Errorf("postgres: get pipeline run: %w", err)
	}

	return run, nil
}

// List returns pipeline runs matching the provided filter with pagination.
func (r *PipelineRunRepo) List(ctx context.Context, filter repository.PipelineRunFilter, limit, offset int) ([]domain.PipelineRun, error) {
	query, args := buildPipelineRunListQuery(r.accountID, filter, limit, offset)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: list pipeline runs: %w", err)
	}
	defer rows.Close()

	var runs []domain.PipelineRun
	for rows.Next() {
		run, err := scanPipelineRun(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: list pipeline runs scan: %w", err)
		}
		runs = append(runs, *run)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list pipeline runs rows: %w", err)
	}

	return runs, nil
}

// Count returns the total number of pipeline runs matching the filter,
// ignoring any pagination (limit/offset).
func (r *PipelineRunRepo) Count(ctx context.Context, filter repository.PipelineRunFilter) (int, error) {
	query, args := buildPipelineRunCountQuery(r.accountID, filter)
	var total int
	if err := r.pool.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("postgres: count pipeline runs: %w", err)
	}
	return total, nil
}

func (r *PipelineRunRepo) CountBySignal(ctx context.Context, filter repository.PipelineRunFilter) (map[domain.PipelineSignal]int, error) {
	query, args := buildPipelineRunGroupedCountQuery("signal", r.accountID, filter)
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: count pipeline runs by signal: %w", err)
	}
	defer rows.Close()
	out := map[domain.PipelineSignal]int{}
	for rows.Next() {
		var key string
		var total int
		if err := rows.Scan(&key, &total); err != nil {
			return nil, fmt.Errorf("postgres: count pipeline runs by signal scan: %w", err)
		}
		out[domain.PipelineSignal(key)] = total
	}
	return out, rows.Err()
}

func (r *PipelineRunRepo) CountByStatus(ctx context.Context, filter repository.PipelineRunFilter) (map[domain.PipelineStatus]int, error) {
	query, args := buildPipelineRunGroupedCountQuery("status", r.accountID, filter)
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: count pipeline runs by status: %w", err)
	}
	defer rows.Close()
	out := map[domain.PipelineStatus]int{}
	for rows.Next() {
		var key string
		var total int
		if err := rows.Scan(&key, &total); err != nil {
			return nil, fmt.Errorf("postgres: count pipeline runs by status scan: %w", err)
		}
		out[domain.PipelineStatus(key)] = total
	}
	return out, rows.Err()
}

// buildPipelineRunCountQuery constructs a SELECT COUNT(*) query for pipeline runs
// with the same filter conditions used by buildPipelineRunListQuery.
func buildPipelineRunCountQuery(accountID uuid.UUID, filter repository.PipelineRunFilter) (string, []any) {
	var (
		conditions []string
		args       []any
		argIdx     int
	)

	nextArg := func(v any) string {
		argIdx++
		args = append(args, v)
		return fmt.Sprintf("$%d", argIdx)
	}
	conditions = append(conditions, "account_id = "+nextArg(accountID))

	if filter.StrategyID != nil {
		conditions = append(conditions, "strategy_id = "+nextArg(*filter.StrategyID))
	}
	if filter.Ticker != "" {
		conditions = append(conditions, "ticker = "+nextArg(filter.Ticker))
	}
	if filter.Status != "" {
		conditions = append(conditions, "status = "+nextArg(filter.Status))
	}
	if filter.TradeDate != nil {
		conditions = append(conditions, "trade_date = "+nextArg(*filter.TradeDate)+"::date")
	}
	if filter.StartedAfter != nil {
		conditions = append(conditions, "started_at >= "+nextArg(*filter.StartedAfter))
	}
	if filter.StartedBefore != nil {
		conditions = append(conditions, "started_at <= "+nextArg(*filter.StartedBefore))
	}

	base := `SELECT COUNT(*) FROM pipeline_runs`
	if len(conditions) > 0 {
		base += " WHERE " + strings.Join(conditions, " AND ")
	}
	return base, args
}

func buildPipelineRunGroupedCountQuery(column string, accountID uuid.UUID, filter repository.PipelineRunFilter) (string, []any) {
	base, args := buildPipelineRunCountQuery(accountID, filter)
	return strings.Replace(base, "SELECT COUNT(*) FROM pipeline_runs", fmt.Sprintf("SELECT %s, COUNT(*) FROM pipeline_runs", column), 1) + " GROUP BY " + column + " ORDER BY " + column, args
}

// Finalize atomically changes a running pipeline run to a terminal state and,
// when provided, inserts its terminal audit event in the same transaction.
func (r *PipelineRunRepo) Finalize(ctx context.Context, ref domain.PipelineRunRef, finalization repository.PipelineRunFinalization) (repository.PipelineRunFinalizationReceipt, error) {
	if err := validatePipelineRunFinalization(ref.ID, finalization); err != nil {
		return repository.PipelineRunFinalizationReceipt{}, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return repository.PipelineRunFinalizationReceipt{}, fmt.Errorf("postgres: begin pipeline run finalization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	run, err := scanPipelineRun(tx.QueryRow(ctx,
		`SELECT `+pipelineRunSelectColumns+` FROM pipeline_runs WHERE id = $1 AND trade_date = $2::date AND account_id = $3 FOR UPDATE`,
		ref.ID, ref.TradeDate, r.accountID,
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.PipelineRunFinalizationReceipt{}, fmt.Errorf("postgres: finalize pipeline run %s on %s: %w", ref.ID, ref.TradeDate.Format("2006-01-02"), ErrNotFound)
		}
		return repository.PipelineRunFinalizationReceipt{}, fmt.Errorf("postgres: lock pipeline run for finalization: %w", err)
	}

	if run.Status != domain.PipelineStatusRunning {
		if err := tx.Commit(ctx); err != nil {
			return repository.PipelineRunFinalizationReceipt{}, fmt.Errorf("postgres: commit pipeline run finalization loser: %w", err)
		}
		return repository.PipelineRunFinalizationReceipt{Run: *run}, nil
	}
	if finalization.Event != nil && finalization.Event.StrategyID != nil && *finalization.Event.StrategyID != run.StrategyID {
		return repository.PipelineRunFinalizationReceipt{}, fmt.Errorf("postgres: terminal agent event strategy does not match pipeline run")
	}

	run, err = scanPipelineRun(tx.QueryRow(ctx,
		`UPDATE pipeline_runs
		 SET status = $1, completed_at = $2, error_message = $3,
		     signal = COALESCE($4, signal), phase_timings = COALESCE($5, phase_timings)
		 WHERE id = $6 AND trade_date = $7::date AND status = $8 AND account_id = $9
		 RETURNING `+pipelineRunSelectColumns,
		finalization.Status, finalization.CompletedAt, finalization.ErrorMessage,
		finalization.Signal, nullableJSON(finalization.PhaseTimings), ref.ID, ref.TradeDate, domain.PipelineStatusRunning, r.accountID,
	))
	if err != nil {
		return repository.PipelineRunFinalizationReceipt{}, fmt.Errorf("postgres: update pipeline run finalization: %w", err)
	}

	if finalization.Event != nil {
		finalization.Event.AccountID, finalization.Event.Environment = run.AccountID, run.Environment
		finalization.Event.OriginType, finalization.Event.OriginID = run.OriginType, run.OriginID
		finalization.Event.PipelineRunTradeDate = &run.TradeDate
		if err := insertTerminalAgentEvent(ctx, tx, finalization.Event); err != nil {
			return repository.PipelineRunFinalizationReceipt{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return repository.PipelineRunFinalizationReceipt{}, fmt.Errorf("postgres: commit pipeline run finalization: %w", err)
	}
	return repository.PipelineRunFinalizationReceipt{Applied: true, Run: *run}, nil
}

// RefineCompletedSignal atomically replaces only the signal of a completed run.
func (r *PipelineRunRepo) RefineCompletedSignal(ctx context.Context, ref domain.PipelineRunRef, expected, signal domain.PipelineSignal) (repository.PipelineRunFinalizationReceipt, error) {
	if expected != "" && !expected.IsValid() {
		return repository.PipelineRunFinalizationReceipt{}, fmt.Errorf("postgres: expected pipeline signal %q is invalid", expected)
	}
	if !signal.IsValid() {
		return repository.PipelineRunFinalizationReceipt{}, fmt.Errorf("postgres: refined pipeline signal %q is invalid", signal)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return repository.PipelineRunFinalizationReceipt{}, fmt.Errorf("postgres: begin completed signal refinement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	run, err := scanPipelineRun(tx.QueryRow(ctx,
		`SELECT `+pipelineRunSelectColumns+` FROM pipeline_runs WHERE id = $1 AND trade_date = $2::date AND account_id = $3 FOR UPDATE`,
		ref.ID, ref.TradeDate, r.accountID,
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.PipelineRunFinalizationReceipt{}, fmt.Errorf("postgres: refine pipeline run signal %s on %s: %w", ref.ID, ref.TradeDate.Format("2006-01-02"), ErrNotFound)
		}
		return repository.PipelineRunFinalizationReceipt{}, fmt.Errorf("postgres: lock pipeline run for signal refinement: %w", err)
	}

	applied := false
	if run.Status == domain.PipelineStatusCompleted && run.Signal == signal {
		applied = true
	} else if run.Status == domain.PipelineStatusCompleted && run.Signal == expected {
		run, err = scanPipelineRun(tx.QueryRow(ctx,
			`UPDATE pipeline_runs SET signal = $1
			 WHERE id = $2 AND trade_date = $3::date AND status = $4 AND signal = $5 AND account_id = $6
			 RETURNING `+pipelineRunSelectColumns,
			signal, ref.ID, ref.TradeDate, domain.PipelineStatusCompleted, expected, r.accountID,
		))
		if err != nil {
			return repository.PipelineRunFinalizationReceipt{}, fmt.Errorf("postgres: refine completed pipeline run signal: %w", err)
		}
		applied = true
	}

	if err := tx.Commit(ctx); err != nil {
		return repository.PipelineRunFinalizationReceipt{}, fmt.Errorf("postgres: commit completed signal refinement: %w", err)
	}
	return repository.PipelineRunFinalizationReceipt{Applied: applied, Run: *run}, nil
}

func validatePipelineRunFinalization(id uuid.UUID, finalization repository.PipelineRunFinalization) error {
	if !domain.PipelineStatusRunning.CanTransitionTo(finalization.Status) {
		return fmt.Errorf("postgres: invalid pipeline run finalization status %q", finalization.Status)
	}
	if finalization.CompletedAt.IsZero() {
		return fmt.Errorf("postgres: pipeline run finalization completion timestamp is required")
	}
	if finalization.Status == domain.PipelineStatusCompleted && finalization.ErrorMessage != "" {
		return fmt.Errorf("postgres: completed pipeline run cannot have an error message")
	}
	if finalization.Status != domain.PipelineStatusCompleted && strings.TrimSpace(finalization.ErrorMessage) == "" {
		return fmt.Errorf("postgres: failed or cancelled pipeline run requires an error message")
	}
	if finalization.Signal != nil && !finalization.Signal.IsValid() {
		return fmt.Errorf("postgres: pipeline run finalization signal %q is invalid", *finalization.Signal)
	}
	if len(finalization.PhaseTimings) > 0 && !json.Valid(finalization.PhaseTimings) {
		return fmt.Errorf("postgres: pipeline run phase timings are not valid JSON")
	}
	if finalization.Event == nil {
		return nil
	}
	if finalization.Event.PipelineRunID == nil || *finalization.Event.PipelineRunID != id {
		return fmt.Errorf("postgres: terminal agent event does not match pipeline run")
	}
	expectedKind := "pipeline_failed"
	switch finalization.Status {
	case domain.PipelineStatusCompleted:
		expectedKind = "pipeline_completed"
	case domain.PipelineStatusCancelled:
		expectedKind = "pipeline_cancelled"
	}
	if finalization.Event.EventKind != expectedKind {
		return fmt.Errorf("postgres: terminal agent event kind %q does not match status %q", finalization.Event.EventKind, finalization.Status)
	}
	if len(finalization.Event.Metadata) > 0 && !json.Valid(finalization.Event.Metadata) {
		return fmt.Errorf("postgres: terminal agent event metadata is not valid JSON")
	}
	return nil
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return []byte(value)
}

func insertTerminalAgentEvent(ctx context.Context, tx pgx.Tx, event *domain.AgentEvent) error {
	metadata, err := marshalAgentEventMetadata(event.Metadata)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO agent_events (account_id, environment, origin_type, origin_id, pipeline_run_id, pipeline_run_trade_date, strategy_id, agent_role, event_kind, title, summary, tags, metadata)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		event.AccountID, event.Environment, event.OriginType, event.OriginID, event.PipelineRunID, event.PipelineRunTradeDate, event.StrategyID, nullString(event.AgentRole.String()), event.EventKind,
		event.Title, nullString(event.Summary), event.Tags, metadata,
	)
	if err != nil {
		return fmt.Errorf("postgres: insert terminal agent event: %w", err)
	}
	return nil
}

// scanPipelineRun scans a single row (pgx.Row or pgx.Rows) into a PipelineRun.
func scanPipelineRun(sc scanner) (*domain.PipelineRun, error) {
	var (
		run                domain.PipelineRun
		signal             string
		configSnapshotJSON []byte
		phaseTimingsJSON   []byte
	)

	err := sc.Scan(
		&run.ID,
		&run.AccountID, &run.Environment, &run.OriginType, &run.OriginID,
		&run.StrategyID,
		&run.Ticker,
		&run.TradeDate,
		&run.Status,
		&signal,
		&run.StartedAt,
		&run.CompletedAt,
		&run.ErrorMessage,
		&configSnapshotJSON,
		&phaseTimingsJSON,
	)
	if err != nil {
		return nil, err
	}

	run.Signal = domain.PipelineSignal(signal)
	if configSnapshotJSON != nil {
		run.ConfigSnapshot = json.RawMessage(configSnapshotJSON)
	}
	if phaseTimingsJSON != nil {
		run.PhaseTimings = json.RawMessage(phaseTimingsJSON)
	}

	return &run, nil
}

// buildPipelineRunListQuery constructs the SELECT query and arguments for List
// with dynamic WHERE conditions. All values are parameterized.
func buildPipelineRunListQuery(accountID uuid.UUID, filter repository.PipelineRunFilter, limit, offset int) (string, []any) {
	var (
		conditions []string
		args       []any
		argIdx     int
	)

	nextArg := func(v any) string {
		argIdx++
		args = append(args, v)
		return fmt.Sprintf("$%d", argIdx)
	}
	conditions = append(conditions, "account_id = "+nextArg(accountID))

	if filter.StrategyID != nil {
		conditions = append(conditions, "strategy_id = "+nextArg(*filter.StrategyID))
	}

	if filter.Ticker != "" {
		conditions = append(conditions, "ticker = "+nextArg(filter.Ticker))
	}

	if filter.Status != "" {
		conditions = append(conditions, "status = "+nextArg(filter.Status))
	}

	if filter.TradeDate != nil {
		conditions = append(conditions, "trade_date = "+nextArg(*filter.TradeDate)+"::date")
	}

	if filter.StartedAfter != nil {
		conditions = append(conditions, "started_at >= "+nextArg(*filter.StartedAfter))
	}

	if filter.StartedBefore != nil {
		conditions = append(conditions, "started_at <= "+nextArg(*filter.StartedBefore))
	}

	base := `SELECT ` + pipelineRunSelectColumns + `
		 FROM pipeline_runs`

	if len(conditions) > 0 {
		base += " WHERE " + strings.Join(conditions, " AND ")
	}

	base += " ORDER BY started_at DESC, id DESC"
	base += fmt.Sprintf(" LIMIT %s OFFSET %s", nextArg(limit), nextArg(offset))

	return base, args
}

// marshalConfigSnapshot ensures the config_snapshot JSONB value is valid JSON.
// A nil or empty value is stored as SQL NULL.
func marshalConfigSnapshot(cfg json.RawMessage) ([]byte, error) {
	if len(cfg) == 0 {
		return nil, nil
	}

	if !json.Valid(cfg) {
		return nil, fmt.Errorf("postgres: pipeline run config snapshot is not valid JSON")
	}

	return cfg, nil
}
