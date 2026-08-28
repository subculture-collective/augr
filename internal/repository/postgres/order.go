package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// OrderRepo implements repository.OrderRepository using PostgreSQL.
type OrderRepo struct {
	pool      *pgxpool.Pool
	accountID uuid.UUID
}

// Compile-time check that OrderRepo satisfies OrderRepository.
var _ repository.OrderRepository = (*OrderRepo)(nil)
var _ repository.ExecutionAccountLocker = (*OrderRepo)(nil)
var _ repository.AtomicOptionCloseRepository = (*OrderRepo)(nil)
var _ repository.AtomicOptionOrderRepository = (*OrderRepo)(nil)
var _ repository.OptionCloseReservationLookup = (*OrderRepo)(nil)

// NewOrderRepo returns an OrderRepo backed by the given connection pool.
func NewOrderRepo(pool *pgxpool.Pool, accountID uuid.UUID) *OrderRepo {
	return &OrderRepo{pool: pool, accountID: accountID}
}

func (r *OrderRepo) WithExecutionAccountLock(ctx context.Context, accountID uuid.UUID, fn func() error) error {
	if accountID == uuid.Nil || accountID != r.accountID || fn == nil {
		return fmt.Errorf("postgres: execution account advisory lock: matching account and callback are required")
	}
	// The lock lives on an isolated connection. A waiter must never consume a
	// pooled connection while the lock owner needs that pool to run its callback.
	conn, err := pgx.ConnectConfig(ctx, r.pool.Config().ConnConfig.Copy())
	if err != nil {
		return fmt.Errorf("postgres: execution account advisory lock connect: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Close(cleanupCtx)
	}()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres: execution account advisory lock begin: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanupCtx)
	}()
	key := sha256.Sum256([]byte("execution-account|" + accountID.String()))
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(binary.BigEndian.Uint64(key[:8])&^uint64(1<<63))); err != nil {
		return fmt.Errorf("postgres: execution account advisory lock acquire: %w", err)
	}
	if err := fn(); err != nil {
		return err
	}
	return nil
}

// Create inserts a new order and populates the generated ID and CreatedAt on
// the provided struct.
func (r *OrderRepo) Create(ctx context.Context, order *domain.Order) error {
	return r.create(ctx, r.pool, order)
}

type orderRowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (r *OrderRepo) create(ctx context.Context, queryer orderRowQuerier, order *domain.Order) error {
	if (order.AllocationOpportunityID == nil) != (order.AllocationClaimID == nil) {
		return fmt.Errorf("postgres: create order: allocation opportunity and claim must be provided together")
	}
	if order.OriginType == "copy_subscription" && (order.CopyIntentID == nil || order.CopyExecutionClaimID == nil) {
		return fmt.Errorf("postgres: create order: copy intent and execution claim are required")
	}
	if err := validateOptionalPipelineRunRef(order.PipelineRunID, order.PipelineRunTradeDate); err != nil {
		return fmt.Errorf("postgres: create order: %w", err)
	}
	if order.AccountID != uuid.Nil && order.AccountID != r.accountID {
		return fmt.Errorf("postgres: create order: account mismatch")
	}
	order.AccountID = r.accountID
	marketType := order.MarketType.Normalize()
	if marketType == "" {
		marketType = domain.MarketTypeStock
	}
	order.MarketType = marketType

	row := queryer.QueryRow(ctx,
		`WITH authorized AS (
			SELECT 1 WHERE ($33::uuid IS NULL AND $34::uuid IS NULL) OR EXISTS (
				SELECT 1 FROM portfolio_opportunities
				WHERE id=$33 AND account_id=$3 AND status='selected'
				  AND allocation_claim_id=$34 AND allocation_claim_expires_at>clock_timestamp()
				  AND environment=$4 AND origin_type=$5 AND origin_id=$6
				  AND strategy_id=$1 AND pipeline_run_id=$2 AND pipeline_run_trade_date=$7
				  AND $4::text IS NOT NULL AND $5::text IS NOT NULL AND $6::text IS NOT NULL
				  AND $1::uuid IS NOT NULL AND $2::uuid IS NOT NULL AND $7::date IS NOT NULL
			)
		), locked_copy_subscription AS (
			SELECT id FROM copy_subscriptions
			WHERE $5='copy_subscription' AND id=$6::uuid AND account_id=$3 AND environment=$4 AND status='paper_active' AND is_paper=true
			FOR SHARE
		), copy_authorized AS (
			SELECT 1 WHERE $5<>'copy_subscription' OR EXISTS (
				SELECT 1 FROM locked_copy_subscription s
				JOIN copy_origin_rebalance_intents ri ON ri.run_id=$8 AND ri.account_id=$3 AND ri.environment=$4 AND ri.origin_type=$5 AND ri.origin_id=$6
				JOIN copy_trade_intents i ON i.id=ri.intent_id AND i.subscription_id=s.id AND i.account_id=$3 AND i.environment=$4
				WHERE i.id=$36 AND i.ticker=$10 AND i.side=$12 AND i.execution_claim_id=$37 AND i.status='received' AND i.order_id IS NULL
			)
		)
		INSERT INTO orders (
			strategy_id, pipeline_run_id, account_id, environment, origin_type, origin_id,
			pipeline_run_trade_date, copy_origin_rebalance_run_id, external_id, ticker, market_type, side, order_type,
			quantity, limit_price, stop_price, filled_quantity, filled_avg_price,
			status, broker, submitted_at, filled_at, asset_class, underlying_ticker,
			option_type, strike, expiry, contract_multiplier, position_intent, leg_group_id,
			prediction_side, polymarket_intent, allocation_opportunity_id, client_order_id
		)
		 SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32, $33, $35 FROM authorized,copy_authorized
		 RETURNING id, created_at`,
		order.StrategyID,
		order.PipelineRunID,
		r.accountID,
		nullString(string(order.Environment)),
		nullString(order.OriginType),
		nullString(order.OriginID),
		order.PipelineRunTradeDate,
		nullableUUID(order.CopyOriginRebalanceRunID),
		nullString(order.ExternalID),
		order.Ticker,
		marketType,
		order.Side,
		order.OrderType,
		order.Quantity,
		order.LimitPrice,
		order.StopPrice,
		order.FilledQuantity,
		order.FilledAvgPrice,
		order.Status,
		nullString(order.Broker),
		order.SubmittedAt,
		order.FilledAt,
		order.AssetClass,
		nullString(order.UnderlyingTicker),
		order.OptionType,
		order.Strike,
		order.Expiry,
		order.ContractMultiplier,
		order.PositionIntent,
		order.LegGroupID,
		nullString(order.PredictionSide),
		nullString(order.PolymarketIntent),
		order.AllocationOpportunityID,
		order.AllocationClaimID,
		nullString(order.ClientOrderID),
		order.CopyIntentID,
		order.CopyExecutionClaimID,
	)

	if err := row.Scan(&order.ID, &order.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) && order.AllocationOpportunityID != nil {
			return fmt.Errorf("postgres: create order: allocation lineage or claim ownership lost")
		}
		return fmt.Errorf("postgres: create order: %w", err)
	}

	return nil
}

func (r *OrderRepo) CreateOptionCloseOrdersAndReserve(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment, originType, originID string, positionIDs []uuid.UUID, orders []*domain.Order) error {
	if accountID == uuid.Nil || accountID != r.accountID || !environment.IsValid() || strings.TrimSpace(originType) == "" || strings.TrimSpace(originID) == "" || len(positionIDs) == 0 || len(positionIDs) != len(orders) {
		return fmt.Errorf("postgres: atomic option close: complete account-bound pairs are required")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres: atomic option close begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for i, order := range orders {
		if order == nil || order.AccountID != accountID || order.Environment != environment || order.OriginType != originType || order.OriginID != originID || order.MarketType.Normalize() != domain.MarketTypeOptions || order.Status != domain.OrderStatusPending || order.PositionIntent == nil || (*order.PositionIntent != domain.PositionIntentBuyToClose && *order.PositionIntent != domain.PositionIntentSellToClose) {
			return fmt.Errorf("postgres: atomic option close: invalid close order")
		}
		if err := r.create(ctx, tx, order); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE positions SET close_reservation_order_id=$1 WHERE id=$2 AND account_id=$3 AND environment=$4 AND origin_type=$5 AND origin_id=$6 AND asset_class='option' AND closed_at IS NULL AND quantity>0 AND close_reservation_order_id IS NULL`, order.ID, positionIDs[i], accountID, environment, originType, originID)
		if err != nil {
			return fmt.Errorf("postgres: atomic option close reserve: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("postgres: option close position %s is already reserved or ownership changed", positionIDs[i])
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: atomic option close commit: %w", err)
	}
	return nil
}

func (r *OrderRepo) CreateOptionOrders(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment, originType, originID string, orders []*domain.Order) error {
	if accountID == uuid.Nil || accountID != r.accountID || !environment.IsValid() || strings.TrimSpace(originType) == "" || strings.TrimSpace(originID) == "" || len(orders) == 0 {
		return fmt.Errorf("postgres: atomic option orders: complete account-bound batch is required")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres: atomic option orders begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, order := range orders {
		if order == nil || order.AccountID != accountID || order.Environment != environment || order.OriginType != originType || order.OriginID != originID || order.MarketType.Normalize() != domain.MarketTypeOptions || order.Status != domain.OrderStatusPending || order.PositionIntent == nil || (*order.PositionIntent != domain.PositionIntentBuyToOpen && *order.PositionIntent != domain.PositionIntentSellToOpen) {
			return fmt.Errorf("postgres: atomic option orders: invalid opening order")
		}
		if err := r.create(ctx, tx, order); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: atomic option orders commit: %w", err)
	}
	return nil
}

func (r *OrderRepo) GetOptionClosePositionByOrder(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment, orderID uuid.UUID) (*domain.Position, error) {
	if accountID == uuid.Nil || accountID != r.accountID || !environment.IsValid() || orderID == uuid.Nil {
		return nil, fmt.Errorf("postgres: option close reservation lookup: invalid identity")
	}
	position, err := scanPosition(r.pool.QueryRow(ctx, positionSelectSQL+` WHERE p.account_id=$1 AND p.environment=$2 AND p.close_reservation_order_id=$3`, accountID, environment, orderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("postgres: option close reservation lookup: %w", repository.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: option close reservation lookup: %w", err)
	}
	return position, nil
}

func (r *OrderRepo) ReleaseOptionClosePositions(ctx context.Context, accountID uuid.UUID, positionIDs, orderIDs []uuid.UUID) error {
	if accountID == uuid.Nil || accountID != r.accountID || len(positionIDs) != len(orderIDs) {
		return fmt.Errorf("postgres: release option close reservation: invalid pairs")
	}
	for i := range positionIDs {
		if _, err := r.pool.Exec(ctx, `UPDATE positions SET close_reservation_order_id=NULL WHERE id=$1 AND account_id=$2 AND close_reservation_order_id=$3`, positionIDs[i], accountID, orderIDs[i]); err != nil {
			return err
		}
	}
	return nil
}

func (r *OrderRepo) ReconcileOptionCloseReservations(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment) error {
	if accountID == uuid.Nil || accountID != r.accountID || !environment.IsValid() {
		return fmt.Errorf("postgres: reconcile option close reservations: invalid execution account")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE positions p SET close_reservation_order_id=NULL FROM orders o WHERE p.close_reservation_order_id=o.id AND p.account_id=$1 AND p.environment=$2 AND o.status IN ('filled','rejected','cancelled') AND o.filled_quantity<=COALESCE((SELECT SUM(t.quantity) FROM trades t WHERE t.order_id=o.id AND t.account_id=o.account_id),0)`, accountID, environment); err != nil {
		return fmt.Errorf("postgres: release interrupted option closes: %w", err)
	}
	return tx.Commit(ctx)
}

func (r *OrderRepo) CreatePredictionExitOrderAndReserve(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment, originType, originID string, positionID uuid.UUID, order *domain.Order) error {
	if accountID == uuid.Nil || accountID != r.accountID || !environment.IsValid() || strings.TrimSpace(originType) == "" || strings.TrimSpace(originID) == "" || positionID == uuid.Nil || order == nil || order.AccountID != accountID || order.Environment != environment || order.OriginType != originType || order.OriginID != originID || order.MarketType.Normalize() != domain.MarketTypePolymarket || order.Status != domain.OrderStatusPending || order.PositionIntent == nil {
		return fmt.Errorf("postgres: atomic prediction exit: complete scoped order is required")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres: atomic prediction exit begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var positionSide domain.PositionSide
	var positionQuantity float64
	if err := tx.QueryRow(ctx, `SELECT side,quantity::double precision FROM positions WHERE id=$1 AND account_id=$2 AND environment=$3 AND origin_type=$4 AND origin_id=$5 AND closed_at IS NULL AND quantity>0 AND close_reservation_order_id IS NULL FOR UPDATE`, positionID, accountID, environment, originType, originID).Scan(&positionSide, &positionQuantity); err != nil {
		return fmt.Errorf("postgres: lock prediction exit position: %w", err)
	}
	validLongExit := positionSide == domain.PositionSideLong && order.Side == domain.OrderSideSell && *order.PositionIntent == domain.PositionIntentSellToClose
	validShortExit := positionSide == domain.PositionSideShort && order.Side == domain.OrderSideBuy && *order.PositionIntent == domain.PositionIntentBuyToClose
	if !validLongExit && !validShortExit {
		return fmt.Errorf("postgres: prediction exit direction and intent do not close the locked position")
	}
	order.Quantity = positionQuantity
	if err := r.create(ctx, tx, order); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE positions SET close_reservation_order_id=$1 WHERE id=$2 AND account_id=$3 AND environment=$4 AND origin_type=$5 AND origin_id=$6 AND closed_at IS NULL AND quantity>0 AND close_reservation_order_id IS NULL`, order.ID, positionID, accountID, environment, originType, originID)
	if err != nil {
		return fmt.Errorf("postgres: atomic prediction exit reserve: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("postgres: prediction position %s is already reserved or ownership changed", positionID)
	}
	return tx.Commit(ctx)
}

func (r *OrderRepo) ReleasePredictionExitPosition(ctx context.Context, accountID, positionID, orderID uuid.UUID) error {
	if accountID == uuid.Nil || accountID != r.accountID || positionID == uuid.Nil || orderID == uuid.Nil {
		return fmt.Errorf("postgres: release prediction exit reservation: invalid identity")
	}
	_, err := r.pool.Exec(ctx, `UPDATE positions p SET close_reservation_order_id=NULL FROM orders o WHERE p.id=$1 AND p.account_id=$2 AND p.close_reservation_order_id=$3 AND o.id=$3 AND o.account_id=$2 AND o.status IN ('filled','rejected','cancelled') AND o.filled_quantity<=COALESCE((SELECT SUM(t.quantity) FROM trades t WHERE t.order_id=o.id AND t.account_id=o.account_id),0)`, positionID, accountID, orderID)
	return err
}

func (r *OrderRepo) GetPredictionExitOrderByPosition(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment, positionID uuid.UUID) (*domain.Order, error) {
	if accountID == uuid.Nil || accountID != r.accountID || !environment.IsValid() || positionID == uuid.Nil {
		return nil, fmt.Errorf("postgres: prediction exit reservation lookup: invalid identity")
	}
	order, err := scanOrder(r.pool.QueryRow(ctx, orderSelectSQL+` WHERE id=(SELECT close_reservation_order_id FROM positions WHERE id=$1 AND account_id=$2 AND environment=$3) AND account_id=$2 AND environment=$3`, positionID, accountID, environment))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: prediction exit reservation lookup: %w", err)
	}
	return order, nil
}

func (r *OrderRepo) MarkPredictionExitSubmitted(ctx context.Context, accountID, orderID uuid.UUID, externalID string, submittedAt time.Time) error {
	if accountID == uuid.Nil || accountID != r.accountID || orderID == uuid.Nil || strings.TrimSpace(externalID) == "" || submittedAt.IsZero() {
		return fmt.Errorf("postgres: mark prediction exit submitted: complete broker evidence is required")
	}
	tag, err := r.pool.Exec(ctx, `UPDATE orders SET external_id=$1,status='submitted',submitted_at=COALESCE(submitted_at,$2) WHERE id=$3 AND account_id=$4 AND status IN ('pending','submitted') AND (external_id IS NULL OR external_id='' OR external_id=$1)`, strings.TrimSpace(externalID), submittedAt.UTC(), orderID, accountID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("postgres: mark prediction exit submitted: pending order not found")
	}
	return nil
}

// FinalizePredictionExit atomically records a definitive unsuccessful venue
// outcome and releases the matching position reservation.
func (r *OrderRepo) FinalizePredictionExit(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment, positionID, orderID uuid.UUID, status domain.OrderStatus, externalID string, observedAt time.Time) error {
	if accountID == uuid.Nil || accountID != r.accountID || !environment.IsValid() || positionID == uuid.Nil || orderID == uuid.Nil || (status != domain.OrderStatusCancelled && status != domain.OrderStatusRejected) || observedAt.IsZero() {
		return fmt.Errorf("postgres: finalize prediction exit: invalid terminal outcome")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE orders o SET external_id=COALESCE(NULLIF($1,''),external_id),status=$2,submitted_at=COALESCE(submitted_at,$3) WHERE id=$4 AND account_id=$5 AND environment=$6 AND status IN ('pending','submitted','partial') AND o.filled_quantity<=COALESCE((SELECT SUM(t.quantity) FROM trades t WHERE t.order_id=o.id AND t.account_id=o.account_id),0)`, strings.TrimSpace(externalID), status, observedAt.UTC(), orderID, accountID, environment)
	if err != nil {
		return fmt.Errorf("postgres: finalize prediction exit order: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("postgres: finalize prediction exit: retryable order not found")
	}
	tag, err = tx.Exec(ctx, `UPDATE positions SET close_reservation_order_id=NULL WHERE id=$1 AND account_id=$2 AND environment=$3 AND close_reservation_order_id=$4`, positionID, accountID, environment, orderID)
	if err != nil {
		return fmt.Errorf("postgres: finalize prediction exit reservation: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("postgres: finalize prediction exit: reservation not found")
	}
	return tx.Commit(ctx)
}

func (r *OrderRepo) ReconcilePredictionExitReservations(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment) error {
	if accountID == uuid.Nil || accountID != r.accountID || !environment.IsValid() {
		return fmt.Errorf("postgres: reconcile prediction exit reservations: invalid execution account")
	}
	_, err := r.pool.Exec(ctx, `UPDATE positions p SET close_reservation_order_id=NULL FROM orders o WHERE p.close_reservation_order_id=o.id AND p.account_id=$1 AND p.environment=$2 AND o.account_id=$1 AND o.environment=$2 AND o.market_type IN ('stock','polymarket','kalshi') AND o.status IN ('filled','rejected','cancelled') AND o.filled_quantity<=COALESCE((SELECT SUM(t.quantity) FROM trades t WHERE t.order_id=o.id AND t.account_id=o.account_id),0)`, accountID, environment)
	if err != nil {
		return fmt.Errorf("postgres: reconcile prediction exit reservations: %w", err)
	}
	return nil
}

// Get retrieves an order by ID. It returns ErrNotFound when no row matches.
func (r *OrderRepo) Get(ctx context.Context, id uuid.UUID) (*domain.Order, error) {
	row := r.pool.QueryRow(ctx, orderSelectSQL+` WHERE id = $1 AND account_id = $2`, id, r.accountID)

	order, err := scanOrder(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("postgres: get order %s: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("postgres: get order: %w", err)
	}

	return order, nil
}

func (r *OrderRepo) GetByAllocationOpportunity(ctx context.Context, opportunity domain.Opportunity) (*domain.Order, error) {
	if opportunity.ID == uuid.Nil || opportunity.AccountID != r.accountID || opportunity.PipelineRunID == nil || opportunity.PipelineRunTradeDate == nil || opportunity.StrategyID == uuid.Nil {
		return nil, fmt.Errorf("postgres: get allocation order: complete account-bound lineage is required")
	}
	order, err := scanOrder(r.pool.QueryRow(ctx, orderSelectSQL+` WHERE allocation_opportunity_id=$1 AND account_id=$2 AND environment=$3 AND origin_type=$4 AND origin_id=$5 AND pipeline_run_id=$6 AND pipeline_run_trade_date=$7 AND strategy_id=$8`, opportunity.ID, r.accountID, opportunity.Environment, opportunity.OriginType, opportunity.OriginID, opportunity.PipelineRunID, opportunity.PipelineRunTradeDate, opportunity.StrategyID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("postgres: get allocation order: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: get allocation order: %w", err)
	}
	return order, nil
}

// List returns orders matching the provided filter with pagination.
func (r *OrderRepo) List(ctx context.Context, filter repository.OrderFilter, limit, offset int) ([]domain.Order, error) {
	query, args := buildOrderQuery("account", r.accountID, filter, limit, offset)
	return r.list(ctx, query, args, "list orders")
}

// Update persists changes to an existing order. It returns ErrNotFound when no
// row matches the order ID.
func (r *OrderRepo) Update(ctx context.Context, order *domain.Order) error {
	if err := validateOptionalPipelineRunRef(order.PipelineRunID, order.PipelineRunTradeDate); err != nil {
		return fmt.Errorf("postgres: update order: %w", err)
	}
	marketType := order.MarketType.Normalize()
	if marketType == "" {
		marketType = domain.MarketTypeStock
	}
	order.MarketType = marketType

	row := r.pool.QueryRow(ctx,
		`WITH locked AS (SELECT id FROM orders WHERE id=$34 AND account_id=$35 FOR UPDATE)
		 UPDATE orders o
		 SET external_id = $9, filled_quantity = $17, filled_avg_price = $18, status = $19,
		     broker = $20, submitted_at = $21, filled_at = $22, client_order_id=$33
		 FROM locked
		 WHERE o.id = locked.id
		   AND o.strategy_id IS NOT DISTINCT FROM $1 AND o.pipeline_run_id IS NOT DISTINCT FROM $2
		   AND o.account_id IS NOT DISTINCT FROM $3 AND o.environment IS NOT DISTINCT FROM $4
		   AND o.origin_type IS NOT DISTINCT FROM $5 AND o.origin_id IS NOT DISTINCT FROM $6
		   AND o.pipeline_run_trade_date IS NOT DISTINCT FROM $7 AND o.copy_origin_rebalance_run_id IS NOT DISTINCT FROM $8
		   AND o.ticker=$10 AND o.market_type=$11 AND o.side=$12 AND o.order_type=$13 AND o.quantity=$14
		   AND o.limit_price IS NOT DISTINCT FROM $15 AND o.stop_price IS NOT DISTINCT FROM $16
		   AND o.asset_class IS NOT DISTINCT FROM $23 AND o.underlying_ticker IS NOT DISTINCT FROM $24
		   AND o.option_type IS NOT DISTINCT FROM $25 AND o.strike IS NOT DISTINCT FROM $26 AND o.expiry IS NOT DISTINCT FROM $27
		   AND o.contract_multiplier IS NOT DISTINCT FROM $28 AND o.position_intent IS NOT DISTINCT FROM $29
		   AND o.leg_group_id IS NOT DISTINCT FROM $30 AND o.prediction_side IS NOT DISTINCT FROM $31 AND o.polymarket_intent IS NOT DISTINCT FROM $32
		 RETURNING o.id`,
		order.StrategyID,
		order.PipelineRunID,
		nullableUUID(order.AccountID),
		nullString(string(order.Environment)),
		nullString(order.OriginType),
		nullString(order.OriginID),
		order.PipelineRunTradeDate,
		nullableUUID(order.CopyOriginRebalanceRunID),
		nullString(order.ExternalID),
		order.Ticker,
		marketType,
		order.Side,
		order.OrderType,
		order.Quantity,
		order.LimitPrice,
		order.StopPrice,
		order.FilledQuantity,
		order.FilledAvgPrice,
		order.Status,
		nullString(order.Broker),
		order.SubmittedAt,
		order.FilledAt,
		order.AssetClass,
		nullString(order.UnderlyingTicker),
		order.OptionType,
		order.Strike,
		order.Expiry,
		order.ContractMultiplier,
		order.PositionIntent,
		order.LegGroupID,
		nullString(order.PredictionSide),
		nullString(order.PolymarketIntent),
		nullString(order.ClientOrderID),
		order.ID, r.accountID,
	)

	var updatedID uuid.UUID
	if err := row.Scan(&updatedID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("postgres: update order %s: %w", order.ID, ErrNotFound)
		}
		return fmt.Errorf("postgres: update order: %w", err)
	}

	return nil
}

// Delete removes an order by ID. It returns ErrNotFound when no row matches.
func (r *OrderRepo) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM orders WHERE id = $1 AND account_id = $2`, id, r.accountID)
	if err != nil {
		return fmt.Errorf("postgres: delete order: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: delete order %s: %w", id, ErrNotFound)
	}

	return nil
}

// GetByStrategy returns orders for the given strategy with optional filtering
// and pagination.
func (r *OrderRepo) GetByStrategy(ctx context.Context, strategyID uuid.UUID, filter repository.OrderFilter, limit, offset int) ([]domain.Order, error) {
	query, args := buildOrderQuery("account_strategy", []any{r.accountID, strategyID}, filter, limit, offset)
	return r.list(ctx, query, args, "get orders by strategy")
}

// GetByRun returns orders for the given pipeline run with optional filtering
// and pagination.
func (r *OrderRepo) GetByRun(ctx context.Context, ref domain.PipelineRunRef, filter repository.OrderFilter, limit, offset int) ([]domain.Order, error) {
	query, args := buildOrderQuery("pipeline_run", runOrderScope{r.accountID, ref}, filter, limit, offset)
	return r.list(ctx, query, args, "get orders by run")
}

func (r *OrderRepo) GetByCopyOriginRun(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment, subscriptionID, copyOriginRunID uuid.UUID, filter repository.OrderFilter, limit, offset int) ([]domain.Order, error) {
	if accountID != r.accountID {
		return []domain.Order{}, nil
	}
	query, args := buildOrderQuery("copy_origin", copyOrderScope{r.accountID, environment, subscriptionID, copyOriginRunID}, filter, limit, offset)
	return r.list(ctx, query, args, "get orders by copy origin run")
}

type copyOrderScope struct {
	accountID      uuid.UUID
	environment    domain.AccountEnvironment
	subscriptionID uuid.UUID
	runID          uuid.UUID
}

type runOrderScope struct {
	accountID uuid.UUID
	ref       domain.PipelineRunRef
}

const orderSelectSQL = `SELECT id, strategy_id, pipeline_run_id, account_id, environment, origin_type, origin_id,
		pipeline_run_trade_date, copy_origin_rebalance_run_id, external_id, ticker, market_type, side,
		order_type, quantity::double precision, limit_price::double precision,
		stop_price::double precision, filled_quantity::double precision,
		filled_avg_price::double precision, status, broker, submitted_at,
		filled_at, created_at, asset_class, underlying_ticker, option_type,
		strike::double precision, expiry, contract_multiplier::double precision,
		position_intent, leg_group_id, COALESCE(prediction_side, ''),
		COALESCE(polymarket_intent, ''), allocation_opportunity_id, COALESCE(client_order_id, '')
	 FROM orders`

func (r *OrderRepo) list(ctx context.Context, query string, args []any, op string) ([]domain.Order, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: %s: %w", op, err)
	}
	defer rows.Close()

	var orders []domain.Order
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: %s scan: %w", op, err)
		}
		orders = append(orders, *order)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: %s rows: %w", op, err)
	}

	return orders, nil
}

// scanOrder scans a single row (pgx.Row or pgx.Rows) into an Order. Nullable
// columns are scanned via pointer intermediates and converted to the Go zero
// value when NULL.
func scanOrder(sc scanner) (*domain.Order, error) {
	var (
		order           domain.Order
		strategyID      *uuid.UUID
		pipelineRunID   *uuid.UUID
		accountID       *uuid.UUID
		environment     *domain.AccountEnvironment
		originType      *string
		originID        *string
		copyOriginRunID *uuid.UUID
		externalID      *string
		marketType      *string
		limitPrice      *float64
		stopPrice       *float64
		filledAvgPrice  *float64
		broker          *string
		underlying      *string
		submittedAt     *time.Time
		filledAt        *time.Time
	)

	err := sc.Scan(
		&order.ID,
		&strategyID,
		&pipelineRunID,
		&accountID,
		&environment,
		&originType,
		&originID,
		&order.PipelineRunTradeDate,
		&copyOriginRunID,
		&externalID,
		&order.Ticker,
		&marketType,
		&order.Side,
		&order.OrderType,
		&order.Quantity,
		&limitPrice,
		&stopPrice,
		&order.FilledQuantity,
		&filledAvgPrice,
		&order.Status,
		&broker,
		&submittedAt,
		&filledAt,
		&order.CreatedAt,
		&order.AssetClass,
		&underlying,
		&order.OptionType,
		&order.Strike,
		&order.Expiry,
		&order.ContractMultiplier,
		&order.PositionIntent,
		&order.LegGroupID,
		&order.PredictionSide,
		&order.PolymarketIntent,
		&order.AllocationOpportunityID,
		&order.ClientOrderID,
	)
	if err != nil {
		return nil, err
	}

	order.StrategyID = strategyID
	order.PipelineRunID = pipelineRunID
	if accountID != nil {
		order.AccountID = *accountID
	}
	if environment != nil {
		order.Environment = *environment
	}
	if originType != nil {
		order.OriginType = *originType
	}
	if originID != nil {
		order.OriginID = *originID
	}
	if copyOriginRunID != nil {
		order.CopyOriginRebalanceRunID = *copyOriginRunID
	}
	order.LimitPrice = limitPrice
	order.StopPrice = stopPrice
	order.FilledAvgPrice = filledAvgPrice
	order.SubmittedAt = submittedAt
	order.FilledAt = filledAt

	if externalID != nil {
		order.ExternalID = *externalID
	}
	if marketType == nil || strings.TrimSpace(*marketType) == "" {
		order.MarketType = domain.MarketTypeStock
	} else {
		order.MarketType = domain.MarketType(strings.TrimSpace(*marketType)).Normalize()
	}
	if broker != nil {
		order.Broker = *broker
	}
	if underlying != nil {
		order.UnderlyingTicker = *underlying
	}

	return &order, nil
}

// buildOrderListQuery constructs the SELECT query and arguments for List with
// dynamic WHERE conditions. All values are parameterized.
// Count returns the total number of orders matching the filter (ignoring pagination).
func (r *OrderRepo) Count(ctx context.Context, filter repository.OrderFilter) (int, error) {
	query, args := buildOrderCountQuery(r.accountID, filter)
	var total int
	if err := r.pool.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("postgres: count orders: %w", err)
	}
	return total, nil
}

func buildOrderCountQuery(accountID uuid.UUID, filter repository.OrderFilter) (string, []any) {
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
	if filter.Ticker != "" {
		conditions = append(conditions, "ticker = "+nextArg(filter.Ticker))
	}
	if filter.Broker != "" {
		conditions = append(conditions, "broker = "+nextArg(filter.Broker))
	}
	if filter.MarketType != "" {
		conditions = append(conditions, "market_type = "+nextArg(filter.MarketType.Normalize()))
	}
	if filter.Side != "" {
		conditions = append(conditions, "side = "+nextArg(filter.Side))
	}
	if filter.OrderType != "" {
		conditions = append(conditions, "order_type = "+nextArg(filter.OrderType))
	}
	if filter.Status != "" {
		conditions = append(conditions, "status = "+nextArg(filter.Status))
	}
	if filter.SubmittedAfter != nil {
		conditions = append(conditions, "submitted_at >= "+nextArg(*filter.SubmittedAfter))
	}
	if filter.SubmittedBefore != nil {
		conditions = append(conditions, "submitted_at <= "+nextArg(*filter.SubmittedBefore))
	}
	query := `SELECT COUNT(*) FROM orders`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	return query, args
}

func buildOrderListQuery(filter repository.OrderFilter, limit, offset int) (string, []any) {
	return buildOrderQuery("", nil, filter, limit, offset)
}

// buildOrderScopedListQuery constructs the SELECT query and arguments for
// GetByStrategy and GetByRun using the supplied fixed scope.
func buildOrderScopedListQuery(scopeColumn string, scopeValue uuid.UUID, filter repository.OrderFilter, limit, offset int) (string, []any) {
	return buildOrderQuery(scopeColumn, scopeValue, filter, limit, offset)
}

func buildOrderQuery(scopeColumn string, scopeValue any, filter repository.OrderFilter, limit, offset int) (string, []any) {
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

	if scopeColumn == "copy_origin" {
		scope := scopeValue.(copyOrderScope)
		accountParameter := nextArg(scope.accountID)
		environmentParameter := nextArg(scope.environment)
		orderOriginParameter := nextArg(scope.subscriptionID.String())
		runParameter := nextArg(scope.runID)
		subscriptionParameter := nextArg(scope.subscriptionID)
		conditions = append(conditions,
			"account_id = "+accountParameter,
			"environment = "+environmentParameter,
			"origin_type = 'copy_subscription'",
			"origin_id = "+orderOriginParameter,
			"copy_origin_rebalance_run_id = "+runParameter,
			"EXISTS (SELECT 1 FROM copy_origin_rebalance_runs r WHERE r.id = "+runParameter+" AND r.account_id = "+accountParameter+" AND r.environment = "+environmentParameter+" AND r.subscription_id = "+subscriptionParameter+" AND r.origin_type = 'copy_subscription' AND r.origin_id = "+subscriptionParameter+")",
		)
	} else if scopeColumn == "pipeline_run" {
		scope := scopeValue.(runOrderScope)
		conditions = append(conditions, "account_id = "+nextArg(scope.accountID), "pipeline_run_id = "+nextArg(scope.ref.ID), "pipeline_run_trade_date = "+nextArg(scope.ref.TradeDate)+"::date")
	} else if scopeColumn == "account" {
		conditions = append(conditions, "account_id = "+nextArg(scopeValue))
	} else if scopeColumn == "account_strategy" {
		values := scopeValue.([]any)
		conditions = append(conditions, "account_id = "+nextArg(values[0]), "strategy_id = "+nextArg(values[1]))
	} else if scopeColumn != "" {
		conditions = append(conditions, scopeColumn+" = "+nextArg(scopeValue))
	}

	if filter.Ticker != "" {
		conditions = append(conditions, "ticker = "+nextArg(filter.Ticker))
	}

	if filter.Broker != "" {
		conditions = append(conditions, "broker = "+nextArg(filter.Broker))
	}

	if filter.MarketType != "" {
		conditions = append(conditions, "market_type = "+nextArg(filter.MarketType.Normalize()))
	}

	if filter.Side != "" {
		conditions = append(conditions, "side = "+nextArg(filter.Side))
	}

	if filter.OrderType != "" {
		conditions = append(conditions, "order_type = "+nextArg(filter.OrderType))
	}

	if filter.Status != "" {
		conditions = append(conditions, "status = "+nextArg(filter.Status))
	}

	if filter.SubmittedAfter != nil {
		conditions = append(conditions, "submitted_at >= "+nextArg(*filter.SubmittedAfter))
	}

	if filter.SubmittedBefore != nil {
		conditions = append(conditions, "submitted_at <= "+nextArg(*filter.SubmittedBefore))
	}

	query := orderSelectSQL
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	query += " ORDER BY created_at DESC, id DESC"
	query += fmt.Sprintf(" LIMIT %s OFFSET %s", nextArg(limit), nextArg(offset))

	return query, args
}

func nullString(value string) any {
	if value == "" {
		return nil
	}

	return value
}
