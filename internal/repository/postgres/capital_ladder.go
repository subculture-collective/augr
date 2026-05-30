package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type CapitalLadderRepo struct{ pool *pgxpool.Pool }

var _ repository.CapitalLadderRepository = (*CapitalLadderRepo)(nil)

func NewCapitalLadderRepo(pool *pgxpool.Pool) *CapitalLadderRepo {
	return &CapitalLadderRepo{pool: pool}
}

func (r *CapitalLadderRepo) Upsert(ctx context.Context, entry domain.CapitalLadderEntry) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO capital_ladder (strategy_id, step_pct, fill_rate, win_rate, drawdown_pct, baseline_fill_rate, baseline_win_rate, advanced_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NOW())
ON CONFLICT (strategy_id) DO UPDATE SET step_pct = EXCLUDED.step_pct, fill_rate = EXCLUDED.fill_rate, win_rate = EXCLUDED.win_rate, drawdown_pct = EXCLUDED.drawdown_pct, baseline_fill_rate = EXCLUDED.baseline_fill_rate, baseline_win_rate = EXCLUDED.baseline_win_rate, advanced_at = EXCLUDED.advanced_at, updated_at = NOW()`,
		entry.StrategyID, entry.StepPct, entry.FillRate, entry.WinRate, entry.DrawdownPct, entry.BaselineFillRate, entry.BaselineWinRate, entry.AdvancedAt)
	if err != nil {
		return fmt.Errorf("postgres: upsert capital ladder %s: %w", entry.StrategyID, err)
	}
	return nil
}

func (r *CapitalLadderRepo) Get(ctx context.Context, strategyID string) (*domain.CapitalLadderEntry, error) {
	var e domain.CapitalLadderEntry
	err := r.pool.QueryRow(ctx, `SELECT strategy_id, step_pct, fill_rate, win_rate, drawdown_pct, baseline_fill_rate, baseline_win_rate, advanced_at, updated_at FROM capital_ladder WHERE strategy_id=$1`, strategyID).
		Scan(&e.StrategyID, &e.StepPct, &e.FillRate, &e.WinRate, &e.DrawdownPct, &e.BaselineFillRate, &e.BaselineWinRate, &e.AdvancedAt, &e.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: get capital ladder %s: %w", strategyID, err)
	}
	return &e, nil
}

func (r *CapitalLadderRepo) List(ctx context.Context) ([]domain.CapitalLadderEntry, error) {
	rows, err := r.pool.Query(ctx, `SELECT strategy_id, step_pct, fill_rate, win_rate, drawdown_pct, baseline_fill_rate, baseline_win_rate, advanced_at, updated_at FROM capital_ladder ORDER BY updated_at DESC, strategy_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("postgres: list capital ladder: %w", err)
	}
	defer rows.Close()
	out := make([]domain.CapitalLadderEntry, 0)
	for rows.Next() {
		var e domain.CapitalLadderEntry
		if err := rows.Scan(&e.StrategyID, &e.StepPct, &e.FillRate, &e.WinRate, &e.DrawdownPct, &e.BaselineFillRate, &e.BaselineWinRate, &e.AdvancedAt, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("postgres: list capital ladder scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list capital ladder rows: %w", err)
	}
	return out, nil
}

func (r *CapitalLadderRepo) UpdateMetrics(ctx context.Context, strategyID string, fillRate, winRate, drawdownPct float64) error {
	ct, err := r.pool.Exec(ctx, `UPDATE capital_ladder SET fill_rate=$2, win_rate=$3, drawdown_pct=$4, updated_at=NOW() WHERE strategy_id=$1`, strategyID, fillRate, winRate, drawdownPct)
	if err != nil {
		return fmt.Errorf("postgres: update capital ladder metrics %s: %w", strategyID, err)
	}
	if ct.RowsAffected() == 0 {
		return repository.ErrNotFound
	}
	return nil
}

func (r *CapitalLadderRepo) AdvanceStep(ctx context.Context, strategyID string, newStep float64, advancedAt time.Time) error {
	ct, err := r.pool.Exec(ctx, `UPDATE capital_ladder SET step_pct=$2, baseline_fill_rate=fill_rate, baseline_win_rate=win_rate, advanced_at=$3, updated_at=NOW() WHERE strategy_id=$1`, strategyID, newStep, advancedAt.UTC())
	if err != nil {
		return fmt.Errorf("postgres: advance capital ladder %s: %w", strategyID, err)
	}
	if ct.RowsAffected() == 0 {
		return repository.ErrNotFound
	}
	return nil
}
