package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

func polymarketPositionTicker(slug, side string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return ""
	}
	side = strings.ToUpper(strings.TrimSpace(side))
	if side == "" {
		return slug
	}
	if existingSlug, existingSide, ok := strings.Cut(slug, ":"); ok {
		existingSlug = strings.TrimSpace(existingSlug)
		existingSide = strings.ToUpper(strings.TrimSpace(existingSide))
		if existingSlug != "" && (existingSide == "YES" || existingSide == "NO") {
			return existingSlug + ":" + existingSide
		}
	}
	return slug + ":" + side
}

func normalizedPositionTicker(marketType domain.MarketType, ticker, predictionSide string) string {
	ticker = strings.TrimSpace(ticker)
	if ticker == "" {
		return ""
	}
	switch marketType.Normalize() {
	case domain.MarketTypePolymarket, domain.MarketTypeKalshi:
		return polymarketPositionTicker(ticker, predictionSide)
	default:
		return ticker
	}
}

func realizedPnL(side domain.PositionSide, avgEntry, fillPrice, quantity float64) float64 {
	if side == domain.PositionSideLong {
		return (fillPrice - avgEntry) * quantity
	}
	return (avgEntry - fillPrice) * quantity
}

func (db *DB) ApplyOrderFill(ctx context.Context, input repository.OrderFillInput) (repository.OrderFillResult, error) {
	if err := validateOrderFillInput(input); err != nil {
		return repository.OrderFillResult{}, err
	}

	tx, err := db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return repository.OrderFillResult{}, fmt.Errorf("postgres: begin order fill tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var existingOrderID uuid.UUID
	var existingPositionID *uuid.UUID
	var existingTradeID uuid.UUID
	var existingFillQuantity float64
	var existingFillPrice float64
	var existingCreatedAt time.Time
	var existingAccountID uuid.UUID
	var existingEnvironment, existingOriginType, existingOriginID string
	if err := tx.QueryRow(ctx, `SELECT order_id, position_id, trade_id, fill_quantity, fill_price, created_at, account_id, environment, origin_type, origin_id FROM financial_fill_idempotency WHERE idempotency_key = $1 FOR UPDATE`, input.IdempotencyKey).Scan(&existingOrderID, &existingPositionID, &existingTradeID, &existingFillQuantity, &existingFillPrice, &existingCreatedAt, &existingAccountID, &existingEnvironment, &existingOriginType, &existingOriginID); err == nil {
		if existingOrderID != input.Order.ID || existingAccountID != input.Order.AccountID || existingEnvironment != string(input.Order.Environment) || existingOriginType != input.Order.OriginType || existingOriginID != input.Order.OriginID || !numeric8Equal(existingFillQuantity, input.FillIntent.Quantity) || !numeric8Equal(existingFillPrice, input.FillIntent.ExecutionPrice) {
			return repository.OrderFillResult{}, fmt.Errorf("postgres: idempotency key %s reused with mismatched payload", input.IdempotencyKey)
		}
		var existingPosition *domain.Position
		if existingPositionID != nil {
			existingPosition, err = scanPosition(tx.QueryRow(ctx, positionSelectSQL+` WHERE p.id=$1 AND p.account_id=$2`, *existingPositionID, existingAccountID))
			if err != nil {
				return repository.OrderFillResult{}, fmt.Errorf("postgres: load replayed fill position: %w", err)
			}
		}
		existingTrade, err := scanTrade(tx.QueryRow(ctx, tradeSelectSQL+` WHERE id=$1 AND account_id=$2`, existingTradeID, existingAccountID))
		if err != nil {
			return repository.OrderFillResult{}, fmt.Errorf("postgres: load replayed fill trade: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return repository.OrderFillResult{}, fmt.Errorf("postgres: commit replayed order fill: %w", err)
		}
		return repository.OrderFillResult{OrderID: existingOrderID, PositionID: existingPositionID, Position: existingPosition, TradeID: existingTradeID, Trade: existingTrade, CreatedAt: existingCreatedAt, Replayed: true}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return repository.OrderFillResult{}, fmt.Errorf("postgres: select order fill idempotency: %w", err)
	}

	var persistedStatus, persistedEnvironment, persistedOriginType, persistedOriginID string
	var persistedAccountID uuid.UUID
	var persistedFilledQuantity float64
	var persistedFilledAvgPrice *float64
	var persistedTicker, persistedPredictionSide string
	var persistedMarketType domain.MarketType
	var persistedSide domain.OrderSide
	var persistedQuantity float64
	var persistedPositionIntent *domain.PositionIntent
	if err := tx.QueryRow(ctx, `SELECT status,account_id,environment,origin_type,origin_id,filled_quantity::double precision,filled_avg_price::double precision,ticker,market_type,side,quantity::double precision,position_intent,COALESCE(prediction_side,'') FROM orders WHERE id = $1 FOR UPDATE`, input.Order.ID).Scan(&persistedStatus, &persistedAccountID, &persistedEnvironment, &persistedOriginType, &persistedOriginID, &persistedFilledQuantity, &persistedFilledAvgPrice, &persistedTicker, &persistedMarketType, &persistedSide, &persistedQuantity, &persistedPositionIntent, &persistedPredictionSide); err != nil {
		return repository.OrderFillResult{}, fmt.Errorf("postgres: lock order: %w", err)
	}
	if persistedAccountID != input.Order.AccountID || persistedEnvironment != string(input.Order.Environment) || persistedOriginType != input.Order.OriginType || persistedOriginID != input.Order.OriginID {
		return repository.OrderFillResult{}, fmt.Errorf("postgres: order fill scope mismatch")
	}
	if persistedStatus != string(domain.OrderStatusPending) && persistedStatus != string(domain.OrderStatusSubmitted) && persistedStatus != string(domain.OrderStatusPartial) && persistedStatus != string(domain.OrderStatusFilled) && persistedStatus != string(domain.OrderStatusCancelled) && persistedStatus != string(domain.OrderStatusRejected) {
		return repository.OrderFillResult{}, fmt.Errorf("postgres: order %s status %s not fill-compatible", input.Order.ID, persistedStatus)
	}
	if input.FillIntent.Side != persistedSide {
		return repository.OrderFillResult{}, fmt.Errorf("postgres: fill command side does not match persisted order")
	}
	orderValue := *input.Order
	order := &orderValue
	order.AccountID, order.Environment, order.OriginType, order.OriginID = persistedAccountID, domain.AccountEnvironment(persistedEnvironment), persistedOriginType, persistedOriginID
	order.Ticker, order.MarketType, order.Side, order.Quantity, order.PositionIntent, order.PredictionSide = persistedTicker, persistedMarketType, persistedSide, persistedQuantity, persistedPositionIntent, persistedPredictionSide
	observedQuantity := input.FillIntent.Quantity
	if observedQuantity <= persistedFilledQuantity {
		return repository.OrderFillResult{}, fmt.Errorf("postgres: observed fill quantity %.8f does not advance persisted quantity %.8f", observedQuantity, persistedFilledQuantity)
	}
	if observedQuantity > persistedQuantity {
		return repository.OrderFillResult{}, fmt.Errorf("postgres: cumulative fill quantity %.8f exceeds persisted order size %.8f", observedQuantity, persistedQuantity)
	}
	observedAvgPrice := input.FillIntent.ExecutionPrice
	if observedAvgPrice == 0 && order.FilledAvgPrice != nil {
		observedAvgPrice = *order.FilledAvgPrice
	}
	deltaQuantity := observedQuantity - persistedFilledQuantity
	fillPrice := observedAvgPrice
	if persistedFilledQuantity > 0 && persistedFilledAvgPrice != nil {
		fillPrice = (observedAvgPrice*observedQuantity - *persistedFilledAvgPrice*persistedFilledQuantity) / deltaQuantity
	}

	order.FilledQuantity = observedQuantity
	now := input.Now.UTC()
	order.FilledAt = &now
	if order.Status != domain.OrderStatusPartial && order.Status != domain.OrderStatusFilled && order.Status != domain.OrderStatusCancelled && order.Status != domain.OrderStatusRejected {
		order.Status = domain.OrderStatusFilled
	}
	if _, err := tx.Exec(ctx, `UPDATE orders SET filled_quantity=$1,filled_avg_price=$2,status=$3,filled_at=$4,external_id=COALESCE(NULLIF($6,''),external_id),broker=COALESCE(NULLIF($7,''),broker),submitted_at=COALESCE(submitted_at,$8) WHERE id=$5`, order.FilledQuantity, observedAvgPrice, order.Status, order.FilledAt, order.ID, strings.TrimSpace(order.ExternalID), strings.TrimSpace(order.Broker), order.SubmittedAt); err != nil {
		return repository.OrderFillResult{}, fmt.Errorf("postgres: update filled order: %w", err)
	}

	marketType := order.MarketType.Normalize()
	if marketType == "" {
		marketType = domain.MarketTypeStock
	}
	positionTicker := normalizedPositionTicker(marketType, order.Ticker, order.PredictionSide)

	var position *domain.Position
	var positionID *uuid.UUID
	closingPosition := order.Side == domain.OrderSideSell
	closingPositionSide := domain.PositionSideLong
	if order.PositionIntent != nil && *order.PositionIntent == domain.PositionIntentBuyToClose {
		closingPosition = true
		closingPositionSide = domain.PositionSideShort
	}
	if closingPosition {
		positionQuery := `SELECT p.id, p.strategy_id, p.account_id, p.environment, p.origin_type, p.origin_id, s.market_type, p.ticker, p.side, p.quantity::double precision, p.avg_entry::double precision,
			p.current_price::double precision, p.unrealized_pnl::double precision, p.realized_pnl::double precision, p.stop_loss::double precision,
			p.take_profit::double precision, p.opened_at, p.closed_at, p.asset_class, p.underlying_ticker, p.option_type, p.strike::double precision,
			p.expiry, p.contract_multiplier::double precision, p.leg_group_id, p.delta::double precision, p.gamma::double precision, p.theta::double precision, p.vega::double precision
			FROM positions p LEFT JOIN strategies s ON s.id = p.strategy_id
			WHERE p.account_id = $1 AND p.environment = $2 AND p.origin_type = $3 AND p.origin_id = $4 AND p.ticker = $5 AND p.side = $6 AND p.closed_at IS NULL AND p.quantity > 0
			ORDER BY p.opened_at ASC, p.id ASC FOR UPDATE OF p`
		args := []any{order.AccountID, order.Environment, order.OriginType, order.OriginID, positionTicker, closingPositionSide}
		if order.PositionIntent != nil && (*order.PositionIntent == domain.PositionIntentSellToClose || *order.PositionIntent == domain.PositionIntentBuyToClose) {
			positionQuery = strings.Replace(positionQuery, " ORDER BY", " AND p.close_reservation_order_id=$7 ORDER BY", 1)
			args = append(args, order.ID)
		}
		rows, err := tx.Query(ctx, positionQuery, args...)
		if err != nil {
			return repository.OrderFillResult{}, fmt.Errorf("postgres: lock polymarket position: %w", err)
		}
		defer rows.Close()
		var (
			matchedPositions []*domain.Position
			totalAvailable   float64
		)
		for rows.Next() {
			matchedPosition, scanErr := scanPosition(rows)
			if scanErr != nil {
				return repository.OrderFillResult{}, fmt.Errorf("postgres: scan polymarket position: %w", scanErr)
			}
			totalAvailable += matchedPosition.Quantity
			matchedPositions = append(matchedPositions, matchedPosition)
		}
		if err := rows.Err(); err != nil {
			return repository.OrderFillResult{}, fmt.Errorf("postgres: iterate polymarket positions: %w", err)
		}
		if len(matchedPositions) == 0 {
			return repository.OrderFillResult{}, fmt.Errorf("postgres: sell fill has no open position for %s", positionTicker)
		}
		if totalAvailable < deltaQuantity {
			return repository.OrderFillResult{}, fmt.Errorf("postgres: polymarket sell fill quantity %.8f exceeds open long quantity %.8f for %s", deltaQuantity, totalAvailable, positionTicker)
		}

		remaining := deltaQuantity
		updatedIDs := make([]uuid.UUID, 0, len(matchedPositions))
		closedIDs := make([]uuid.UUID, 0, len(matchedPositions))
		if len(matchedPositions) == 1 {
			position = matchedPositions[0]
			positionID = &position.ID
		}
		for _, matchedPosition := range matchedPositions {
			if remaining <= 0 {
				break
			}
			consume := math.Min(matchedPosition.Quantity, remaining)
			currentPrice := fillPrice
			matchedPosition.CurrentPrice = &currentPrice
			matchedPosition.RealizedPnL += realizedPnL(matchedPosition.Side, matchedPosition.AvgEntry, fillPrice, consume)
			matchedPosition.Quantity -= consume
			if matchedPosition.Quantity == 0 {
				closedAt := now
				matchedPosition.ClosedAt = &closedAt
				closedIDs = append(closedIDs, matchedPosition.ID)
			}
			releaseReservation := order.Status == domain.OrderStatusFilled || order.Status == domain.OrderStatusCancelled || order.Status == domain.OrderStatusRejected
			if _, err := tx.Exec(ctx, `UPDATE positions SET quantity = $1, current_price = $2, realized_pnl = $3, closed_at = $4, close_reservation_order_id=CASE WHEN $7 AND close_reservation_order_id=$6 THEN NULL ELSE close_reservation_order_id END WHERE id = $5`, matchedPosition.Quantity, matchedPosition.CurrentPrice, matchedPosition.RealizedPnL, matchedPosition.ClosedAt, matchedPosition.ID, order.ID, releaseReservation); err != nil {
				return repository.OrderFillResult{}, fmt.Errorf("postgres: update polymarket position: %w", err)
			}
			updatedIDs = append(updatedIDs, matchedPosition.ID)
			remaining -= consume
		}
		if remaining > 0 {
			return repository.OrderFillResult{}, fmt.Errorf("postgres: sell fill did not consume enough quantity")
		}
		if terminalOrderStatusPostgres(order.Status) {
			if _, err := tx.Exec(ctx, `UPDATE positions SET close_reservation_order_id=NULL WHERE account_id=$1 AND environment=$2 AND close_reservation_order_id=$3`, order.AccountID, order.Environment, order.ID); err != nil {
				return repository.OrderFillResult{}, fmt.Errorf("postgres: release unconsumed terminal close reservations: %w", err)
			}
		}
		if len(updatedIDs) > 1 {
			position = nil
			positionID = nil
		}
		if _, err := tx.Exec(ctx, `UPDATE trade_decisions SET status = $1, updated_at = $2
			WHERE status = $3 AND account_id=$6 AND environment=$7 AND origin_type=$8 AND origin_id=$9 AND (
				paper_order_id = $4 OR
				(cardinality($5::uuid[]) > 0 AND paper_order_id IN (
					SELECT DISTINCT t.order_id FROM trades t WHERE t.position_id = ANY($5::uuid[]) AND t.account_id=$6 AND t.environment=$7 AND t.origin_type=$8 AND t.origin_id=$9
				))
			)`, domain.TradeDecisionStatusClosed, now, domain.TradeDecisionStatusPaper, order.ID, closedIDs, order.AccountID, order.Environment, order.OriginType, order.OriginID); err != nil {
			return repository.OrderFillResult{}, fmt.Errorf("postgres: close prediction exit decisions: %w", err)
		}
	} else {
		positionSide := domain.PositionSideLong
		var linkedID uuid.UUID
		var linkedQuantity, linkedAverage float64
		linkedErr := tx.QueryRow(ctx, `SELECT p.id,p.quantity::double precision,p.avg_entry::double precision FROM positions p JOIN trades t ON t.position_id=p.id WHERE t.order_id=$1 AND t.account_id=$2 ORDER BY t.created_at LIMIT 1 FOR UPDATE OF p`, order.ID, order.AccountID).Scan(&linkedID, &linkedQuantity, &linkedAverage)
		if linkedErr == nil {
			newQuantity := linkedQuantity + deltaQuantity
			newAverage := (linkedQuantity*linkedAverage + deltaQuantity*fillPrice) / newQuantity
			if _, err := tx.Exec(ctx, `UPDATE positions SET quantity=$1,avg_entry=$2,current_price=$3 WHERE id=$4`, newQuantity, newAverage, fillPrice, linkedID); err != nil {
				return repository.OrderFillResult{}, fmt.Errorf("postgres: advance order-linked position: %w", err)
			}
			position = &domain.Position{ID: linkedID, AccountID: order.AccountID, Environment: order.Environment, OriginType: order.OriginType, OriginID: order.OriginID, StrategyID: order.StrategyID, MarketType: marketType, Ticker: positionTicker, Side: positionSide, Quantity: newQuantity, AvgEntry: newAverage, OpenedAt: now}
		} else if !errors.Is(linkedErr, pgx.ErrNoRows) {
			return repository.OrderFillResult{}, fmt.Errorf("postgres: load order-linked position: %w", linkedErr)
		} else {
			position = &domain.Position{ID: uuid.New(), AccountID: order.AccountID, Environment: order.Environment, OriginType: order.OriginType, OriginID: order.OriginID, StrategyID: order.StrategyID, MarketType: marketType, Ticker: positionTicker, Side: positionSide, Quantity: deltaQuantity, AvgEntry: fillPrice, OpenedAt: now}
			if input.StopLoss != nil {
				position.StopLoss = input.StopLoss
			}
			if input.TakeProfit != nil {
				position.TakeProfit = input.TakeProfit
			}
			if err := tx.QueryRow(ctx, `INSERT INTO positions (id, strategy_id, account_id, environment, origin_type, origin_id, ticker, side, quantity, avg_entry, stop_loss, take_profit, opened_at, asset_class, underlying_ticker, contract_multiplier)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) RETURNING id, opened_at`, position.ID, position.StrategyID, position.AccountID, position.Environment, position.OriginType, position.OriginID, position.Ticker, position.Side, position.Quantity, position.AvgEntry, position.StopLoss, position.TakeProfit, position.OpenedAt, position.AssetClass, nullString(position.UnderlyingTicker), position.ContractMultiplier).Scan(&position.ID, &position.OpenedAt); err != nil {
				return repository.OrderFillResult{}, fmt.Errorf("postgres: create position: %w", err)
			}
		}
	}

	trade := input.Trade
	trade.OrderID = &order.ID
	if position != nil {
		trade.PositionID = &position.ID
		if positionID == nil {
			positionID = &position.ID
		}
	} else {
		trade.PositionID = nil
	}
	trade.Ticker = order.Ticker
	trade.Side = order.Side
	trade.Quantity = deltaQuantity
	trade.Price = fillPrice
	trade.ExecutedAt = now
	trade.CreatedAt = now
	trade.AccountID, trade.Environment, trade.OriginType, trade.OriginID = order.AccountID, order.Environment, order.OriginType, order.OriginID
	if err := tx.QueryRow(ctx, `INSERT INTO trades (account_id,environment,origin_type,origin_id,external_id, order_id, position_id, ticker, side, quantity, price, fee, executed_at, asset_class, open_close, contract_multiplier, premium)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17) RETURNING id, created_at`, trade.AccountID, trade.Environment, trade.OriginType, trade.OriginID, nullString(trade.ExternalID), trade.OrderID, trade.PositionID, trade.Ticker, trade.Side, trade.Quantity, trade.Price, trade.Fee, trade.ExecutedAt, trade.AssetClass, nullString(trade.OpenClose), trade.ContractMultiplier, trade.Premium).Scan(&trade.ID, &trade.CreatedAt); err != nil {
		return repository.OrderFillResult{}, fmt.Errorf("postgres: create trade: %w", err)
	}

	if _, err := tx.Exec(ctx, `INSERT INTO financial_fill_idempotency (idempotency_key,account_id,environment,origin_type,origin_id,order_id,position_id,trade_id,fill_quantity,fill_price) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, input.IdempotencyKey, order.AccountID, order.Environment, order.OriginType, order.OriginID, order.ID, positionID, trade.ID, input.FillIntent.Quantity, input.FillIntent.ExecutionPrice); err != nil {
		return repository.OrderFillResult{}, fmt.Errorf("postgres: finalize fill idempotency: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return repository.OrderFillResult{}, fmt.Errorf("postgres: commit order fill: %w", err)
	}
	return repository.OrderFillResult{OrderID: order.ID, PositionID: positionID, Position: position, TradeID: trade.ID, Trade: trade, CreatedAt: trade.CreatedAt}, nil
}

func (db *DB) ResolveOrderFillCommit(ctx context.Context, input repository.OrderFillInput) (repository.OrderFillResult, bool, error) {
	if err := validateOrderFillInput(input); err != nil {
		return repository.OrderFillResult{}, false, err
	}
	var result repository.OrderFillResult
	var accountID uuid.UUID
	var environment domain.AccountEnvironment
	var originType, originID string
	var quantity, price float64
	err := db.Pool.QueryRow(ctx, `SELECT order_id,position_id,trade_id,created_at,account_id,environment,origin_type,origin_id,fill_quantity::double precision,fill_price::double precision FROM financial_fill_idempotency WHERE idempotency_key=$1`, input.IdempotencyKey).Scan(&result.OrderID, &result.PositionID, &result.TradeID, &result.CreatedAt, &accountID, &environment, &originType, &originID, &quantity, &price)
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.OrderFillResult{}, false, nil
	}
	if err != nil {
		return repository.OrderFillResult{}, false, fmt.Errorf("postgres: resolve order fill commit: %w", err)
	}
	if result.OrderID != input.Order.ID || accountID != input.Order.AccountID || environment != input.Order.Environment || originType != input.Order.OriginType || originID != input.Order.OriginID || !numeric8Equal(quantity, input.FillIntent.Quantity) || !numeric8Equal(price, input.FillIntent.ExecutionPrice) {
		return repository.OrderFillResult{}, false, fmt.Errorf("postgres: resolved order fill payload mismatch")
	}
	if result.PositionID != nil {
		result.Position, err = scanPosition(db.Pool.QueryRow(ctx, positionSelectSQL+` WHERE p.id=$1 AND p.account_id=$2`, *result.PositionID, accountID))
		if err != nil {
			return repository.OrderFillResult{}, false, err
		}
	}
	result.Trade, err = scanTrade(db.Pool.QueryRow(ctx, tradeSelectSQL+` WHERE id=$1 AND account_id=$2`, result.TradeID, accountID))
	if err != nil {
		return repository.OrderFillResult{}, false, err
	}
	result.Replayed = true
	return result, true, nil
}

func validateOrderFillInput(input repository.OrderFillInput) error {
	if input.Order == nil || input.Order.ID == uuid.Nil || input.Order.AccountID == uuid.Nil || !input.Order.Environment.IsValid() || strings.TrimSpace(input.Order.OriginType) == "" || strings.TrimSpace(input.Order.OriginID) == "" || input.Order.Ticker == "" || input.Order.MarketType == "" || input.Trade == nil || input.IdempotencyKey == "" || input.FillIntent.Quantity <= 0 || math.IsNaN(input.FillIntent.ExecutionPrice) || math.IsInf(input.FillIntent.ExecutionPrice, 0) || input.FillIntent.ExecutionPrice < 0 || input.Order.Quantity <= 0 || input.Order.Side == "" || input.Order.Status == "" || input.Now.IsZero() {
		return fmt.Errorf("postgres: invalid order fill input")
	}
	return nil
}

// ApplyOptionFills atomically persists a complete single-leg fill or all legs
// of a spread. Each order receives a stable idempotency record in the existing
// financial-fill ledger, and a mixed partial replay is rejected.
func (db *DB) ApplyOptionFills(ctx context.Context, inputs []repository.OptionFillInput) ([]repository.OptionFillResult, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("postgres: option fills are required")
	}
	seenOrders := make(map[uuid.UUID]struct{}, len(inputs))
	for _, input := range inputs {
		if err := validateOptionFillInput(input); err != nil {
			return nil, err
		}
		if _, exists := seenOrders[input.Order.ID]; exists {
			return nil, fmt.Errorf("postgres: duplicate option fill order %s", input.Order.ID)
		}
		seenOrders[input.Order.ID] = struct{}{}
	}

	tx, err := db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("postgres: begin option fill tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	lockKeys := make([]string, 0, len(inputs))
	for _, input := range inputs {
		lockKeys = append(lockKeys, input.IdempotencyKey)
	}
	sort.Strings(lockKeys)
	for _, key := range lockKeys {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, financialLifecycleLockKey("option-fill", key)); err != nil {
			return nil, fmt.Errorf("postgres: lock option fill idempotency: %w", err)
		}
	}

	results := make([]repository.OptionFillResult, len(inputs))
	replayed := 0
	for index, input := range inputs {
		if input.StatusOnly {
			var existing optionStatusIdempotencyEvidence
			err := tx.QueryRow(ctx, `SELECT idempotency_key,order_id,account_id,environment,origin_type,origin_id,status,filled_quantity::double precision,COALESCE(external_id,''),submitted_at FROM option_status_idempotency WHERE idempotency_key=$1 OR order_id=$2 FOR UPDATE`, input.IdempotencyKey, input.Order.ID).Scan(&existing.key, &existing.orderID, &existing.accountID, &existing.environment, &existing.originType, &existing.originID, &existing.status, &existing.quantity, &existing.externalID, &existing.submittedAt)
			if err == nil {
				if !optionStatusIdempotencyMatches(existing, input) {
					return nil, fmt.Errorf("postgres: option status idempotency mismatch for order %s", input.Order.ID)
				}
				results[index].OrderID = existing.orderID
				continue
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("postgres: select option status idempotency: %w", err)
			}
			if err := applyOptionStatusTx(ctx, tx, input); err != nil {
				return nil, err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO option_status_idempotency(idempotency_key,account_id,environment,origin_type,origin_id,order_id,status,filled_quantity,external_id,submitted_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, input.IdempotencyKey, input.AccountID, input.Environment, input.OriginType, input.OriginID, input.Order.ID, input.Order.Status, input.FillQuantity, nullString(input.Order.ExternalID), input.Order.SubmittedAt); err != nil {
				return nil, fmt.Errorf("postgres: persist option status idempotency: %w", err)
			}
			results[index].OrderID = input.Order.ID
			continue
		}
		key := input.IdempotencyKey
		var existingOrderID uuid.UUID
		var existingPositionID *uuid.UUID
		var existingTradeID uuid.UUID
		var existingQuantity, existingPrice, existingFee, existingPremium float64
		var existingFilledAt time.Time
		var existingExitReason string
		var existingAccountID uuid.UUID
		var existingEnvironment domain.AccountEnvironment
		var existingOriginType, existingOriginID string
		err := tx.QueryRow(ctx, `SELECT f.order_id, f.position_id, f.trade_id, f.fill_quantity, f.fill_price,
			f.account_id,f.environment,f.origin_type,f.origin_id,
			COALESCE(f.cumulative_fee,(SELECT SUM(all_t.fee) FROM trades all_t WHERE all_t.order_id=f.order_id AND all_t.account_id=f.account_id),0)::double precision,
			COALESCE(f.cumulative_premium,(SELECT SUM(all_t.premium) FROM trades all_t WHERE all_t.order_id=f.order_id AND all_t.account_id=f.account_id),0)::double precision,
			COALESCE(f.cumulative_filled_at,t.executed_at), COALESCE(f.cumulative_exit_reason,t.exit_reason,'')
			FROM financial_fill_idempotency f JOIN trades t ON t.id=f.trade_id
			WHERE f.idempotency_key=$1 FOR UPDATE OF f`, key).Scan(
			&existingOrderID, &existingPositionID, &existingTradeID, &existingQuantity, &existingPrice,
			&existingAccountID, &existingEnvironment, &existingOriginType, &existingOriginID,
			&existingFee, &existingPremium, &existingFilledAt, &existingExitReason,
		)
		switch {
		case err == nil:
			positionMismatch := input.PositionID != nil && (existingPositionID == nil || *existingPositionID != *input.PositionID)
			if existingOrderID != input.Order.ID || existingAccountID != input.AccountID || existingEnvironment != input.Environment || existingOriginType != input.OriginType || existingOriginID != input.OriginID || existingPositionID == nil || positionMismatch || !numeric8Equal(existingQuantity, input.FillQuantity) || !numeric8Equal(existingPrice, input.FillPrice) || !numeric8Equal(existingFee, input.Fee) || !numeric8Equal(existingPremium, input.Premium) || !existingFilledAt.Equal(input.FilledAt.UTC()) || existingExitReason != strings.TrimSpace(input.ExitReason) {
				return nil, fmt.Errorf("postgres: option fill idempotency mismatch for order %s", input.Order.ID)
			}
			results[index] = repository.OptionFillResult{OrderID: existingOrderID, PositionID: *existingPositionID, TradeID: existingTradeID}
			replayed++
		case errors.Is(err, pgx.ErrNoRows):
			// Persisted below after the batch replay state is known.
		default:
			return nil, fmt.Errorf("postgres: select option fill idempotency: %w", err)
		}
	}
	nonStatusCount := 0
	for _, input := range inputs {
		if !input.StatusOnly {
			nonStatusCount++
		}
	}
	if replayed != 0 {
		if replayed != nonStatusCount {
			return nil, fmt.Errorf("postgres: partial option fill replay detected")
		}
	}

	for index, input := range inputs {
		if input.StatusOnly {
			continue
		}
		if replayed != 0 {
			continue
		}
		result, err := applyOptionFillTx(ctx, tx, input)
		if err != nil {
			return nil, err
		}
		results[index] = result
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres: commit option fills: %w", err)
	}
	return results, nil
}

type optionStatusIdempotencyEvidence struct {
	key                              string
	orderID, accountID               uuid.UUID
	environment                      domain.AccountEnvironment
	originType, originID, externalID string
	status                           domain.OrderStatus
	quantity                         float64
	submittedAt                      *time.Time
}

func optionStatusIdempotencyMatches(existing optionStatusIdempotencyEvidence, input repository.OptionFillInput) bool {
	return existing.key == input.IdempotencyKey && existing.orderID == input.Order.ID && existing.accountID == input.AccountID && existing.environment == input.Environment &&
		existing.originType == input.OriginType && existing.originID == input.OriginID && existing.status == input.Order.Status && numeric8Equal(existing.quantity, input.FillQuantity) &&
		existing.externalID == strings.TrimSpace(input.Order.ExternalID) && sameTimePointer(existing.submittedAt, input.Order.SubmittedAt)
}

func (db *DB) ResolveOptionFillCommit(ctx context.Context, inputs []repository.OptionFillInput) ([]repository.OptionFillResult, bool, error) {
	if len(inputs) == 0 {
		return nil, false, fmt.Errorf("postgres: option fills are required")
	}
	results := make([]repository.OptionFillResult, len(inputs))
	for index, input := range inputs {
		if input.StatusOnly {
			var orderID uuid.UUID
			var status domain.OrderStatus
			var quantity float64
			var externalID string
			var submittedAt *time.Time
			if err := db.Pool.QueryRow(ctx, `SELECT order_id,status,filled_quantity::double precision,COALESCE(external_id,''),submitted_at FROM option_status_idempotency WHERE idempotency_key=$1 AND account_id=$2 AND environment=$3 AND origin_type=$4 AND origin_id=$5`, input.IdempotencyKey, input.AccountID, input.Environment, input.OriginType, input.OriginID).Scan(&orderID, &status, &quantity, &externalID, &submittedAt); errors.Is(err, pgx.ErrNoRows) {
				return nil, false, nil
			} else if err != nil {
				return nil, false, fmt.Errorf("postgres: resolve option recovery status: %w", err)
			}
			if orderID != input.Order.ID || status != input.Order.Status || !numeric8Equal(quantity, input.FillQuantity) || externalID != strings.TrimSpace(input.Order.ExternalID) || !sameTimePointer(submittedAt, input.Order.SubmittedAt) {
				return nil, false, fmt.Errorf("postgres: resolved option status payload mismatch for order %s", input.Order.ID)
			}
			results[index].OrderID = input.Order.ID
			continue
		}
		var accountID uuid.UUID
		var environment domain.AccountEnvironment
		var originType, originID string
		var quantity, price, fee, premium float64
		var status domain.OrderStatus
		var filledAt time.Time
		var exitReason string
		err := db.Pool.QueryRow(ctx, `SELECT f.order_id,f.position_id,f.trade_id,f.account_id,f.environment,f.origin_type,f.origin_id,f.fill_quantity::double precision,f.fill_price::double precision,
			COALESCE(f.cumulative_fee,(SELECT SUM(t.fee) FROM trades t WHERE t.order_id=f.order_id AND t.account_id=f.account_id),0)::double precision,
			COALESCE(f.cumulative_premium,(SELECT SUM(t.premium) FROM trades t WHERE t.order_id=f.order_id AND t.account_id=f.account_id),0)::double precision,
			COALESCE(f.cumulative_status,o.status),COALESCE(f.cumulative_filled_at,o.filled_at),COALESCE(f.cumulative_exit_reason,'')
			FROM financial_fill_idempotency f JOIN orders o ON o.id=f.order_id AND o.account_id=f.account_id WHERE f.idempotency_key=$1`, input.IdempotencyKey).Scan(
			&results[index].OrderID, &results[index].PositionID, &results[index].TradeID, &accountID, &environment, &originType, &originID, &quantity, &price, &fee, &premium, &status, &filledAt, &exitReason,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("postgres: resolve option fill commit: %w", err)
		}
		if results[index].OrderID != input.Order.ID || input.PositionID != nil && results[index].PositionID != *input.PositionID || accountID != input.AccountID || environment != input.Environment || originType != input.OriginType || originID != input.OriginID || !numeric8Equal(quantity, input.FillQuantity) || !numeric8Equal(price, input.FillPrice) || !numeric8Equal(fee, input.Fee) || !numeric8Equal(premium, input.Premium) || status != input.Order.Status || !filledAt.Equal(input.FilledAt.UTC()) || strings.TrimSpace(exitReason) != strings.TrimSpace(input.ExitReason) {
			return nil, false, fmt.Errorf("postgres: resolved option fill payload mismatch for order %s", input.Order.ID)
		}
	}
	return results, true, nil
}

func validateOptionFillInput(input repository.OptionFillInput) error {
	order := input.Order
	if input.StatusOnly {
		if order == nil || strings.TrimSpace(input.IdempotencyKey) == "" || input.AccountID == uuid.Nil || !input.Environment.IsValid() || strings.TrimSpace(input.OriginType) == "" || strings.TrimSpace(input.OriginID) == "" || order.ID == uuid.Nil || order.AccountID != input.AccountID || order.Environment != input.Environment || order.OriginType != input.OriginType || order.OriginID != input.OriginID || order.MarketType.Normalize() != domain.MarketTypeOptions || !terminalOrderStatusPostgres(order.Status) || input.FillQuantity < 0 {
			return fmt.Errorf("postgres: invalid option recovery status input")
		}
		return nil
	}
	if order == nil || input.IdempotencyKey == "" || input.AccountID == uuid.Nil || !input.Environment.IsValid() || strings.TrimSpace(input.OriginType) == "" || strings.TrimSpace(input.OriginID) == "" || order.ID == uuid.Nil || order.AccountID != input.AccountID || order.Environment != input.Environment || order.OriginType != input.OriginType || order.OriginID != input.OriginID || order.StrategyID == nil || order.Ticker == "" || strings.TrimSpace(order.ExternalID) == "" || strings.TrimSpace(order.Broker) == "" || order.SubmittedAt == nil || order.MarketType.Normalize() != domain.MarketTypeOptions || order.AssetClass != domain.AssetClassOption || order.PositionIntent == nil {
		return fmt.Errorf("postgres: invalid option fill input")
	}
	if order.Status != domain.OrderStatusPartial && order.Status != domain.OrderStatusFilled && order.Status != domain.OrderStatusCancelled && order.Status != domain.OrderStatusRejected || order.FilledAvgPrice == nil || order.FilledAt == nil || input.FillQuantity > order.Quantity || !numeric8Equal(order.FilledQuantity, input.FillQuantity) || !numeric8Equal(*order.FilledAvgPrice, input.FillPrice) || !order.FilledAt.UTC().Equal(input.FilledAt.UTC()) || order.SubmittedAt.After(*order.FilledAt) {
		return fmt.Errorf("postgres: option order does not contain the reported fill")
	}
	if input.FilledAt.IsZero() || input.FillQuantity <= 0 || input.FillPrice < 0 || input.Fee < 0 || input.Premium < 0 || math.IsNaN(input.FillQuantity) || math.IsInf(input.FillQuantity, 0) || math.IsNaN(input.FillPrice) || math.IsInf(input.FillPrice, 0) || math.IsNaN(input.Fee) || math.IsInf(input.Fee, 0) || math.IsNaN(input.Premium) || math.IsInf(input.Premium, 0) {
		return fmt.Errorf("postgres: invalid option fill accounting")
	}
	validOptionType := order.OptionType != nil && (*order.OptionType == domain.OptionTypeCall || *order.OptionType == domain.OptionTypePut)
	if !validOptionType || order.Strike == nil || *order.Strike <= 0 || math.IsNaN(*order.Strike) || math.IsInf(*order.Strike, 0) || order.Expiry == nil || order.Expiry.IsZero() || strings.TrimSpace(order.UnderlyingTicker) == "" || order.ContractMultiplier <= 0 || math.IsNaN(order.ContractMultiplier) || math.IsInf(order.ContractMultiplier, 0) || !numeric8Equal(input.Premium, input.FillPrice*input.FillQuantity*order.ContractMultiplier) {
		return fmt.Errorf("postgres: option fill has incomplete or inconsistent contract accounting")
	}
	switch *order.PositionIntent {
	case domain.PositionIntentBuyToOpen:
		if order.Side != domain.OrderSideBuy || input.PositionID != nil || strings.TrimSpace(input.ExitReason) != "" {
			return fmt.Errorf("postgres: invalid buy-to-open fill")
		}
	case domain.PositionIntentSellToOpen:
		if order.Side != domain.OrderSideSell || input.PositionID != nil || strings.TrimSpace(input.ExitReason) != "" {
			return fmt.Errorf("postgres: invalid sell-to-open fill")
		}
	case domain.PositionIntentBuyToClose:
		if order.Side != domain.OrderSideBuy || input.PositionID == nil || strings.TrimSpace(input.ExitReason) == "" {
			return fmt.Errorf("postgres: invalid buy-to-close fill")
		}
	case domain.PositionIntentSellToClose:
		if order.Side != domain.OrderSideSell || input.PositionID == nil || strings.TrimSpace(input.ExitReason) == "" {
			return fmt.Errorf("postgres: invalid sell-to-close fill")
		}
	default:
		return fmt.Errorf("postgres: invalid option position intent")
	}
	return nil
}

func applyOptionStatusTx(ctx context.Context, tx pgx.Tx, input repository.OptionFillInput) error {
	order := input.Order
	tag, err := tx.Exec(ctx, `UPDATE orders SET external_id=COALESCE(NULLIF(external_id,''),NULLIF($1,'')),status=$2,submitted_at=COALESCE(submitted_at,$3) WHERE id=$4 AND account_id=$5 AND environment=$6 AND filled_quantity=$7 AND (status IN ('pending','submitted','partial') OR status=$2) AND (external_id IS NULL OR external_id='' OR $1='' OR external_id=$1)`, strings.TrimSpace(order.ExternalID), order.Status, order.SubmittedAt, order.ID, input.AccountID, input.Environment, input.FillQuantity)
	if err != nil {
		return fmt.Errorf("postgres: persist option recovery status: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("postgres: option recovery status changed concurrently")
	}
	if terminalOrderStatusPostgres(order.Status) {
		if _, err := tx.Exec(ctx, `UPDATE positions SET close_reservation_order_id=NULL WHERE account_id=$1 AND close_reservation_order_id=$2`, input.AccountID, order.ID); err != nil {
			return fmt.Errorf("postgres: release terminal option reservation: %w", err)
		}
	}
	return nil
}

func applyOptionFillTx(ctx context.Context, tx pgx.Tx, input repository.OptionFillInput) (repository.OptionFillResult, error) {
	order := input.Order
	var (
		persistedStrategyID         *uuid.UUID
		persistedAccountID          uuid.UUID
		persistedEnvironment        domain.AccountEnvironment
		persistedOriginType         string
		persistedOriginID           string
		persistedTicker             string
		persistedMarketType         domain.MarketType
		persistedSide               domain.OrderSide
		persistedStatus             domain.OrderStatus
		persistedQuantity           float64
		persistedAssetClass         domain.AssetClass
		persistedUnderlyingTicker   string
		persistedOptionType         *domain.OptionType
		persistedStrike             *float64
		persistedExpiry             *time.Time
		persistedContractMultiplier float64
		persistedPositionIntent     *domain.PositionIntent
		persistedLegGroupID         *uuid.UUID
		persistedFilledQuantity     float64
		persistedFilledAvgPrice     *float64
	)
	if err := tx.QueryRow(ctx, `SELECT strategy_id,account_id,environment,origin_type,origin_id,ticker,market_type,side,status,quantity::double precision,asset_class,
		COALESCE(underlying_ticker,''),option_type,strike::double precision,expiry,
		contract_multiplier::double precision,position_intent,leg_group_id,filled_quantity::double precision,filled_avg_price::double precision
		FROM orders WHERE id=$1 AND account_id=$2 FOR UPDATE`, order.ID, input.AccountID).Scan(
		&persistedStrategyID, &persistedAccountID, &persistedEnvironment, &persistedOriginType, &persistedOriginID, &persistedTicker, &persistedMarketType, &persistedSide, &persistedStatus,
		&persistedQuantity, &persistedAssetClass, &persistedUnderlyingTicker, &persistedOptionType,
		&persistedStrike, &persistedExpiry, &persistedContractMultiplier, &persistedPositionIntent, &persistedLegGroupID, &persistedFilledQuantity, &persistedFilledAvgPrice,
	); err != nil {
		return repository.OptionFillResult{}, fmt.Errorf("postgres: lock option order: %w", err)
	}
	switch persistedStatus {
	case domain.OrderStatusPending, domain.OrderStatusSubmitted, domain.OrderStatusPartial:
	case domain.OrderStatusCancelled, domain.OrderStatusRejected:
		if order.Status != persistedStatus {
			return repository.OptionFillResult{}, fmt.Errorf("postgres: terminal option order status changed")
		}
	default:
		return repository.OptionFillResult{}, fmt.Errorf("postgres: option order %s status %s not fill-compatible", order.ID, persistedStatus)
	}
	metadataMatches := persistedStrategyID != nil && *persistedStrategyID == *order.StrategyID && persistedAccountID == input.AccountID && persistedEnvironment == input.Environment && persistedOriginType == input.OriginType && persistedOriginID == input.OriginID &&
		persistedTicker == order.Ticker && persistedMarketType.Normalize() == domain.MarketTypeOptions &&
		persistedSide == order.Side && numeric8Equal(persistedQuantity, order.Quantity) && input.FillQuantity <= persistedQuantity &&
		persistedAssetClass == domain.AssetClassOption && persistedUnderlyingTicker == order.UnderlyingTicker &&
		persistedOptionType != nil && *persistedOptionType == *order.OptionType &&
		persistedStrike != nil && numeric8Equal(*persistedStrike, *order.Strike) &&
		persistedExpiry != nil && persistedExpiry.UTC().Equal(order.Expiry.UTC()) &&
		numeric8Equal(persistedContractMultiplier, order.ContractMultiplier) &&
		persistedPositionIntent != nil && *persistedPositionIntent == *order.PositionIntent &&
		((persistedLegGroupID == nil && order.LegGroupID == nil) || (persistedLegGroupID != nil && order.LegGroupID != nil && *persistedLegGroupID == *order.LegGroupID))
	if !metadataMatches {
		return repository.OptionFillResult{}, fmt.Errorf("postgres: persisted option order %s metadata does not match fill", order.ID)
	}
	if input.FillQuantity <= persistedFilledQuantity {
		return repository.OptionFillResult{}, fmt.Errorf("postgres: cumulative option fill %.8f does not advance persisted quantity %.8f", input.FillQuantity, persistedFilledQuantity)
	}
	deltaQuantity := input.FillQuantity - persistedFilledQuantity
	deltaPrice := input.FillPrice
	if persistedFilledQuantity > 0 && persistedFilledAvgPrice != nil {
		deltaPrice = (input.FillPrice*input.FillQuantity - *persistedFilledAvgPrice*persistedFilledQuantity) / deltaQuantity
	}
	var priorFee, priorPremium float64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(fee),0)::double precision,COALESCE(SUM(premium),0)::double precision FROM trades WHERE order_id=$1 AND account_id=$2`, order.ID, input.AccountID).Scan(&priorFee, &priorPremium); err != nil {
		return repository.OptionFillResult{}, fmt.Errorf("postgres: load prior option accounting: %w", err)
	}
	deltaFee, deltaPremium := input.Fee-priorFee, input.Premium-priorPremium
	if deltaPrice < 0 || deltaFee < 0 || deltaPremium < 0 || !numeric8Equal(deltaPremium, deltaPrice*deltaQuantity*order.ContractMultiplier) {
		return repository.OptionFillResult{}, fmt.Errorf("postgres: cumulative option accounting regressed")
	}
	filledAt := input.FilledAt.UTC()
	if _, err := tx.Exec(ctx, `UPDATE orders SET external_id=$1, broker=$2, submitted_at=$3,
		filled_quantity=$4, filled_avg_price=$5, status=$6, filled_at=$7 WHERE id=$8 AND account_id=$9`,
		nullString(order.ExternalID), nullString(order.Broker), order.SubmittedAt,
		input.FillQuantity, input.FillPrice, order.Status, filledAt, order.ID, input.AccountID,
	); err != nil {
		return repository.OptionFillResult{}, fmt.Errorf("postgres: update filled option order: %w", err)
	}

	var positionID uuid.UUID
	openClose := "open"
	if input.PositionID == nil {
		if order.OptionType == nil || order.Strike == nil || order.Expiry == nil || order.UnderlyingTicker == "" || order.ContractMultiplier <= 0 {
			return repository.OptionFillResult{}, fmt.Errorf("postgres: opening option fill lacks contract metadata")
		}
		positionSide := domain.PositionSideLong
		if *order.PositionIntent == domain.PositionIntentSellToOpen {
			positionSide = domain.PositionSideShort
		}
		err := tx.QueryRow(ctx, `SELECT p.id FROM positions p JOIN trades t ON t.position_id=p.id WHERE t.order_id=$1 AND t.account_id=$2 ORDER BY t.created_at LIMIT 1 FOR UPDATE OF p`, order.ID, input.AccountID).Scan(&positionID)
		if err == nil {
			var quantity, avgEntry float64
			if err := tx.QueryRow(ctx, `SELECT quantity::double precision,avg_entry::double precision FROM positions WHERE id=$1 FOR UPDATE`, positionID).Scan(&quantity, &avgEntry); err != nil {
				return repository.OptionFillResult{}, fmt.Errorf("postgres: reload option position: %w", err)
			}
			newQuantity := quantity + deltaQuantity
			newAverage := (quantity*avgEntry + deltaQuantity*deltaPrice) / newQuantity
			if _, err := tx.Exec(ctx, `UPDATE positions SET quantity=$1,avg_entry=$2,current_price=$3 WHERE id=$4`, newQuantity, newAverage, input.FillPrice, positionID); err != nil {
				return repository.OptionFillResult{}, fmt.Errorf("postgres: advance option position: %w", err)
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return repository.OptionFillResult{}, fmt.Errorf("postgres: find prior option position: %w", err)
		} else {
			positionID = uuid.New()
			var delta, gamma, theta, vega *float64
			if order.OptionGreeks != nil {
				delta, gamma, theta, vega = &order.OptionGreeks.Delta, &order.OptionGreeks.Gamma, &order.OptionGreeks.Theta, &order.OptionGreeks.Vega
			}
			if _, err := tx.Exec(ctx, `INSERT INTO positions
			(id,account_id,environment,origin_type,origin_id,strategy_id,ticker,side,quantity,avg_entry,opened_at,asset_class,underlying_ticker,option_type,strike,expiry,contract_multiplier,leg_group_id,delta,gamma,theta,vega)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)`,
				positionID, input.AccountID, input.Environment, input.OriginType, input.OriginID, order.StrategyID, order.Ticker, positionSide, deltaQuantity, deltaPrice, filledAt,
				domain.AssetClassOption, order.UnderlyingTicker, order.OptionType, order.Strike, order.Expiry,
				order.ContractMultiplier, order.LegGroupID, delta, gamma, theta, vega,
			); err != nil {
				return repository.OptionFillResult{}, fmt.Errorf("postgres: create option position: %w", err)
			}
		}
	} else {
		openClose = "close"
		positionID = *input.PositionID
		var (
			strategyID         *uuid.UUID
			ticker             string
			side               domain.PositionSide
			quantity           float64
			avgEntry           float64
			realizedPnL        float64
			contractMultiplier float64
			assetClass         domain.AssetClass
			closedAt           *time.Time
			underlyingTicker   string
			optionType         *domain.OptionType
			strike             *float64
			expiry             *time.Time
			legGroupID         *uuid.UUID
		)
		if err := tx.QueryRow(ctx, `SELECT strategy_id,ticker,side,quantity::double precision,avg_entry::double precision,
			COALESCE(realized_pnl,0)::double precision,COALESCE(NULLIF(contract_multiplier,0),100)::double precision,asset_class,closed_at,
			COALESCE(underlying_ticker,''),option_type,strike::double precision,expiry,leg_group_id
			FROM positions WHERE id=$1 AND account_id=$2 AND environment=$3 AND origin_type=$4 AND origin_id=$5 FOR UPDATE`, positionID, input.AccountID, input.Environment, input.OriginType, input.OriginID).Scan(
			&strategyID, &ticker, &side, &quantity, &avgEntry, &realizedPnL, &contractMultiplier, &assetClass, &closedAt,
			&underlyingTicker, &optionType, &strike, &expiry, &legGroupID,
		); err != nil {
			return repository.OptionFillResult{}, fmt.Errorf("postgres: lock option close position: %w", err)
		}
		contractMatches := optionType != nil && *optionType == *order.OptionType &&
			strike != nil && numeric8Equal(*strike, *order.Strike) &&
			expiry != nil && expiry.UTC().Equal(order.Expiry.UTC()) &&
			numeric8Equal(contractMultiplier, order.ContractMultiplier) &&
			((legGroupID == nil && order.LegGroupID == nil) || (legGroupID != nil && order.LegGroupID != nil && *legGroupID == *order.LegGroupID))
		if strategyID == nil || *strategyID != *order.StrategyID || ticker != order.Ticker || underlyingTicker != order.UnderlyingTicker || assetClass != domain.AssetClassOption || closedAt != nil || deltaQuantity > quantity || !contractMatches {
			return repository.OptionFillResult{}, fmt.Errorf("postgres: option close position does not match fill")
		}
		realizedDelta := (deltaPrice - avgEntry) * deltaQuantity * contractMultiplier
		if *order.PositionIntent == domain.PositionIntentBuyToClose {
			if side != domain.PositionSideShort {
				return repository.OptionFillResult{}, fmt.Errorf("postgres: buy-to-close requires a short option position")
			}
			realizedDelta = (avgEntry - deltaPrice) * deltaQuantity * contractMultiplier
		} else if side != domain.PositionSideLong {
			return repository.OptionFillResult{}, fmt.Errorf("postgres: sell-to-close requires a long option position")
		}
		remaining := quantity - deltaQuantity
		var closedAtValue *time.Time
		if numeric8Equal(remaining, 0) {
			remaining = 0
			closedAtValue = &filledAt
		}
		releaseReservation := terminalOrderStatusPostgres(order.Status)
		if tag, err := tx.Exec(ctx, `UPDATE positions SET quantity=$1,current_price=$2,realized_pnl=$3,
			unrealized_pnl=NULL,closed_at=$4,close_reservation_order_id=CASE WHEN $8 THEN NULL ELSE close_reservation_order_id END WHERE id=$5 AND account_id=$6 AND close_reservation_order_id=$7`, remaining, input.FillPrice, realizedPnL+realizedDelta-deltaFee, closedAtValue, positionID, input.AccountID, order.ID, releaseReservation); err != nil {
			return repository.OptionFillResult{}, fmt.Errorf("postgres: close option position: %w", err)
		} else if tag.RowsAffected() != 1 {
			return repository.OptionFillResult{}, fmt.Errorf("postgres: option close reservation is missing or belongs to another order")
		}
	}

	tradeID := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO trades
		(id,account_id,environment,origin_type,origin_id,external_id,order_id,position_id,ticker,side,quantity,price,fee,executed_at,created_at,asset_class,open_close,contract_multiplier,premium,exit_reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$14,$15,$16,$17,$18,$19)`,
		tradeID, input.AccountID, input.Environment, input.OriginType, input.OriginID, nullString(order.ExternalID), order.ID, positionID, order.Ticker, order.Side,
		deltaQuantity, deltaPrice, deltaFee, filledAt, domain.AssetClassOption, openClose,
		order.ContractMultiplier, deltaPremium, nullString(strings.TrimSpace(input.ExitReason)),
	); err != nil {
		return repository.OptionFillResult{}, fmt.Errorf("postgres: create option fill trade: %w", err)
	}
	key := input.IdempotencyKey
	if _, err := tx.Exec(ctx, `INSERT INTO financial_fill_idempotency
		(idempotency_key,account_id,environment,origin_type,origin_id,order_id,position_id,trade_id,fill_quantity,fill_price,cumulative_fee,cumulative_premium,cumulative_filled_at,cumulative_status,cumulative_exit_reason) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		key, input.AccountID, input.Environment, input.OriginType, input.OriginID, order.ID, positionID, tradeID, input.FillQuantity, input.FillPrice, input.Fee, input.Premium, filledAt, order.Status, strings.TrimSpace(input.ExitReason),
	); err != nil {
		return repository.OptionFillResult{}, fmt.Errorf("postgres: finalize option fill idempotency: %w", err)
	}
	return repository.OptionFillResult{OrderID: order.ID, PositionID: positionID, TradeID: tradeID}, nil
}

// SettleOptionPosition atomically closes one expired option position and
// creates its linked cash-settlement trade. The locked database row is the
// source of truth for quantity, side, entry price, and contract multiplier.
func (db *DB) SettleOptionPosition(ctx context.Context, input repository.OptionPositionSettlementInput) (repository.OptionPositionSettlementResult, error) {
	if input.IdempotencyKey == "" || input.AccountID == uuid.Nil || !input.Environment.IsValid() || strings.TrimSpace(input.OriginType) == "" || strings.TrimSpace(input.OriginID) == "" || input.PositionID == uuid.Nil || input.SettledAt.IsZero() || input.SettlementPrice < 0 || math.IsNaN(input.SettlementPrice) || math.IsInf(input.SettlementPrice, 0) {
		return repository.OptionPositionSettlementResult{}, fmt.Errorf("postgres: invalid option settlement input")
	}
	if (input.SettlementPrice == 0 && input.ExitReason != "expired_worthless") || (input.SettlementPrice > 0 && input.ExitReason != "exercise_cash_settled") {
		return repository.OptionPositionSettlementResult{}, fmt.Errorf("postgres: invalid option settlement reason")
	}

	tx, err := db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return repository.OptionPositionSettlementResult{}, fmt.Errorf("postgres: begin option settlement tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, financialLifecycleLockKey("option-settlement", input.PositionID.String())); err != nil {
		return repository.OptionPositionSettlementResult{}, fmt.Errorf("postgres: lock option settlement idempotency: %w", err)
	}
	var replayPositionID, replayTradeID, replayAccountID uuid.UUID
	var replayEnvironment domain.AccountEnvironment
	var replayOriginType, replayOriginID, replayReason string
	var replayPrice float64
	var replayAt time.Time
	err = tx.QueryRow(ctx, `SELECT position_id,trade_id,account_id,environment,origin_type,origin_id,settlement_price::double precision,settled_at,exit_reason FROM option_settlement_idempotency WHERE idempotency_key=$1 FOR UPDATE`, input.IdempotencyKey).Scan(&replayPositionID, &replayTradeID, &replayAccountID, &replayEnvironment, &replayOriginType, &replayOriginID, &replayPrice, &replayAt, &replayReason)
	if err == nil {
		if replayPositionID != input.PositionID || replayAccountID != input.AccountID || replayEnvironment != input.Environment || replayOriginType != input.OriginType || replayOriginID != input.OriginID || !numeric8Equal(replayPrice, input.SettlementPrice) || !replayAt.Equal(input.SettledAt.UTC()) || replayReason != input.ExitReason {
			return repository.OptionPositionSettlementResult{}, fmt.Errorf("postgres: option settlement idempotency mismatch")
		}
		if err := tx.Commit(ctx); err != nil {
			return repository.OptionPositionSettlementResult{}, fmt.Errorf("postgres: commit replayed option settlement: %w", err)
		}
		return repository.OptionPositionSettlementResult{PositionID: replayPositionID, TradeID: replayTradeID}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return repository.OptionPositionSettlementResult{}, fmt.Errorf("postgres: select option settlement idempotency: %w", err)
	}

	var (
		ticker             string
		side               domain.PositionSide
		quantity           float64
		avgEntry           float64
		realizedPnL        float64
		contractMultiplier float64
		closedAt           *time.Time
		assetClass         domain.AssetClass
		expiry             *time.Time
		closeReservationID *uuid.UUID
		accountID          uuid.UUID
		environment        domain.AccountEnvironment
	)
	if err := tx.QueryRow(ctx, `SELECT account_id,environment,ticker, side, quantity::double precision, avg_entry::double precision,
		COALESCE(realized_pnl, 0)::double precision, COALESCE(NULLIF(contract_multiplier, 0), 100)::double precision,
		closed_at, asset_class, expiry, close_reservation_order_id
		FROM positions WHERE id = $1 AND account_id=$2 AND environment=$3 FOR UPDATE`, input.PositionID, input.AccountID, input.Environment).Scan(
		&accountID, &environment, &ticker, &side, &quantity, &avgEntry, &realizedPnL, &contractMultiplier, &closedAt, &assetClass, &expiry, &closeReservationID,
	); err != nil {
		return repository.OptionPositionSettlementResult{}, fmt.Errorf("postgres: lock option settlement position: %w", err)
	}
	if accountID != input.AccountID || environment != input.Environment || assetClass != domain.AssetClassOption || closedAt != nil || quantity <= 0 || expiry == nil {
		return repository.OptionPositionSettlementResult{}, fmt.Errorf("postgres: option settlement position is not eligible")
	}
	if closeReservationID != nil {
		return repository.OptionPositionSettlementResult{}, fmt.Errorf("postgres: option settlement position has an active close reservation")
	}
	settledAt := input.SettledAt.UTC()
	settlementDay := time.Date(settledAt.Year(), settledAt.Month(), settledAt.Day(), 0, 0, 0, 0, time.UTC)
	expiryDay := time.Date(expiry.UTC().Year(), expiry.UTC().Month(), expiry.UTC().Day(), 0, 0, 0, 0, time.UTC)
	if expiryDay.After(settlementDay) {
		return repository.OptionPositionSettlementResult{}, fmt.Errorf("postgres: option settlement position has not expired")
	}

	realizedDelta := (input.SettlementPrice - avgEntry) * quantity * contractMultiplier
	tradeSide := domain.OrderSideSell
	if side == domain.PositionSideShort {
		realizedDelta = (avgEntry - input.SettlementPrice) * quantity * contractMultiplier
		tradeSide = domain.OrderSideBuy
	} else if side != domain.PositionSideLong {
		return repository.OptionPositionSettlementResult{}, fmt.Errorf("postgres: option settlement position has invalid side")
	}
	if _, err := tx.Exec(ctx, `UPDATE positions
		SET quantity = 0, current_price = $1, realized_pnl = $2, unrealized_pnl = NULL, closed_at = $3
		WHERE id = $4 AND account_id=$5`, input.SettlementPrice, realizedPnL+realizedDelta, settledAt, input.PositionID, input.AccountID); err != nil {
		return repository.OptionPositionSettlementResult{}, fmt.Errorf("postgres: close option settlement position: %w", err)
	}

	tradeID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(input.IdempotencyKey))
	if _, err := tx.Exec(ctx, `INSERT INTO trades
		(id,account_id,environment,origin_type,origin_id,position_id,ticker,side,quantity,price,executed_at,created_at,asset_class,open_close,contract_multiplier,premium,exit_reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11,$12,'close',$13,$14,$15)`,
		tradeID, input.AccountID, input.Environment, input.OriginType, input.OriginID, input.PositionID, ticker, tradeSide, quantity, input.SettlementPrice, settledAt,
		domain.AssetClassOption, contractMultiplier, input.SettlementPrice*quantity*contractMultiplier, input.ExitReason,
	); err != nil {
		return repository.OptionPositionSettlementResult{}, fmt.Errorf("postgres: create option settlement trade: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO option_settlement_idempotency(idempotency_key,account_id,environment,origin_type,origin_id,position_id,trade_id,settlement_price,settled_at,exit_reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, input.IdempotencyKey, input.AccountID, input.Environment, input.OriginType, input.OriginID, input.PositionID, tradeID, input.SettlementPrice, settledAt, input.ExitReason); err != nil {
		return repository.OptionPositionSettlementResult{}, fmt.Errorf("postgres: finalize option settlement idempotency: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return repository.OptionPositionSettlementResult{}, fmt.Errorf("postgres: commit option settlement: %w", err)
	}
	return repository.OptionPositionSettlementResult{PositionID: input.PositionID, TradeID: tradeID}, nil
}

func (db *DB) ResolveOptionSettlementCommit(ctx context.Context, input repository.OptionPositionSettlementInput) (repository.OptionPositionSettlementResult, bool, error) {
	var positionID, tradeID, accountID uuid.UUID
	var environment domain.AccountEnvironment
	var originType, originID, reason string
	var price float64
	var settledAt time.Time
	err := db.Pool.QueryRow(ctx, `SELECT position_id,trade_id,account_id,environment,origin_type,origin_id,settlement_price::double precision,settled_at,exit_reason FROM option_settlement_idempotency WHERE idempotency_key=$1`, input.IdempotencyKey).Scan(&positionID, &tradeID, &accountID, &environment, &originType, &originID, &price, &settledAt, &reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.OptionPositionSettlementResult{}, false, nil
	}
	if err != nil {
		return repository.OptionPositionSettlementResult{}, false, err
	}
	if positionID != input.PositionID || accountID != input.AccountID || environment != input.Environment || originType != input.OriginType || originID != input.OriginID || !numeric8Equal(price, input.SettlementPrice) || !settledAt.Equal(input.SettledAt.UTC()) || reason != input.ExitReason {
		return repository.OptionPositionSettlementResult{}, false, fmt.Errorf("postgres: option settlement commit identity mismatch")
	}
	return repository.OptionPositionSettlementResult{PositionID: positionID, TradeID: tradeID}, true, nil
}

func (db *DB) SettlePredictionDecision(ctx context.Context, input repository.PredictionDecisionSettlementInput) (repository.PredictionDecisionSettlementResult, error) {
	if input.Decision == nil || input.Decision.ID == uuid.Nil || input.IdempotencyKey == "" || input.ResolvedAt.IsZero() || math.IsNaN(input.Payout) || math.IsInf(input.Payout, 0) || input.Payout < 0 || input.Payout > 1 {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: invalid settlement input")
	}
	tx, err := db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: begin settlement tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	requestedAccount, requestedEnvironment := input.AccountID, input.Environment
	persisted := &domain.TradeDecision{ID: input.Decision.ID}
	if err := tx.QueryRow(ctx, `SELECT strategy_id,paper_order_id,account_id,environment,origin_type,origin_id,market_type,instrument_key,outcome,status FROM trade_decisions WHERE id=$1 FOR UPDATE`, persisted.ID).Scan(
		&persisted.StrategyID, &persisted.PaperOrderID, &persisted.AccountID, &persisted.Environment, &persisted.OriginType, &persisted.OriginID, &persisted.MarketType, &persisted.InstrumentKey, &persisted.Outcome, &persisted.Status,
	); err != nil {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: lock settlement decision: %w", err)
	}
	if requestedAccount != uuid.Nil && requestedAccount != persisted.AccountID || requestedEnvironment != "" && requestedEnvironment != persisted.Environment {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: settlement decision scope mismatch")
	}
	if persisted.AccountID == uuid.Nil || !persisted.Environment.IsValid() || persisted.StrategyID == nil || persisted.PaperOrderID == nil || strings.TrimSpace(persisted.OriginType) == "" || strings.TrimSpace(persisted.OriginID) == "" {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: persisted settlement decision lacks ownership")
	}
	held := strings.ToUpper(strings.TrimSpace(persisted.Outcome))
	if held != "YES" && held != "NO" || strings.TrimSpace(persisted.InstrumentKey) == "" {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: persisted settlement decision lacks linkage")
	}
	input.Decision = persisted
	input.AccountID, input.Environment, input.OriginType, input.OriginID = persisted.AccountID, persisted.Environment, persisted.OriginType, persisted.OriginID
	input.PositionTicker = strings.TrimSpace(persisted.InstrumentKey) + ":" + held
	var decisionID uuid.UUID
	var idempotencyKey string
	var positionID *uuid.UUID
	var tradeID uuid.UUID
	var replayEventID *uuid.UUID
	var payout float64
	var resolvedAt time.Time
	var createdAt time.Time
	var existingAccountID uuid.UUID
	var existingEnvironment domain.AccountEnvironment
	var existingOriginType, existingOriginID string
	if err := tx.QueryRow(ctx, `SELECT idempotency_key,decision_id,position_id,trade_id,replay_event_id,payout,resolved_at,created_at,account_id,environment,origin_type,origin_id FROM prediction_settlement_idempotency WHERE idempotency_key=$1 OR decision_id=$2 FOR UPDATE`, input.IdempotencyKey, input.Decision.ID).Scan(&idempotencyKey, &decisionID, &positionID, &tradeID, &replayEventID, &payout, &resolvedAt, &createdAt, &existingAccountID, &existingEnvironment, &existingOriginType, &existingOriginID); err == nil {
		if idempotencyKey != input.IdempotencyKey || decisionID != input.Decision.ID || existingAccountID != input.AccountID || existingEnvironment != input.Environment || existingOriginType != input.OriginType || existingOriginID != input.OriginID || math.IsNaN(payout) || math.IsInf(payout, 0) || !numeric8Equal(payout, input.Payout) || !resolvedAt.UTC().Equal(input.ResolvedAt.UTC()) {
			return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: idempotency mismatch for decision %s", input.Decision.ID)
		}
		if err := tx.Commit(ctx); err != nil {
			return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: commit replayed settlement: %w", err)
		}
		return repository.PredictionDecisionSettlementResult{DecisionID: decisionID, PositionID: positionID, TradeID: tradeID, ReplayEventID: replayEventID, CreatedAt: createdAt, Replayed: true}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: select settlement idempotency: %w", err)
	}
	if persisted.Status != domain.TradeDecisionStatusPaper {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: settlement decision %s is not open", persisted.ID)
	}
	rows, err := tx.Query(ctx, `SELECT p.id, p.strategy_id, p.quantity::double precision, p.avg_entry::double precision, p.realized_pnl::double precision, p.close_reservation_order_id FROM positions p INNER JOIN trades t ON t.position_id = p.id AND t.order_id = $1 AND t.account_id=$2 WHERE p.account_id=$2 AND p.environment=$3 AND p.origin_type=$4 AND p.origin_id=$5 AND p.ticker=$6 AND p.closed_at IS NULL AND p.quantity > 0 FOR UPDATE OF p`, input.Decision.PaperOrderID, input.AccountID, input.Environment, input.OriginType, input.OriginID, input.PositionTicker)
	if err != nil {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: lock settlement position: %w", err)
	}
	defer rows.Close()
	var positions []domain.Position
	var reservations []*uuid.UUID
	for rows.Next() {
		var position domain.Position
		var strategyID uuid.UUID
		var reservation *uuid.UUID
		if err := rows.Scan(&position.ID, &strategyID, &position.Quantity, &position.AvgEntry, &position.RealizedPnL, &reservation); err != nil {
			return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: scan settlement position: %w", err)
		}
		position.StrategyID = &strategyID
		positions = append(positions, position)
		reservations = append(reservations, reservation)
	}
	if err := rows.Err(); err != nil {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: iterate settlement positions: %w", err)
	}
	if len(positions) != 1 {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: expected exactly one open position for decision %s, got %d", input.Decision.ID, len(positions))
	}
	position := positions[0]
	if position.StrategyID == nil || input.Decision.StrategyID == nil || *position.StrategyID != *input.Decision.StrategyID {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: settlement position strategy linkage mismatch")
	}
	if reservations[0] != nil {
		var exitStatus domain.OrderStatus
		var externalID string
		var filledQuantity, accountedQuantity float64
		if err := tx.QueryRow(ctx, `SELECT status,COALESCE(external_id,''),filled_quantity::double precision,COALESCE((SELECT SUM(t.quantity) FROM trades t WHERE t.order_id=orders.id AND t.account_id=orders.account_id),0)::double precision FROM orders WHERE id=$1 AND account_id=$2 AND environment=$3 FOR UPDATE`, *reservations[0], input.AccountID, input.Environment).Scan(&exitStatus, &externalID, &filledQuantity, &accountedQuantity); err != nil {
			return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: inspect active prediction close: %w", err)
		}
		verifiedTerminal := (exitStatus == domain.OrderStatusCancelled || exitStatus == domain.OrderStatusRejected) && filledQuantity <= accountedQuantity
		if !verifiedTerminal && (exitStatus != domain.OrderStatusPending || strings.TrimSpace(externalID) != "") {
			return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: active effectful prediction close requires broker cancellation and verified terminal status")
		}
		if !verifiedTerminal {
			tag, err := tx.Exec(ctx, `UPDATE orders SET status='rejected' WHERE id=$1 AND account_id=$2 AND environment=$3 AND status='pending' AND (external_id IS NULL OR external_id='') AND filled_quantity=0`, *reservations[0], input.AccountID, input.Environment)
			if err != nil {
				return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: terminalize active prediction close: %w", err)
			}
			if tag.RowsAffected() != 1 {
				return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: active prediction close cannot be safely terminalized")
			}
		}
	}
	quantity := position.Quantity
	position.Quantity = 0
	position.CurrentPrice = &input.Payout
	position.RealizedPnL += (input.Payout - position.AvgEntry) * quantity
	position.UnrealizedPnL = nil
	closedAt := input.ResolvedAt.UTC()
	position.ClosedAt = &closedAt
	if _, err := tx.Exec(ctx, `UPDATE positions SET quantity = 0, current_price = $1, realized_pnl = $2, unrealized_pnl = NULL, closed_at = $3, close_reservation_order_id=NULL WHERE id = $4 AND account_id=$5`, input.Payout, position.RealizedPnL, closedAt, position.ID, input.AccountID); err != nil {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: update position: %w", err)
	}
	tradeID = uuid.New()
	if err := tx.QueryRow(ctx, `INSERT INTO trades (id,account_id,environment,origin_type,origin_id,order_id,position_id,ticker,side,quantity,price,executed_at,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12) RETURNING id`, tradeID, input.AccountID, input.Environment, input.OriginType, input.OriginID, input.Decision.PaperOrderID, position.ID, input.PositionTicker, domain.OrderSideSell, quantity, input.Payout, closedAt).Scan(&tradeID); err != nil {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: insert payout trade: %w", err)
	}
	if tag, err := tx.Exec(ctx, `UPDATE trade_decisions SET status = $2, updated_at = NOW() WHERE id = $1 AND account_id=$4 AND status = $3`, input.Decision.ID, domain.TradeDecisionStatusClosed, domain.TradeDecisionStatusPaper, input.AccountID); err != nil {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: update decision: %w", err)
	} else if tag.RowsAffected() != 1 {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: update decision: expected 1 row, got %d", tag.RowsAffected())
	}
	replayEventID = func() *uuid.UUID { id := uuid.New(); return &id }()
	payload, _ := json.Marshal(map[string]any{"decision_id": input.Decision.ID, "position_id": position.ID, "trade_id": tradeID, "payout": input.Payout, "resolved_at": closedAt, "position_ticker": input.PositionTicker})
	if err := tx.QueryRow(ctx, `INSERT INTO replay_events (id,account_id,environment,origin_type,origin_id,trade_decision_id,event_type,source,payload,occurred_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`, *replayEventID, input.AccountID, input.Environment, input.OriginType, input.OriginID, input.Decision.ID, domain.ReplayEventTypeOutcomeResolved, "prediction_settler", payload, closedAt).Scan(replayEventID); err != nil {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: insert replay event: %w", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO prediction_settlement_idempotency(idempotency_key,account_id,environment,origin_type,origin_id,decision_id,position_id,trade_id,replay_event_id,payout,resolved_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING created_at`, input.IdempotencyKey, input.AccountID, input.Environment, input.OriginType, input.OriginID, input.Decision.ID, position.ID, tradeID, replayEventID, input.Payout, input.ResolvedAt.UTC()).Scan(&createdAt); err != nil {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: finalize settlement idempotency: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: commit settlement: %w", err)
	}
	return repository.PredictionDecisionSettlementResult{DecisionID: input.Decision.ID, PositionID: &position.ID, TradeID: tradeID, ReplayEventID: replayEventID, CreatedAt: createdAt}, nil
}

func (db *DB) RecordOptionSettlementSyncFailure(ctx context.Context, input repository.OptionPositionSettlementInput, syncErr error) error {
	if input.PositionID == uuid.Nil || input.AccountID == uuid.Nil || !input.Environment.IsValid() || strings.TrimSpace(input.OriginType) == "" || strings.TrimSpace(input.OriginID) == "" || input.SettledAt.IsZero() || syncErr == nil {
		return fmt.Errorf("postgres: invalid option broker sync retry evidence")
	}
	tag, err := db.Pool.Exec(ctx, `INSERT INTO option_broker_sync_retries(position_id,account_id,environment,origin_type,origin_id,settlement_price,settled_at,last_error,status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'retry') ON CONFLICT(position_id) DO UPDATE SET last_error=EXCLUDED.last_error,status='retry',updated_at=NOW() WHERE option_broker_sync_retries.account_id=EXCLUDED.account_id AND option_broker_sync_retries.environment=EXCLUDED.environment AND option_broker_sync_retries.origin_type=EXCLUDED.origin_type AND option_broker_sync_retries.origin_id=EXCLUDED.origin_id AND option_broker_sync_retries.settlement_price=EXCLUDED.settlement_price AND option_broker_sync_retries.settled_at=EXCLUDED.settled_at`, input.PositionID, input.AccountID, input.Environment, input.OriginType, input.OriginID, input.SettlementPrice, input.SettledAt.UTC(), syncErr.Error())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("postgres: conflicting option broker sync retry evidence")
	}
	return nil
}

func (db *DB) ResolveOptionSettlementSyncRetries(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment) error {
	if accountID == uuid.Nil || !environment.IsValid() {
		return fmt.Errorf("postgres: invalid option broker sync retry scope")
	}
	_, err := db.Pool.Exec(ctx, `UPDATE option_broker_sync_retries SET status='resolved',updated_at=NOW() WHERE account_id=$1 AND environment=$2 AND status='retry'`, accountID, environment)
	return err
}

func (db *DB) HasOptionSettlementSyncRetries(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment) (bool, error) {
	if accountID == uuid.Nil || !environment.IsValid() {
		return false, fmt.Errorf("postgres: invalid option broker sync retry scope")
	}
	var pending bool
	if err := db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM option_broker_sync_retries WHERE account_id=$1 AND environment=$2 AND status='retry')`, accountID, environment).Scan(&pending); err != nil {
		return false, err
	}
	return pending, nil
}

func numeric8Equal(left, right float64) bool {
	return math.Round(left*1e8) == math.Round(right*1e8)
}

func terminalOrderStatusPostgres(status domain.OrderStatus) bool {
	return status == domain.OrderStatusFilled || status == domain.OrderStatusCancelled || status == domain.OrderStatusRejected
}

func financialLifecycleLockKey(namespace, identity string) int64 {
	hash := sha256.Sum256([]byte(namespace + "|" + identity))
	return int64(binary.BigEndian.Uint64(hash[:8]) &^ uint64(1<<63))
}
