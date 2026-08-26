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

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/eventmarkets"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type OvernightBacktestRunRepo struct{ pool *pgxpool.Pool }

var (
	_ repository.OvernightBacktestRunRepository = (*OvernightBacktestRunRepo)(nil)
	_ repository.OvernightBacktestRunCommitter  = (*OvernightBacktestRunRepo)(nil)
	_ repository.OvernightBacktestRunReconciler = (*OvernightBacktestRunRepo)(nil)
)

func NewOvernightBacktestRunRepo(pool *pgxpool.Pool) *OvernightBacktestRunRepo {
	return &OvernightBacktestRunRepo{pool: pool}
}

func (r *OvernightBacktestRunRepo) Create(ctx context.Context, run *domain.OvernightBacktestRun) error {
	if run.ID == uuid.Nil {
		run.ID = uuid.New()
	}
	candidates, generated, errs, summary, err := marshalOvernightBacktestRunJSON(*run)
	if err != nil {
		return err
	}
	row := r.pool.QueryRow(ctx, `INSERT INTO overnight_backtest_runs
		(id, status, phase, candidate_index, candidates, generated, errors, summary, completed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING started_at, updated_at`,
		run.ID, run.Status, run.Phase, run.CandidateIndex, candidates, generated, errs, summary, run.CompletedAt,
	)
	if err := row.Scan(&run.StartedAt, &run.UpdatedAt); err != nil {
		return fmt.Errorf("postgres: create overnight backtest run: %w", err)
	}
	return nil
}

func (r *OvernightBacktestRunRepo) Get(ctx context.Context, id uuid.UUID) (*domain.OvernightBacktestRun, error) {
	run, err := scanOvernightBacktestRun(r.pool.QueryRow(ctx, overnightBacktestSelectSQL+` WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("postgres: get overnight backtest run %s: %w", id, repository.ErrNotFound)
		}
		return nil, fmt.Errorf("postgres: get overnight backtest run: %w", err)
	}
	return run, nil
}

func (r *OvernightBacktestRunRepo) GetActive(ctx context.Context) (*domain.OvernightBacktestRun, error) {
	run, err := scanOvernightBacktestRun(r.pool.QueryRow(ctx, overnightBacktestSelectSQL+` WHERE status = 'running' ORDER BY updated_at DESC LIMIT 1`))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: get active overnight backtest run: %w", err)
	}
	return run, nil
}

func (r *OvernightBacktestRunRepo) SaveIfRunning(ctx context.Context, run *domain.OvernightBacktestRun) error {
	candidates, generated, errs, summary, err := marshalOvernightBacktestRunJSON(*run)
	if err != nil {
		return err
	}
	row := r.pool.QueryRow(ctx, `UPDATE overnight_backtest_runs SET
		status = $2, phase = $3, candidate_index = $4, candidates = $5, generated = $6, errors = $7, summary = $8, completed_at = $9, updated_at = NOW()
		WHERE id = $1 AND status = 'running' RETURNING updated_at`,
		run.ID, run.Status, run.Phase, run.CandidateIndex, candidates, generated, errs, summary, run.CompletedAt,
	)
	if err := row.Scan(&run.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return r.classifyOvernightBacktestRunNoRows(ctx, run.ID, "save")
		}
		return fmt.Errorf("postgres: save overnight backtest run: %w", err)
	}
	return nil
}

func (r *OvernightBacktestRunRepo) classifyOvernightBacktestRunNoRows(ctx context.Context, id uuid.UUID, operation string) error {
	var status string
	err := r.pool.QueryRow(ctx, `SELECT status FROM overnight_backtest_runs WHERE id = $1`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("postgres: %s overnight backtest run %s: %w", operation, id, repository.ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("postgres: classify overnight backtest run %s after %s: %w", id, operation, err)
	}
	return fmt.Errorf("postgres: %s overnight backtest run %s with status %s: %w", operation, id, status, repository.ErrOvernightBacktestRunClosed)
}

func (r *OvernightBacktestRunRepo) CommitIfRunning(ctx context.Context, runID uuid.UUID, completedAt time.Time, summary domain.OvernightBacktestSummary, prepared []domain.Strategy) (domain.OvernightBacktestSummary, time.Time, error) {
	if len(prepared) > 3 {
		return summary, time.Time{}, fmt.Errorf("postgres: commit overnight backtest run: %d strategies exceeds maximum 3", len(prepared))
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return summary, time.Time{}, fmt.Errorf("postgres: begin overnight backtest commit: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status, phase string
	err = tx.QueryRow(ctx, `SELECT status, phase FROM overnight_backtest_runs WHERE id = $1 FOR UPDATE`, runID).Scan(&status, &phase)
	if errors.Is(err, pgx.ErrNoRows) {
		return summary, time.Time{}, fmt.Errorf("postgres: commit overnight backtest run %s: %w", runID, repository.ErrNotFound)
	}
	if err != nil {
		return summary, time.Time{}, fmt.Errorf("postgres: lock overnight backtest run: %w", err)
	}
	if status != domain.OvernightBacktestStatusRunning {
		return summary, time.Time{}, fmt.Errorf("postgres: commit overnight backtest run %s with status %s: %w", runID, status, repository.ErrOvernightBacktestRunClosed)
	}
	if phase != domain.OvernightBacktestPhaseSweepValidateDeploy {
		return summary, time.Time{}, fmt.Errorf("postgres: commit overnight backtest run %s: phase %s is not %s", runID, phase, domain.OvernightBacktestPhaseSweepValidateDeploy)
	}

	created := 0
	for i := range prepared {
		wasCreated, insertErr := createOrReusePreparedStrategy(ctx, tx, &prepared[i])
		if insertErr != nil {
			return summary, time.Time{}, fmt.Errorf("postgres: deploy overnight strategy %s: %w", prepared[i].Name, insertErr)
		}
		if wasCreated {
			created++
		}
	}
	summary.Created = created
	summary.Reused = len(prepared) - created
	summary.Deployed = len(prepared)
	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		return summary, time.Time{}, fmt.Errorf("postgres: marshal overnight commit summary: %w", err)
	}
	completedAt = completedAt.UTC()
	var persistedAt time.Time
	err = tx.QueryRow(ctx, `UPDATE overnight_backtest_runs SET
		status = 'completed', phase = 'done', summary = $2, completed_at = $3, updated_at = $3
		WHERE id = $1 AND status = 'running'
		RETURNING updated_at`, runID, summaryJSON, completedAt).Scan(&persistedAt)
	if err != nil {
		return summary, time.Time{}, fmt.Errorf("postgres: complete overnight backtest run: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return summary, time.Time{}, fmt.Errorf("postgres: commit overnight backtest transaction: %w", err)
	}
	return summary, persistedAt, nil
}

func createOrReusePreparedStrategy(ctx context.Context, tx pgx.Tx, strategy *domain.Strategy) (bool, error) {
	if existing, err := findPreparedStrategy(ctx, tx, *strategy); err != nil {
		return false, err
	} else if existing != nil {
		*strategy = *existing
		return false, nil
	}
	config, err := marshalConfig(strategy.Config)
	if err != nil {
		return false, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO strategies
		(name, description, ticker, market_type, schedule_cron, config, status, skip_next_run, is_paper, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT DO NOTHING
		RETURNING id, created_at, updated_at`, strategy.Name, strategy.Description, strategy.Ticker, strategy.MarketType,
		strategy.ScheduleCron, config, strategy.Status, strategy.SkipNextRun, strategy.IsPaper,
		strategy.Status == domain.StrategyStatusActive).Scan(&strategy.ID, &strategy.CreatedAt, &strategy.UpdatedAt)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	existing, err := findPreparedStrategy(ctx, tx, *strategy)
	if err != nil {
		return false, err
	}
	if existing == nil {
		return false, fmt.Errorf("strategy insert conflicted without a matching paper strategy")
	}
	*strategy = *existing
	return false, nil
}

func findPreparedStrategy(ctx context.Context, tx pgx.Tx, strategy domain.Strategy) (*domain.Strategy, error) {
	query := `SELECT id, name, description, ticker, market_type, schedule_cron, config, status, skip_next_run, is_paper, created_at, updated_at
		FROM strategies WHERE ticker = $1 AND market_type = $2 AND is_paper = true`
	args := []any{strategy.Ticker, strategy.MarketType}
	if !eventmarkets.ReuseByTickerOnly(strategy.MarketType) {
		query += ` AND name = $3`
		args = append(args, strategy.Name)
	}
	query += ` ORDER BY created_at, id LIMIT 1`
	existing, err := scanStrategy(tx.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return existing, err
}

// ReconcileActive atomically fails every active checkpoint without rewriting
// terminal history. Repeated calls are no-ops.
func (r *OvernightBacktestRunRepo) ReconcileActive(ctx context.Context, completedAt time.Time, reason string) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: begin overnight backtest reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `UPDATE overnight_backtest_runs SET
		status='failed', phase='done', errors=errors || jsonb_build_array($2::text), completed_at=$1, updated_at=$1
		WHERE status='running'`, completedAt.UTC(), reason)
	if err != nil {
		return 0, fmt.Errorf("postgres: reconcile active overnight backtest runs: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("postgres: commit overnight backtest reconciliation: %w", err)
	}
	return int(command.RowsAffected()), nil
}

func (r *OvernightBacktestRunRepo) ListLatest(ctx context.Context, limit int) ([]domain.OvernightBacktestRun, error) {
	query, args := buildOvernightBacktestListLatestQuery(limit)
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: list overnight backtest runs: %w", err)
	}
	defer rows.Close()
	var runs []domain.OvernightBacktestRun
	for rows.Next() {
		run, err := scanOvernightBacktestRun(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scan overnight backtest run: %w", err)
		}
		runs = append(runs, *run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list overnight backtest runs rows: %w", err)
	}
	return runs, nil
}

const overnightBacktestSelectSQL = `SELECT id, status, phase, candidate_index, candidates, generated, errors, summary, started_at, updated_at, completed_at FROM overnight_backtest_runs`

func buildOvernightBacktestListLatestQuery(limit int) (string, []any) {
	if limit <= 0 {
		limit = 20
	}
	return overnightBacktestSelectSQL + ` ORDER BY started_at DESC, id DESC LIMIT $1`, []any{limit}
}

func marshalOvernightBacktestRunJSON(run domain.OvernightBacktestRun) ([]byte, []byte, []byte, []byte, error) {
	candidates, err := json.Marshal(run.Candidates)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("postgres: marshal overnight candidates: %w", err)
	}
	generated, err := json.Marshal(run.Generated)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("postgres: marshal overnight generated: %w", err)
	}
	errs, err := json.Marshal(run.Errors)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("postgres: marshal overnight errors: %w", err)
	}
	summary, err := json.Marshal(run.Summary)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("postgres: marshal overnight summary: %w", err)
	}
	return candidates, generated, errs, summary, nil
}

func scanOvernightBacktestRun(sc scanner) (*domain.OvernightBacktestRun, error) {
	var run domain.OvernightBacktestRun
	var candidates, generated, errs, summary []byte
	var completedAt *time.Time
	if err := sc.Scan(&run.ID, &run.Status, &run.Phase, &run.CandidateIndex, &candidates, &generated, &errs, &summary, &run.StartedAt, &run.UpdatedAt, &completedAt); err != nil {
		return nil, err
	}
	run.CompletedAt = completedAt
	if err := json.Unmarshal(candidates, &run.Candidates); err != nil {
		return nil, fmt.Errorf("postgres: unmarshal overnight candidates: %w", err)
	}
	if err := json.Unmarshal(generated, &run.Generated); err != nil {
		return nil, fmt.Errorf("postgres: unmarshal overnight generated: %w", err)
	}
	if err := json.Unmarshal(errs, &run.Errors); err != nil {
		return nil, fmt.Errorf("postgres: unmarshal overnight errors: %w", err)
	}
	if err := json.Unmarshal(summary, &run.Summary); err != nil {
		return nil, fmt.Errorf("postgres: unmarshal overnight summary: %w", err)
	}
	return &run, nil
}
