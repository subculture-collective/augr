package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/lifecycle"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

const acceptedEconomicNormalizerVersion = "accepted-economic-planner-v1"

// AcceptedEconomicPlanner interprets an accepted compatibility mutation only
// when the same order was routed through the durable common lifecycle first.
// It loads immutable canonical reference facts; it never guesses an
// instrument, contract, account, or route from a legacy ticker.
type AcceptedEconomicPlanner struct{ pool *pgxpool.Pool }

var (
	_ execution.AcceptedEconomicPlanner         = (*AcceptedEconomicPlanner)(nil)
	_ execution.PositionExecutionScopeResolver  = (*AcceptedEconomicPlanner)(nil)
	_ execution.AcceptedOrderPreparationChecker = (*AcceptedEconomicPlanner)(nil)
)

func NewAcceptedEconomicPlanner(pool *pgxpool.Pool) *AcceptedEconomicPlanner {
	return &AcceptedEconomicPlanner{pool: pool}
}

func (planner *AcceptedEconomicPlanner) RequireAcceptedOrderPrepared(ctx context.Context, scope execution.ExecutionScope, order *domain.Order) error {
	if order == nil || order.ID == uuid.Nil {
		return fmt.Errorf("postgres: accepted order preparation requires an order identity")
	}
	current, _, _, _, err := planner.loadRoutedContext(ctx, scope, order.ID)
	if err != nil {
		return err
	}
	if current.Order == nil || current.Order.ID != order.ID || current.Order.ClientOrderID != order.ClientOrderID {
		return fmt.Errorf("postgres: accepted order differs from canonical routed command")
	}
	return nil
}

func (planner *AcceptedEconomicPlanner) PlanAcceptedOrderFill(ctx context.Context, scope execution.ExecutionScope, mutation repository.OrderFillInput) (execution.AcceptedFillInput, error) {
	if planner == nil || planner.pool == nil || mutation.Order == nil || mutation.Trade == nil {
		return execution.AcceptedFillInput{}, fmt.Errorf("postgres: accepted order fill planner requires runtime pool, order, and trade")
	}
	return planner.planFill(ctx, scope, mutation, nil)
}

func (planner *AcceptedEconomicPlanner) PlanAcceptedOptionFills(ctx context.Context, scope execution.ExecutionScope, mutations []repository.OptionFillInput) ([]execution.AcceptedFillInput, error) {
	if planner == nil || planner.pool == nil || len(mutations) == 0 {
		return nil, fmt.Errorf("postgres: accepted option fill planner requires runtime pool and fills")
	}
	result := make([]execution.AcceptedFillInput, len(mutations))
	for index := range mutations {
		mutation := mutations[index]
		if mutation.Order == nil || mutation.StatusOnly {
			return nil, fmt.Errorf("postgres: accepted option fill %d requires an economic order mutation", index)
		}
		orderMutation := repository.OrderFillInput{
			IdempotencyKey: mutation.IdempotencyKey,
			Order:          mutation.Order,
			FillIntent:     repository.OrderFillIntent{Side: mutation.Order.Side, Quantity: mutation.FillQuantity, ExecutionPrice: mutation.FillPrice},
			Now:            mutation.FilledAt,
		}
		planned, err := planner.planFill(ctx, scope, orderMutation, &mutation)
		if err != nil {
			return nil, fmt.Errorf("postgres: plan accepted option fill %d: %w", index, err)
		}
		result[index] = planned
	}
	return result, nil
}

func (planner *AcceptedEconomicPlanner) planFill(ctx context.Context, scope execution.ExecutionScope, mutation repository.OrderFillInput, optionMutation *repository.OptionFillInput) (execution.AcceptedFillInput, error) {
	if mutation.Order == nil || mutation.Order.ID == uuid.Nil || mutation.FillIntent.Quantity <= 0 || mutation.FillIntent.ExecutionPrice <= 0 || mutation.Now.IsZero() {
		return execution.AcceptedFillInput{}, fmt.Errorf("accepted fill mutation is incomplete")
	}
	current, account, canonicalInstrument, contract, err := planner.loadRoutedContext(ctx, scope, mutation.Order.ID)
	if err != nil {
		return execution.AcceptedFillInput{}, err
	}
	if current.Order == nil || current.Order.ID != mutation.Order.ID {
		return execution.AcceptedFillInput{}, fmt.Errorf("canonical routed order does not match accepted compatibility order")
	}
	raw, err := json.Marshal(struct {
		IdempotencyKey string             `json:"idempotency_key"`
		OrderID        uuid.UUID          `json:"order_id"`
		ExternalID     string             `json:"external_id,omitempty"`
		Status         domain.OrderStatus `json:"status"`
		Quantity       float64            `json:"quantity"`
		Price          float64            `json:"price"`
		FilledAt       time.Time          `json:"filled_at"`
	}{mutation.IdempotencyKey, mutation.Order.ID, mutation.Order.ExternalID, mutation.Order.Status, mutation.FillIntent.Quantity, mutation.FillIntent.ExecutionPrice, mutation.Now.UTC().Truncate(time.Microsecond)})
	if err != nil {
		return execution.AcceptedFillInput{}, fmt.Errorf("marshal accepted fill evidence: %w", err)
	}
	venue := strings.ToLower(strings.TrimSpace(current.Order.Venue))
	if venue == "" {
		return execution.AcceptedFillInput{}, fmt.Errorf("canonical routed order venue is required")
	}
	observedAt := mutation.Now.UTC().Truncate(time.Microsecond)
	fillQuantity, fillPrice, err := acceptedFillIncrement(current, mutation.FillIntent.Quantity, mutation.FillIntent.ExecutionPrice)
	if err != nil {
		return execution.AcceptedFillInput{}, err
	}
	source, err := ledger.NewEconomicSourceEvent(ledger.EconomicSourceEventInput{
		AccountID: scope.AccountID(), Source: venue, SourceNamespace: "fills/" + venue + "/accepted",
		SourceEventID: mutation.IdempotencyKey, SourceRevision: decimal.NewFromFloat(mutation.FillIntent.Quantity).String(),
		ObservedAt: observedAt, RawPayload: raw, CreatedAt: observedAt,
	})
	if err != nil {
		return execution.AcceptedFillInput{}, fmt.Errorf("construct accepted fill source event: %w", err)
	}
	side := ledger.FillSideBuy
	if current.Order.Side == lifecycle.SideSell {
		side = ledger.FillSideSell
	}
	fillID := lifecycle.FillID(current.Order.ID, source.ID)
	normalization, err := ledger.NewFillEconomicNormalization(ledger.FillEconomicEventInput{
		Base:       ledger.EconomicNormalizationBaseInput{SourceEvent: source, Account: &account, NormalizerVersion: acceptedEconomicNormalizerVersion, ExecutionOriginType: current.Intent.OriginType, ExecutionOriginID: current.Intent.OriginID, ReferenceType: "execution_fill", ReferenceID: fillID.String(), EffectiveAt: observedAt},
		Instrument: canonicalInstrument, VenueContract: contract, Side: side,
		Quantity: fillQuantity, Price: fillPrice,
	})
	if err != nil {
		return execution.AcceptedFillInput{}, fmt.Errorf("normalize accepted fill: %w", err)
	}
	transition, err := lifecycle.RecordFill(current, lifecycle.FillInput{
		Normalization: normalization, ExternalOrderID: strings.TrimSpace(mutation.Order.ExternalID),
		Event:     lifecycle.EventInput{Source: venue, SourceNamespace: source.SourceNamespace, SourceEventID: source.SourceEventID, SourceRevision: source.SourceRevision, SourceAt: observedAt, ReceivedAt: observedAt, Actor: "accepted-economic-planner", ReasonCode: "authoritative_fill_accepted", Evidence: raw},
		CreatedAt: observedAt,
	})
	if err != nil {
		return execution.AcceptedFillInput{}, fmt.Errorf("record accepted lifecycle fill: %w", err)
	}
	input := execution.AcceptedFillInput{Scope: scope, Mutation: mutation, OptionMutation: optionMutation, PriorLifecycle: current, Transition: transition, AcceptedFill: transition.Fill, SourceEvent: source, Instrument: &canonicalInstrument, VenueContract: &contract, Normalization: normalization, LedgerTransaction: normalization.Transaction}
	if optionMutation != nil {
		input.Mutation = repository.OrderFillInput{}
	}
	if err := input.Validate(); err != nil {
		return execution.AcceptedFillInput{}, err
	}
	return input, nil
}

// acceptedFillIncrement converts the compatibility model's cumulative filled
// quantity and average price into the one incremental fill represented by the
// next immutable lifecycle event. Prior lifecycle fills are authoritative;
// deriving from mutable order fields would permit a retry or concurrent update
// to double-post quantity or notional to the ledger.
func acceptedFillIncrement(current *lifecycle.Aggregate, cumulativeQuantity, cumulativeAveragePrice float64) (decimal.Decimal, decimal.Decimal, error) {
	if current == nil || cumulativeQuantity <= 0 || cumulativeAveragePrice <= 0 {
		return decimal.Zero, decimal.Zero, fmt.Errorf("accepted fill cumulative quantity and average price are required")
	}
	cumulative := decimal.NewFromFloat(cumulativeQuantity)
	average := decimal.NewFromFloat(cumulativeAveragePrice)
	priorQuantity := decimal.Zero
	priorNotional := decimal.Zero
	for index := range current.Fills {
		fill := current.Fills[index]
		priorQuantity = priorQuantity.Add(fill.Quantity)
		priorNotional = priorNotional.Add(fill.Quantity.Mul(fill.Price))
	}
	increment := cumulative.Sub(priorQuantity)
	if !increment.IsPositive() {
		return decimal.Zero, decimal.Zero, fmt.Errorf("accepted fill cumulative quantity %s does not advance lifecycle quantity %s", cumulative, priorQuantity)
	}
	incrementNotional := cumulative.Mul(average).Sub(priorNotional)
	if !incrementNotional.IsPositive() {
		return decimal.Zero, decimal.Zero, fmt.Errorf("accepted fill cumulative notional does not advance lifecycle notional")
	}
	price := incrementNotional.Div(increment)
	if !price.IsPositive() {
		return decimal.Zero, decimal.Zero, fmt.Errorf("accepted fill incremental price is invalid")
	}
	return increment, price, nil
}

func (planner *AcceptedEconomicPlanner) PlanAcceptedPredictionSettlement(ctx context.Context, scope execution.ExecutionScope, mutation repository.PredictionDecisionSettlementInput) (execution.AcceptedPredictionSettlementInput, error) {
	if planner == nil || planner.pool == nil || mutation.Decision == nil || mutation.Decision.PaperOrderID == nil || mutation.ResolvedAt.IsZero() {
		return execution.AcceptedPredictionSettlementInput{}, fmt.Errorf("postgres: prediction settlement planner requires runtime pool, decision, order, and resolution time")
	}
	current, account, canonicalInstrument, contract, err := planner.loadRoutedContext(ctx, scope, *mutation.Decision.PaperOrderID)
	if err != nil {
		return execution.AcceptedPredictionSettlementInput{}, err
	}
	evidence := mutation.Resolution
	if evidence.Source == "" || evidence.SourceNamespace == "" || evidence.SourceEventID == "" || evidence.ObservedAt.IsZero() || len(evidence.RawPayload) == 0 {
		return execution.AcceptedPredictionSettlementInput{}, fmt.Errorf("exact prediction resolution evidence is required")
	}
	source, err := ledger.NewEconomicSourceEvent(ledger.EconomicSourceEventInput{AccountID: scope.AccountID(), Source: evidence.Source, SourceNamespace: evidence.SourceNamespace, SourceEventID: evidence.SourceEventID, SourceRevision: evidence.SourceRevision, ObservedAt: evidence.ObservedAt, RawPayload: evidence.RawPayload, CreatedAt: evidence.ObservedAt})
	if err != nil {
		return execution.AcceptedPredictionSettlementInput{}, fmt.Errorf("construct prediction resolution source event: %w", err)
	}
	quantity, err := planner.loadSettlementQuantity(ctx, scope, mutation.PositionTicker)
	if err != nil {
		return execution.AcceptedPredictionSettlementInput{}, err
	}
	normalization, err := ledger.NewCashSettlementEconomicNormalization(ledger.CashSettlementEconomicEventInput{
		Base: ledger.EconomicNormalizationBaseInput{SourceEvent: source, Account: &account, NormalizerVersion: acceptedEconomicNormalizerVersion, ExecutionOriginType: current.Intent.OriginType, ExecutionOriginID: current.Intent.OriginID, ReferenceType: "prediction_settlement", ReferenceID: mutation.Decision.ID.String(), EffectiveAt: mutation.ResolvedAt},
		Kind: ledger.CashSettlementPrediction, Instrument: canonicalInstrument, VenueContract: contract,
		PositionQuantity: quantity, SettlementPrice: decimal.NewFromFloat(mutation.Payout),
	})
	if err != nil {
		return execution.AcceptedPredictionSettlementInput{}, fmt.Errorf("normalize prediction settlement: %w", err)
	}
	input := execution.AcceptedPredictionSettlementInput{Scope: scope, Mutation: mutation, SourceEvent: source, Instrument: &canonicalInstrument, VenueContract: &contract, Normalization: normalization, LedgerTransaction: normalization.Transaction}
	if err := input.Validate(); err != nil {
		return execution.AcceptedPredictionSettlementInput{}, err
	}
	return input, nil
}

func (planner *AcceptedEconomicPlanner) loadRoutedContext(ctx context.Context, scope execution.ExecutionScope, orderID uuid.UUID) (*lifecycle.Aggregate, domain.Account, instrument.Instrument, instrument.VenueContract, error) {
	var intentID uuid.UUID
	err := planner.pool.QueryRow(ctx, `SELECT intent_id FROM execution_orders WHERE account_id=$1 AND id=$2`, scope.AccountID(), orderID).Scan(&intentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.Account{}, instrument.Instrument{}, instrument.VenueContract{}, fmt.Errorf("canonical routed order %s is not prepared: %w", orderID, repository.ErrNotFound)
	}
	if err != nil {
		return nil, domain.Account{}, instrument.Instrument{}, instrument.VenueContract{}, fmt.Errorf("load canonical routed order: %w", err)
	}
	current, err := NewExecutionLifecycleRepo(planner.pool).GetExecutionLifecycle(ctx, scope.AccountID(), intentID)
	if err != nil {
		return nil, domain.Account{}, instrument.Instrument{}, instrument.VenueContract{}, err
	}
	if current.Order == nil || current.Intent.Environment != scope.Environment() {
		return nil, domain.Account{}, instrument.Instrument{}, instrument.VenueContract{}, fmt.Errorf("canonical routed lifecycle scope mismatch")
	}
	originType, originID := scope.Origin()
	if current.Intent.OriginType != originType || current.Intent.OriginID != originID || current.Intent.CopyOriginRebalanceRunID != scope.CopyOriginRunID() {
		return nil, domain.Account{}, instrument.Instrument{}, instrument.VenueContract{}, fmt.Errorf("canonical routed lifecycle origin mismatch")
	}
	account, err := NewAccountRepo(planner.pool).GetByID(ctx, scope.AccountID())
	if err != nil {
		return nil, domain.Account{}, instrument.Instrument{}, instrument.VenueContract{}, err
	}
	canonicalInstrument, err := NewInstrumentRepo(planner.pool).GetInstrumentByID(ctx, current.Intent.InstrumentID)
	if err != nil {
		return nil, domain.Account{}, instrument.Instrument{}, instrument.VenueContract{}, err
	}
	contract, err := NewInstrumentRepo(planner.pool).getVenueContractByID(ctx, current.Order.VenueContractID)
	if err != nil {
		return nil, domain.Account{}, instrument.Instrument{}, instrument.VenueContract{}, err
	}
	return current, *account, *canonicalInstrument, *contract, nil
}

func (planner *AcceptedEconomicPlanner) loadSettlementQuantity(ctx context.Context, scope execution.ExecutionScope, ticker string) (decimal.Decimal, error) {
	originType, originID := scope.Origin()
	var quantity float64
	var side domain.PositionSide
	err := planner.pool.QueryRow(ctx, `SELECT quantity,side FROM positions WHERE account_id=$1 AND environment=$2 AND origin_type=$3 AND origin_id=$4 AND ticker=$5 AND closed_at IS NULL ORDER BY opened_at,id LIMIT 1`, scope.AccountID(), scope.Environment(), string(originType), originID, strings.TrimSpace(ticker)).Scan(&quantity, &side)
	if errors.Is(err, pgx.ErrNoRows) {
		return decimal.Zero, repository.ErrNotFound
	}
	if err != nil {
		return decimal.Zero, fmt.Errorf("load prediction settlement position: %w", err)
	}
	result := decimal.NewFromFloat(quantity)
	if side == domain.PositionSideShort {
		result = result.Neg()
	}
	return result, nil
}

func (planner *AcceptedEconomicPlanner) ResolvePositionExecutionScope(ctx context.Context, position domain.Position) (execution.ExecutionScope, error) {
	if planner == nil || planner.pool == nil || position.ID == uuid.Nil {
		return execution.ExecutionScope{}, fmt.Errorf("postgres: position scope resolver requires runtime pool and position")
	}
	var order domain.Order
	var pipelineRunID *uuid.UUID
	var pipelineTradeDate *time.Time
	var strategyID *uuid.UUID
	err := planner.pool.QueryRow(ctx, `SELECT o.account_id,o.environment,o.origin_type,o.origin_id,o.pipeline_run_id,o.pipeline_run_trade_date,o.copy_origin_rebalance_run_id,o.strategy_id
		FROM trades t JOIN orders o ON o.id=t.order_id
		WHERE t.account_id=$1 AND t.position_id=$2 ORDER BY t.executed_at,t.id LIMIT 1`, position.AccountID, position.ID).Scan(&order.AccountID, &order.Environment, &order.OriginType, &order.OriginID, &pipelineRunID, &pipelineTradeDate, &order.CopyOriginRebalanceRunID, &strategyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return execution.ExecutionScope{}, repository.ErrNotFound
	}
	if err != nil {
		return execution.ExecutionScope{}, fmt.Errorf("postgres: resolve position execution scope: %w", err)
	}
	order.PipelineRunID, order.PipelineRunTradeDate, order.StrategyID = pipelineRunID, pipelineTradeDate, strategyID
	return execution.ExecutionScopeFromOrder(order)
}
