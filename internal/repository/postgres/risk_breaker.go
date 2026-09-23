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

// Trip opens the breaker for scope. The table keeps one row per scope
// (migration 000040 makes scope the primary key), so a re-trip updates the
// existing row and clears reset_at; the previous reset timestamp is not
// retained beyond the audit log.
func (r *RiskBreakerRepo) Trip(ctx context.Context, scope, reason string, trippedAt time.Time) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO risk_breaker_state (scope, tripped_at, reason, reset_at)
	VALUES ($1, $2, $3, NULL)
	ON CONFLICT (scope) DO UPDATE SET tripped_at = EXCLUDED.tripped_at, reason = EXCLUDED.reason, reset_at = NULL`, scope, trippedAt.UTC(), reason)
	if err != nil {
		return fmt.Errorf("postgres: trip risk breaker %s: %w", scope, err)
	}
	return nil
}

// Reset closes the open breaker for scope by recording reset_at. The row is
// kept so operators can see when and why the breaker last tripped. Resetting a
// scope without an open breaker is a no-op.
func (r *RiskBreakerRepo) Reset(ctx context.Context, scope string, resetAt time.Time) error {
	if resetAt.IsZero() {
		resetAt = time.Now()
	}
	_, err := r.pool.Exec(ctx, `UPDATE risk_breaker_state SET reset_at = $2 WHERE scope = $1 AND reset_at IS NULL`, scope, resetAt.UTC())
	if err != nil {
		return fmt.Errorf("postgres: reset risk breaker %s: %w", scope, err)
	}
	return nil
}

// HasOpenBreaker reports whether any of the scopes has a breaker that has
// been tripped and not reset. Callers such as the portfolio allocator pass the
// global scope plus their own scope in one call.
func (r *RiskBreakerRepo) HasOpenBreaker(ctx context.Context, scopes ...string) (bool, error) {
	if len(scopes) == 0 {
		return false, nil
	}
	var open bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM risk_breaker_state WHERE scope = ANY($1) AND reset_at IS NULL)`, scopes).Scan(&open)
	if err != nil {
		return false, fmt.Errorf("postgres: check open risk breakers: %w", err)
	}
	return open, nil
}

// Get returns the open breaker for scope. A reset breaker is reported as
// repository.ErrNotFound so callers treat it as clear.
func (r *RiskBreakerRepo) Get(ctx context.Context, scope string) (*domain.RiskBreakerState, error) {
	var st domain.RiskBreakerState
	var resetAt *time.Time
	err := r.pool.QueryRow(ctx, `SELECT scope, tripped_at, reason, reset_at FROM risk_breaker_state WHERE scope = $1 AND reset_at IS NULL`, scope).Scan(&st.Scope, &st.TrippedAt, &st.Reason, &resetAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: get risk breaker %s: %w", scope, err)
	}
	st.ResetAt = resetAt
	return &st, nil
}

// ListTripped returns breakers that are currently open.
func (r *RiskBreakerRepo) ListTripped(ctx context.Context) ([]domain.RiskBreakerState, error) {
	rows, err := r.pool.Query(ctx, `SELECT scope, tripped_at, reason, reset_at FROM risk_breaker_state WHERE reset_at IS NULL ORDER BY tripped_at DESC`)
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
