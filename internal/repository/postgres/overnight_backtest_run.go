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
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type OvernightBacktestRunRepo struct{ pool *pgxpool.Pool }

var _ repository.OvernightBacktestRunRepository = (*OvernightBacktestRunRepo)(nil)

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

func (r *OvernightBacktestRunRepo) Update(ctx context.Context, run *domain.OvernightBacktestRun) error {
	candidates, generated, errs, summary, err := marshalOvernightBacktestRunJSON(*run)
	if err != nil {
		return err
	}
	row := r.pool.QueryRow(ctx, `UPDATE overnight_backtest_runs SET
		status = $2, phase = $3, candidate_index = $4, candidates = $5, generated = $6, errors = $7, summary = $8, completed_at = $9, updated_at = NOW()
		WHERE id = $1 RETURNING updated_at`,
		run.ID, run.Status, run.Phase, run.CandidateIndex, candidates, generated, errs, summary, run.CompletedAt,
	)
	if err := row.Scan(&run.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("postgres: update overnight backtest run %s: %w", run.ID, repository.ErrNotFound)
		}
		return fmt.Errorf("postgres: update overnight backtest run: %w", err)
	}
	return nil
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
