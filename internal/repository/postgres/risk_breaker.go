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

type RiskBreakerRepo struct{ pool *pgxpool.Pool }

var _ repository.RiskBreakerRepository = (*RiskBreakerRepo)(nil)

func NewRiskBreakerRepo(pool *pgxpool.Pool) *RiskBreakerRepo { return &RiskBreakerRepo{pool: pool} }

func (r *RiskBreakerRepo) Trip(ctx context.Context, scope, reason string, trippedAt time.Time) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO risk_breaker_state (scope, tripped_at, reason, reset_at)
	VALUES ($1, $2, $3, NULL)
	ON CONFLICT (scope) DO UPDATE SET tripped_at = EXCLUDED.tripped_at, reason = EXCLUDED.reason, reset_at = NULL`, scope, trippedAt.UTC(), reason)
	if err != nil {
		return fmt.Errorf("postgres: trip risk breaker %s: %w", scope, err)
	}
	return nil
}

func (r *RiskBreakerRepo) Reset(ctx context.Context, scope string, resetAt time.Time) error {
	_ = resetAt
	ct, err := r.pool.Exec(ctx, `DELETE FROM risk_breaker_state WHERE scope = $1`, scope)
	if err != nil {
		return fmt.Errorf("postgres: reset risk breaker %s: %w", scope, err)
	}
	if ct.RowsAffected() == 0 {
		return nil
	}
	return nil
}

func (r *RiskBreakerRepo) Get(ctx context.Context, scope string) (*domain.RiskBreakerState, error) {
	var st domain.RiskBreakerState
	var resetAt *time.Time
	err := r.pool.QueryRow(ctx, `SELECT scope, tripped_at, reason, reset_at FROM risk_breaker_state WHERE scope = $1`, scope).Scan(&st.Scope, &st.TrippedAt, &st.Reason, &resetAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: get risk breaker %s: %w", scope, err)
	}
	st.ResetAt = resetAt
	return &st, nil
}

func (r *RiskBreakerRepo) ListTripped(ctx context.Context) ([]domain.RiskBreakerState, error) {
	rows, err := r.pool.Query(ctx, `SELECT scope, tripped_at, reason, reset_at FROM risk_breaker_state ORDER BY tripped_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("postgres: list risk breakers: %w", err)
	}
	defer rows.Close()
	out := make([]domain.RiskBreakerState, 0)
	for rows.Next() {
		var st domain.RiskBreakerState
		if err := rows.Scan(&st.Scope, &st.TrippedAt, &st.Reason, &st.ResetAt); err != nil {
			return nil, fmt.Errorf("postgres: list risk breakers scan: %w", err)
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list risk breakers rows: %w", err)
	}
	return out, nil
}
