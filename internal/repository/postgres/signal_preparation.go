package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/economicid"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/lifecycle"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// SignalPreparation bridges one scoped signal to persisted lifecycle facts.
// Construct one per signal with already persisted canonical reference evidence.
// It never discovers an instrument by guessing from a ticker.
type SignalPreparation struct {
	mu       sync.Mutex
	repo     *ExecutionLifecycleRepo
	ticker   string
	proposal lifecycle.ProposeInput
	route    lifecycle.RouteInput
	resolved *ExecutionOrderPreparation
	orderID  uuid.UUID
	now      func() time.Time
}

// NewSignalPreparation requires the evidence owner to supply the exact ticker
// binding, proposal evidence, and dated venue/quote/policy facts.
func NewSignalPreparation(repo *ExecutionLifecycleRepo, ticker string, proposal lifecycle.ProposeInput, route lifecycle.RouteInput) *SignalPreparation {
	return &SignalPreparation{repo: repo, ticker: ticker, proposal: proposal, route: route, now: time.Now}
}

// Resolve rounds down before risk admission and binds the immutable run scope.
func (p *SignalPreparation) Resolve(ctx context.Context, scope execution.ExecutionScope, signal execution.FinalSignal, plan execution.TradingPlan, requested float64) (uuid.UUID, float64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.repo == nil || p.repo.pool == nil || p.ticker == "" || plan.Ticker != p.ticker ||
		math.IsNaN(requested) || math.IsInf(requested, 0) || requested <= 0 {
		return uuid.Nil, 0, fmt.Errorf("signal preparation requires exact ticker binding and finite positive sizing")
	}
	origin, originID := scope.Origin()
	if p.proposal.Account.ID != scope.AccountID() || p.proposal.Account.Environment != scope.Environment() ||
		p.proposal.Instrument.ID != p.route.Instrument.ID || p.route.VenueContract.InstrumentID != p.route.Instrument.ID {
		return uuid.Nil, 0, fmt.Errorf("signal preparation reference scope mismatch")
	}
	lot := p.route.VenueContract.LotSize
	if !lot.IsPositive() {
		return uuid.Nil, 0, fmt.Errorf("signal preparation requires a positive venue lot")
	}
	lots, _ := decimal.NewFromFloat(requested).QuoRem(lot, 0)
	quantity := lots.Mul(lot)
	if !quantity.IsPositive() || !quantity.Equal(quantity.Truncate(12)) || !decimal.NewFromFloat(quantity.InexactFloat64()).Equal(quantity) {
		return uuid.Nil, 0, fmt.Errorf("signal preparation quantity cannot cross the exact compatibility boundary")
	}
	delta := quantity
	if signal.Signal == domain.PipelineSignalSell {
		delta = delta.Neg()
	} else if signal.Signal != domain.PipelineSignalBuy {
		return uuid.Nil, 0, fmt.Errorf("signal preparation requires buy or sell")
	}
	proposal, route := p.proposal, p.route
	proposal.OriginType, proposal.OriginID, proposal.CopyOriginRebalanceRunID = origin, originID, scope.CopyOriginRunID()
	proposal.StrategyVersionID = ""
	if origin == ledger.ExecutionOriginStrategyVersion {
		proposal.StrategyVersionID = originID
	}
	proposal.DesiredQuantityDelta = delta
	route.LimitPrice, route.StopPrice = nil, nil
	switch plan.EntryType {
	case "", "market":
		route.OrderType = lifecycle.OrderMarket
	case "limit":
		if math.IsNaN(plan.EntryPrice) || math.IsInf(plan.EntryPrice, 0) || plan.EntryPrice <= 0 {
			return uuid.Nil, 0, fmt.Errorf("signal preparation requires a finite limit price")
		}
		price := decimal.NewFromFloat(plan.EntryPrice)
		route.OrderType, route.LimitPrice = lifecycle.OrderLimit, &price
	default:
		return uuid.Nil, 0, fmt.Errorf("signal preparation does not infer entry stop triggers")
	}
	run, hasRun := scope.PipelineRun()
	var runBinding *domain.PipelineRunRef
	if hasRun {
		runBinding = &run
	}
	metadata, err := json.Marshal(struct {
		Version  string                 `json:"version"`
		Run      *domain.PipelineRunRef `json:"pipeline_run,omitempty"`
		Ticker   string                 `json:"ticker"`
		Type     lifecycle.OrderType    `json:"order_type"`
		Limit    *decimal.Decimal       `json:"limit_price,omitempty"`
		Evidence json.RawMessage        `json:"decision_metadata"`
	}{"signal-preparation-v1", runBinding, p.ticker, route.OrderType, route.LimitPrice, proposal.Metadata})
	if err != nil {
		return uuid.Nil, 0, err
	}
	proposal.Metadata = metadata
	proposed, err := lifecycle.Propose(proposal)
	if err != nil {
		return uuid.Nil, 0, err
	}
	existing, err := p.repo.FindExecutionLifecycleByIdempotencyKey(ctx, scope.AccountID(), proposal.IdempotencyKey)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return uuid.Nil, 0, err
	}
	if existing != nil && !lifecycle.SameIntentPayload(&existing.Intent, &proposed.Intent) {
		return uuid.Nil, 0, fmt.Errorf("signal preparation intent conflicts: %w", repository.ErrIdempotencyConflict)
	}
	allocationEvidence, err := json.Marshal(struct {
		Quantity decimal.Decimal `json:"quantity"`
	}{delta})
	if err != nil {
		return uuid.Nil, 0, err
	}
	allocation := p.signalPreparationEvent("allocation", proposal.IdempotencyKey, allocationEvidence)
	if existing != nil {
		for _, event := range existing.Events {
			if event.Kind == lifecycle.EventIntentAllocated {
				allocation = preparationEventInput(event)
			}
		}
	}
	p.resolved = &ExecutionOrderPreparation{Proposal: proposal, AllocatedQuantity: delta, Allocation: allocation, Route: route}
	p.orderID = economicid.DeterministicUUID("execution-order", proposed.Intent.ID.String(), route.OrderIdempotencyKey)
	return p.orderID, quantity.InexactFloat64(), nil
}

// PersistApproved is invoked by OrderManager only after actual risk admission.
func (p *SignalPreparation) PersistApproved(ctx context.Context, scope execution.ExecutionScope, order domain.Order) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.resolved == nil || order.ID != p.orderID || order.ClientOrderID != p.orderID.String() || order.AccountID != scope.AccountID() ||
		p.resolved.Proposal.Account.ID != scope.AccountID() || math.IsNaN(order.Quantity) || math.IsInf(order.Quantity, 0) ||
		!decimal.NewFromFloat(order.Quantity).Equal(p.resolved.AllocatedQuantity.Abs()) {
		return fmt.Errorf("signal preparation approval does not match resolved command")
	}
	input := *p.resolved
	origin, originID := scope.Origin()
	wantSide := domain.OrderSideBuy
	if input.AllocatedQuantity.IsNegative() {
		wantSide = domain.OrderSideSell
	}
	if input.Proposal.OriginType != origin || input.Proposal.OriginID != originID || input.Proposal.Account.Environment != scope.Environment() ||
		input.Proposal.CopyOriginRebalanceRunID != scope.CopyOriginRunID() || order.CopyOriginRebalanceRunID != scope.CopyOriginRunID() ||
		order.OriginType != string(origin) || order.OriginID != originID || order.Environment != scope.Environment() ||
		order.Ticker != p.ticker || order.Side != wantSide || string(order.OrderType) != string(input.Route.OrderType) ||
		!preparedOrderPriceMatches(input.Route.LimitPrice, order.LimitPrice) || !preparedOrderPriceMatches(input.Route.StopPrice, order.StopPrice) {
		return fmt.Errorf("signal preparation admitted command differs from resolved mechanics")
	}
	if err := requireSignalPreparationRun(input.Proposal.Metadata, scope); err != nil {
		return err
	}
	run, hasRun := scope.PipelineRun()
	if hasRun && (order.PipelineRunID == nil || *order.PipelineRunID != run.ID || order.PipelineRunTradeDate == nil || !order.PipelineRunTradeDate.Equal(run.TradeDate)) ||
		!hasRun && (order.PipelineRunID != nil || order.PipelineRunTradeDate != nil) {
		return fmt.Errorf("signal preparation admitted order pipeline run mismatch")
	}
	evidence, err := json.Marshal(struct {
		OrderID  uuid.UUID       `json:"order_id"`
		Quantity decimal.Decimal `json:"quantity"`
		Approved bool            `json:"approved"`
	}{order.ID, input.AllocatedQuantity, true})
	if err != nil {
		return err
	}
	input.RiskApproval = p.signalPreparationEvent("risk", input.Proposal.IdempotencyKey, evidence)
	input.Route.Event = p.signalPreparationEvent("route", input.Proposal.IdempotencyKey, evidence)
	existing, err := p.repo.FindExecutionLifecycleByIdempotencyKey(ctx, scope.AccountID(), input.Proposal.IdempotencyKey)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	if existing != nil {
		for _, event := range existing.Events {
			switch event.Kind {
			case lifecycle.EventRiskApproved:
				input.RiskApproval = preparationEventInput(event)
			case lifecycle.EventOrderRouted:
				input.Route.Event = preparationEventInput(event)
			}
		}
	}
	input.Route.RoutedAt, input.Route.CreatedAt = input.Route.Event.ReceivedAt, input.Route.Event.ReceivedAt
	if _, err := p.repo.PrepareExecutionOrder(ctx, input); err != nil {
		return err
	}
	return NewAcceptedEconomicPlanner(p.repo.pool).RequireAcceptedOrderPrepared(ctx, scope, &order)
}

func (p *SignalPreparation) signalPreparationEvent(stage, key string, evidence json.RawMessage) lifecycle.EventInput {
	now := p.now().UTC().Truncate(time.Microsecond)
	return lifecycle.EventInput{
		Source: "order-manager", SourceNamespace: "signal-preparation-v1", SourceEventID: key + ":" + stage,
		SourceAt: now, ReceivedAt: now, Actor: "order-manager", ReasonCode: stage, Evidence: evidence,
	}
}

func requireSignalPreparationRun(metadata json.RawMessage, scope execution.ExecutionScope) error {
	var binding struct {
		Version string                 `json:"version"`
		Run     *domain.PipelineRunRef `json:"pipeline_run"`
	}
	if err := json.Unmarshal(metadata, &binding); err != nil {
		return err
	}
	run, hasRun := scope.PipelineRun()
	if binding.Version != "signal-preparation-v1" || hasRun != (binding.Run != nil) ||
		hasRun && (binding.Run.ID != run.ID || !binding.Run.TradeDate.Equal(run.TradeDate)) {
		return fmt.Errorf("canonical signal preparation pipeline run mismatch")
	}
	return nil
}

func preparationEventInput(event lifecycle.Event) lifecycle.EventInput {
	return lifecycle.EventInput{
		Source: event.Source, SourceNamespace: event.SourceNamespace, SourceEventID: event.SourceEventID,
		SourceRevision: event.SourceRevision, ObservationClass: event.ObservationClass, ObservationDiscriminator: event.ObservationDiscriminator,
		SourceAt: event.SourceAt, ReceivedAt: event.ReceivedAt, Actor: event.Actor, ReasonCode: event.ReasonCode, Reason: event.Reason, Evidence: event.Evidence,
	}
}
