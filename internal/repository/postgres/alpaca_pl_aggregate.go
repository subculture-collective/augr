package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// AlpacaPLAggregateRepo provides read-only Alpaca-only local P/L aggregates.
type AlpacaPLAggregateRepo struct {
	pool *pgxpool.Pool
}

var _ repository.AlpacaPLAggregateRepository = (*AlpacaPLAggregateRepo)(nil)

func NewAlpacaPLAggregateRepo(pool *pgxpool.Pool) *AlpacaPLAggregateRepo {
	return &AlpacaPLAggregateRepo{pool: pool}
}

const alpacaClosedRealizedPnLSQL = `SELECT COALESCE(SUM(COALESCE(p.realized_pnl, 0)), 0)
		FROM positions p
		WHERE p.account_id=$1 AND p.environment=$2 AND p.closed_at IS NOT NULL AND (
			EXISTS (SELECT 1 FROM position_provenance pp WHERE pp.position_id = p.id AND pp.broker = 'alpaca') OR
			EXISTS (
				SELECT 1
				FROM trades t
				JOIN orders o ON o.id = t.order_id AND o.account_id=p.account_id AND o.environment=p.environment
				WHERE t.position_id = p.id AND t.account_id=p.account_id AND t.environment=p.environment AND o.broker = 'alpaca'
			)
		)`

func (r *AlpacaPLAggregateRepo) ClosedRealizedPnL(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment) (float64, error) {
	var total float64
	if err := r.pool.QueryRow(ctx, alpacaClosedRealizedPnLSQL, accountID, environment).Scan(&total); err != nil {
		return 0, fmt.Errorf("postgres: alpaca closed realized pnl: %w", err)
	}
	return total, nil
}

const alpacaOpenUnrealizedPnLSQL = `SELECT COALESCE(SUM(COALESCE(p.unrealized_pnl, 0)), 0)
		FROM positions p
		WHERE p.account_id=$1 AND p.environment=$2 AND p.closed_at IS NULL AND (
			EXISTS (SELECT 1 FROM position_provenance pp WHERE pp.position_id = p.id AND pp.broker = 'alpaca') OR
			EXISTS (
				SELECT 1
				FROM trades t
				JOIN orders o ON o.id = t.order_id AND o.account_id=p.account_id AND o.environment=p.environment
				WHERE t.position_id = p.id AND t.account_id=p.account_id AND t.environment=p.environment AND o.broker = 'alpaca'
			)
		)`

func (r *AlpacaPLAggregateRepo) OpenUnrealizedPnL(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment) (float64, error) {
	var total float64
	if err := r.pool.QueryRow(ctx, alpacaOpenUnrealizedPnLSQL, accountID, environment).Scan(&total); err != nil {
		return 0, fmt.Errorf("postgres: alpaca open unrealized pnl: %w", err)
	}
	return total, nil
}

func (r *AlpacaPLAggregateRepo) TradeCount(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment) (int, error) {
	var total int
	if err := r.pool.QueryRow(ctx, `SELECT COUNT(*)
		FROM trades t
		JOIN orders o ON o.id = t.order_id
		WHERE t.account_id=$1 AND t.environment=$2 AND o.account_id=$1 AND o.environment=$2 AND o.broker = 'alpaca'`, accountID, environment).Scan(&total); err != nil {
		return 0, fmt.Errorf("postgres: alpaca trade count: %w", err)
	}
	return total, nil
}

func (r *AlpacaPLAggregateRepo) FeeTotal(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment) (float64, error) {
	var total float64
	if err := r.pool.QueryRow(ctx, `SELECT COALESCE(SUM(COALESCE(t.fee, 0)), 0)
		FROM trades t
		JOIN orders o ON o.id = t.order_id
		WHERE t.account_id=$1 AND t.environment=$2 AND o.account_id=$1 AND o.environment=$2 AND o.broker = 'alpaca'`, accountID, environment).Scan(&total); err != nil {
		return 0, fmt.Errorf("postgres: alpaca fee total: %w", err)
	}
	return total, nil
}
