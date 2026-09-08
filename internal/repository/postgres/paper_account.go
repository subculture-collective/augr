package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// PaperAccountRepo provides provenance-safe reads for paper-account restoration.
type PaperAccountRepo struct{ pool *DB }

var (
	_ repository.PaperAccountRepository = (*PaperAccountRepo)(nil)
	_ repository.ExecutionAccountLocker = (*PaperAccountRepo)(nil)
)

func NewPaperAccountRepo(db *DB) *PaperAccountRepo { return &PaperAccountRepo{pool: db} }

func (r *PaperAccountRepo) WithExecutionAccountLock(ctx context.Context, accountID uuid.UUID, fn func() error) error {
	if r == nil || r.pool == nil || r.pool.Pool == nil {
		return fmt.Errorf("postgres: paper account repository is unavailable")
	}
	return (&OrderRepo{pool: r.pool.Pool, accountID: accountID}).WithExecutionAccountLock(ctx, accountID, fn)
}

func (r *PaperAccountRepo) ListPaperTrades(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment, limit, offset int) ([]domain.Trade, error) {
	rows, err := r.pool.Pool.Query(ctx, `SELECT t.id, t.account_id, t.environment, t.origin_type, t.origin_id, t.external_id, t.order_id, t.position_id, t.ticker, t.side,
			t.quantity::double precision, t.price::double precision, t.fee::double precision,
			t.executed_at, t.created_at, t.asset_class, t.open_close,
			COALESCE(t.contract_multiplier, 100)::double precision, COALESCE(t.premium, 0)::double precision,
			COALESCE(t.exit_reason, '')
		FROM trades t
		LEFT JOIN orders o ON o.id = t.order_id AND o.account_id=t.account_id AND o.environment=t.environment
		WHERE t.account_id=$1 AND t.environment=$2
		  AND (o.broker = 'paper' OR (t.order_id IS NULL AND t.origin_type='settlement'))
		ORDER BY t.executed_at DESC, t.created_at DESC, t.id DESC LIMIT $3 OFFSET $4`, accountID, environment, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("postgres: list paper trades: %w", err)
	}
	defer rows.Close()
	var out []domain.Trade
	for rows.Next() {
		tr, err := scanTrade(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: list paper trades scan: %w", err)
		}
		out = append(out, *tr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list paper trades rows: %w", err)
	}
	return out, nil
}

func (r *PaperAccountRepo) GetOpenPaperPositions(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment, limit, offset int) ([]domain.Position, error) {
	rows, err := r.pool.Pool.Query(ctx, `SELECT p.id, p.strategy_id, p.account_id, p.environment, p.origin_type, p.origin_id, s.market_type, p.ticker, p.side,
		p.quantity::double precision, p.avg_entry::double precision,
		p.current_price::double precision, p.unrealized_pnl::double precision,
		p.realized_pnl::double precision, p.stop_loss::double precision,
		p.take_profit::double precision, p.opened_at, p.closed_at,
		p.asset_class, p.underlying_ticker, p.option_type, p.strike::double precision,
		p.expiry, p.contract_multiplier::double precision, p.leg_group_id,
		p.delta::double precision, p.gamma::double precision, p.theta::double precision,
		p.vega::double precision
		FROM positions p
		LEFT JOIN strategies s ON s.id = p.strategy_id
		WHERE p.closed_at IS NULL AND p.account_id=$1 AND p.environment=$2
		AND ((p.strategy_id IS NOT NULL AND s.is_paper=true) OR p.origin_type='copy_subscription')
		AND (p.origin_type='copy_subscription' OR EXISTS (
			SELECT 1
			FROM trades t
			INNER JOIN orders o ON o.id = t.order_id AND o.account_id=p.account_id AND o.environment=p.environment
			WHERE t.position_id = p.id AND t.account_id=p.account_id AND t.environment=p.environment AND o.broker = 'paper'
		))
		ORDER BY p.opened_at ASC, p.id ASC LIMIT $3 OFFSET $4`, accountID, environment, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("postgres: get open paper positions: %w", err)
	}
	defer rows.Close()
	var out []domain.Position
	for rows.Next() {
		pos, err := scanPosition(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: get open paper positions scan: %w", err)
		}
		out = append(out, *pos)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: get open paper positions rows: %w", err)
	}
	return out, nil
}

const listOpenPaperOrdersSQL = `SELECT o.id, o.strategy_id, o.pipeline_run_id, o.account_id, o.environment, o.origin_type, o.origin_id,
		o.pipeline_run_trade_date, o.copy_origin_rebalance_run_id, o.copy_intent_id, o.copy_execution_claim_id, o.external_id, o.ticker,
		o.market_type, o.side, o.order_type, o.quantity::double precision, o.limit_price::double precision,
		o.stop_price::double precision, o.filled_quantity::double precision, o.filled_avg_price::double precision,
		o.status, o.broker, o.submitted_at, o.filled_at, o.created_at, o.asset_class, o.underlying_ticker,
		o.option_type, o.strike::double precision, o.expiry, o.contract_multiplier::double precision,
		o.position_intent, o.leg_group_id, COALESCE(o.prediction_side, ''), COALESCE(o.polymarket_intent, ''), o.allocation_opportunity_id, COALESCE(o.client_order_id, ''),
		COALESCE(o.spread_max_risk,0)::double precision, COALESCE(o.spread_max_reward,0)::double precision
		FROM orders o
		WHERE o.account_id=$1 AND o.environment=$2 AND o.broker = 'paper' AND (
			o.status IN ('pending', 'submitted', 'partial') OR
			(o.status='filled' AND o.market_type<>'options' AND (
				o.filled_quantity>COALESCE((SELECT SUM(t.quantity) FROM trades t WHERE t.order_id=o.id AND t.account_id=o.account_id AND t.environment=o.environment),0) OR
				NOT EXISTS (SELECT 1 FROM trade_decisions d WHERE d.paper_order_id=o.id AND d.account_id=o.account_id AND d.environment=o.environment) OR
				EXISTS (SELECT 1 FROM trade_decisions d WHERE d.paper_order_id=o.id AND d.account_id=o.account_id AND d.environment=o.environment AND (
					NOT EXISTS (SELECT 1 FROM replay_events re WHERE re.trade_decision_id=d.id AND re.account_id=o.account_id AND re.environment=o.environment AND re.event_type='fill_observed' AND re.payload->>'order_id'=o.id::text) OR
					(EXISTS (SELECT 1 FROM financial_fill_idempotency f WHERE f.order_id=o.id AND f.account_id=o.account_id AND f.environment=o.environment AND f.position_id IS NOT NULL) AND NOT EXISTS (SELECT 1 FROM replay_events re WHERE re.trade_decision_id=d.id AND re.account_id=o.account_id AND re.environment=o.environment AND re.event_type='position_updated'))
				))
			)) OR
			(o.market_type<>'options' AND o.status IN ('cancelled','rejected') AND o.filled_quantity>0 AND (
				o.filled_quantity>COALESCE((SELECT SUM(t.quantity) FROM trades t WHERE t.order_id=o.id AND t.account_id=o.account_id AND t.environment=o.environment),0) OR
				NOT EXISTS (SELECT 1 FROM trade_decisions d WHERE d.paper_order_id=o.id AND d.account_id=o.account_id AND d.environment=o.environment) OR
				EXISTS (SELECT 1 FROM trade_decisions d WHERE d.paper_order_id=o.id AND d.account_id=o.account_id AND d.environment=o.environment AND (
					NOT EXISTS (SELECT 1 FROM replay_events re WHERE re.trade_decision_id=d.id AND re.account_id=o.account_id AND re.environment=o.environment AND re.event_type='fill_observed' AND re.payload->>'order_id'=o.id::text) OR
					(EXISTS (SELECT 1 FROM financial_fill_idempotency f WHERE f.order_id=o.id AND f.account_id=o.account_id AND f.environment=o.environment AND f.position_id IS NOT NULL) AND NOT EXISTS (SELECT 1 FROM replay_events re WHERE re.trade_decision_id=d.id AND re.account_id=o.account_id AND re.environment=o.environment AND re.event_type='position_updated'))
				))
			)) OR
			(o.market_type='options' AND o.status IN ('cancelled','rejected') AND o.filled_quantity>COALESCE((SELECT SUM(t.quantity) FROM trades t WHERE t.order_id=o.id AND t.account_id=o.account_id AND t.environment=o.environment),0)) OR
			(o.market_type='options' AND o.leg_group_id IS NOT NULL AND EXISTS (
				SELECT 1 FROM orders sibling WHERE sibling.account_id=o.account_id AND sibling.environment=o.environment
				AND sibling.broker='paper' AND sibling.market_type='options' AND sibling.leg_group_id=o.leg_group_id AND (sibling.status IN ('pending','submitted','partial') OR
				(sibling.status IN ('cancelled','rejected') AND sibling.filled_quantity>COALESCE((SELECT SUM(t.quantity) FROM trades t WHERE t.order_id=sibling.id AND t.account_id=sibling.account_id AND t.environment=sibling.environment),0)))
			))
		)
		ORDER BY o.submitted_at ASC, o.id ASC LIMIT $3 OFFSET $4`

func (r *PaperAccountRepo) ListOpenPaperOrders(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment, limit, offset int) ([]domain.Order, error) {
	rows, err := r.pool.Pool.Query(ctx, listOpenPaperOrdersSQL, accountID, environment, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("postgres: list open paper orders: %w", err)
	}
	defer rows.Close()
	var out []domain.Order
	for rows.Next() {
		ord, err := scanOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: list open paper orders scan: %w", err)
		}
		out = append(out, *ord)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list open paper orders rows: %w", err)
	}
	return out, nil
}

func (r *PaperAccountRepo) GetMaxPaperExternalIDSequence(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment) (uint64, error) {
	var maxSeq sql.NullInt64
	if err := r.pool.Pool.QueryRow(ctx, `SELECT COALESCE(MAX((regexp_match(external_id, '^paper-([0-9]+)$'))[1]::bigint), 0) FROM orders WHERE account_id=$1 AND environment=$2 AND broker = 'paper' AND external_id ~ '^paper-[0-9]+$'`, accountID, environment).Scan(&maxSeq); err != nil {
		return 0, fmt.Errorf("postgres: get max paper external id sequence: %w", err)
	}
	if !maxSeq.Valid || maxSeq.Int64 < 0 {
		return 0, nil
	}
	return uint64(maxSeq.Int64), nil
}
