package portfolio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/risk"
)

type paperOptionsAccountPreflight interface {
	PreflightPaperOptions(context.Context, float64) error
}

type paperOptionsPreflightReasoner interface {
	PaperOptionsPreflightReason() string
}

type OptionsPaperOrderProcessorDeps struct {
	Broker          execution.OptionsBroker
	OrderRepo       repository.OrderRepository
	PositionRepo    repository.PositionRepository
	TradeRepo       repository.TradeRepository
	RiskEngine      risk.RiskEngine
	EconomicWriter  execution.AcceptedOptionFillWriter
	OpportunityRepo repository.OpportunityRepository
	Logger          *slog.Logger
}

type OptionsPaperOrderProcessor struct {
	deps OptionsPaperOrderProcessorDeps
}

func NewOptionsPaperOrderProcessor(deps OptionsPaperOrderProcessorDeps) *OptionsPaperOrderProcessor {
	return &OptionsPaperOrderProcessor{deps: deps}
}

func (processor *OptionsPaperOrderProcessor) ProcessPaperOptionsOrder(ctx context.Context, scope execution.ExecutionScope, opportunity domain.Opportunity, decision domain.AllocationDecision) (PaperOrderResult, error) {
	if processor == nil || processor.deps.Broker == nil || processor.deps.OrderRepo == nil || processor.deps.PositionRepo == nil ||
		processor.deps.TradeRepo == nil || processor.deps.RiskEngine == nil || processor.deps.EconomicWriter == nil || processor.deps.OpportunityRepo == nil {
		return PaperOrderResult{}, errors.New("portfolio: options paper processor dependencies are required")
	}
	if opportunity.MarketType != domain.MarketTypeOptions || decision.ExecutionRoute != "alpaca_mleg" || decision.Quantity <= 0 || decision.Quantity != math.Floor(decision.Quantity) {
		return PaperOrderResult{Skipped: true, Reason: "invalid_defined_risk_option_decision"}, nil
	}
	if decision.ReservedRiskUSD != decision.Quantity*opportunity.MaxLossPerUnit || decision.ReservedCapitalUSD != decision.Quantity*opportunity.RequiredCapitalUnit {
		return PaperOrderResult{Skipped: true, Reason: "option_allocation_risk_mismatch"}, nil
	}
	preflight, ok := processor.deps.Broker.(paperOptionsAccountPreflight)
	if !ok {
		return PaperOrderResult{Skipped: true, Reason: "missing_paper_options_preflight"}, nil
	}
	if err := preflight.PreflightPaperOptions(ctx, decision.ReservedCapitalUSD); err != nil {
		return PaperOrderResult{Skipped: true, Reason: paperOptionsPreflightReason(err)}, nil
	}
	spread, err := DefinedRiskSpreadFromOpportunity(opportunity)
	if err != nil {
		return PaperOrderResult{Skipped: true, Reason: "invalid_defined_risk_option_package"}, nil
	}
	orderRepo := allocationOrderRepo{OrderRepository: processor.deps.OrderRepo, opportunityID: opportunity.ID, claimID: decision.ExecutionClaimID}
	manager := execution.NewOptionsOrderManager(processor.deps.Broker, orderRepo, processor.deps.PositionRepo, processor.deps.TradeRepo, processor.deps.RiskEngine, processor.deps.Logger).
		WithAcceptedOptionFillWriter(processor.deps.EconomicWriter).
		WithBrokerName("alpaca").
		WithLiveTrading(false)
	err = manager.ProcessSpreadSignal(ctx, scope, spread, decision.Quantity)
	allocationRepo, ok := processor.deps.OrderRepo.(repository.AllocationOrderRepository)
	if !ok {
		return PaperOrderResult{}, errors.New("portfolio: allocation order lookup is required")
	}
	order, lookupErr := allocationRepo.GetByAllocationOpportunity(ctx, opportunity)
	if lookupErr != nil && !errors.Is(lookupErr, repository.ErrNotFound) {
		return PaperOrderResult{}, errors.Join(err, lookupErr)
	}
	if order == nil {
		if err != nil {
			return PaperOrderResult{}, err
		}
		return PaperOrderResult{Skipped: true, Reason: "option_package_order_not_created"}, nil
	}
	return classifyOptionsPackageResult(order, err)
}

func paperOptionsPreflightReason(err error) string {
	if err == nil {
		return ""
	}
	var reasoner paperOptionsPreflightReasoner
	if errors.As(err, &reasoner) {
		code := strings.TrimSpace(reasoner.PaperOptionsPreflightReason())
		if code != "" {
			return "paper_options_preflight_" + code
		}
	}
	return "paper_options_preflight_rejected"
}

func classifyOptionsPackageResult(order *domain.Order, submitErr error) (PaperOrderResult, error) {
	if order == nil {
		return PaperOrderResult{}, submitErr
	}
	result := PaperOrderResult{OrderID: &order.ID, Status: order.Status}
	if order.Status == domain.OrderStatusRejected || order.Status == domain.OrderStatusCancelled {
		result.Reason = "option_package_" + order.Status.String()
		return result, nil
	}
	return result, submitErr
}

func (processor *OptionsPaperOrderProcessor) ReconcilePaperOrder(ctx context.Context, opportunity domain.Opportunity, _ *domain.Order, claimID uuid.UUID) (PaperOrderResult, error) {
	if processor == nil || processor.deps.Broker == nil || processor.deps.OrderRepo == nil || processor.deps.PositionRepo == nil || processor.deps.TradeRepo == nil || processor.deps.RiskEngine == nil || processor.deps.EconomicWriter == nil {
		return PaperOrderResult{}, errors.New("portfolio: options paper recovery dependencies are required")
	}
	packageRepo, ok := processor.deps.OrderRepo.(repository.AllocationPackageOrderRepository)
	if !ok {
		return PaperOrderResult{}, errors.New("portfolio: allocation package order lookup is required")
	}
	orders, err := packageRepo.ListByAllocationOpportunity(ctx, opportunity)
	if err != nil || len(orders) != 2 {
		return PaperOrderResult{}, fmt.Errorf("portfolio: exact two-leg recovery package is required: %w", err)
	}
	positionRepo, ok := processor.deps.PositionRepo.(repository.AccountScopedPositionRepository)
	if !ok {
		return PaperOrderResult{}, errors.New("portfolio: account-scoped option positions are required")
	}
	positions, err := positionRepo.GetOpenByAccount(ctx, opportunity.AccountID, opportunity.Environment, repository.PositionFilter{}, 1000, 0)
	if err != nil {
		return PaperOrderResult{}, err
	}
	binding, err := domain.NewExecutionAccountBinding(opportunity.AccountID, opportunity.Environment)
	if err != nil {
		return PaperOrderResult{}, err
	}
	orderRepo := allocationOrderRepo{OrderRepository: processor.deps.OrderRepo, opportunityID: opportunity.ID, claimID: claimID}
	manager := execution.NewOptionsOrderManager(processor.deps.Broker, orderRepo, processor.deps.PositionRepo, processor.deps.TradeRepo, processor.deps.RiskEngine, processor.deps.Logger).
		WithAcceptedOptionFillWriter(processor.deps.EconomicWriter).WithBrokerName("alpaca").WithLiveTrading(false)
	if err := manager.ReconcilePendingOptionOrders(ctx, binding, orders, positions); err != nil {
		return PaperOrderResult{}, err
	}
	status := domain.OrderStatusFilled
	for _, existing := range orders {
		reloaded, loadErr := processor.deps.OrderRepo.Get(ctx, existing.ID)
		if loadErr != nil {
			return PaperOrderResult{}, loadErr
		}
		if reloaded.Status == domain.OrderStatusRejected {
			status = domain.OrderStatusRejected
			break
		}
		if reloaded.Status == domain.OrderStatusCancelled {
			status = domain.OrderStatusCancelled
			break
		}
		if reloaded.Status != domain.OrderStatusFilled {
			status = reloaded.Status
		}
	}
	return PaperOrderResult{OrderID: &orders[0].ID, Status: status}, nil
}

func DefinedRiskSpreadFromOpportunity(opportunity domain.Opportunity) (*domain.OptionSpread, error) {
	if opportunity.MarketType != domain.MarketTypeOptions || !validVertical(opportunity.OptionLegs) || opportunity.MaxLossPerUnit <= 0 {
		return nil, errors.New("portfolio: exact two-leg defined-risk option package is required")
	}
	legs := make([]domain.SpreadLeg, 0, 2)
	for _, source := range opportunity.OptionLegs {
		executable := source.Ask
		if source.Side == domain.OrderSideSell {
			executable = source.Bid
		}
		legs = append(legs, domain.SpreadLeg{
			Contract:          domain.OptionContract{InstrumentID: source.ContractID, OCCSymbol: source.OCCSymbol, Underlying: source.Underlying, OptionType: domain.OptionType(source.OptionType), Strike: source.Strike, Expiry: source.Expiry, Multiplier: float64(source.Multiplier)},
			ContractPayloadID: source.ContractPayloadID, ContractSHA256: source.ContractSHA256,
			QuotePayloadID: source.QuotePayloadID, QuoteSHA256: source.QuoteSHA256,
			SnapshotPayloadID: source.SnapshotPayloadID, SnapshotSHA256: source.SnapshotSHA256,
			Side: source.Side, PositionIntent: domain.PositionIntent(source.PositionIntent), Ratio: source.Ratio, Quantity: 1, ExecutablePrice: executable,
		})
	}
	strategyType, err := verticalStrategyType(legs)
	if err != nil {
		return nil, err
	}
	widthValue := math.Abs(legs[0].Contract.Strike-legs[1].Contract.Strike) * legs[0].Contract.Multiplier
	if opportunity.MaxLossPerUnit > widthValue {
		return nil, errors.New("portfolio: option maximum loss exceeds vertical width")
	}
	return &domain.OptionSpread{
		StrategyType: strategyType, Underlying: strings.ToUpper(strings.TrimSpace(opportunity.Ticker)), Legs: legs,
		MaxRisk: opportunity.MaxLossPerUnit, MaxReward: widthValue - opportunity.MaxLossPerUnit,
	}, nil
}

func verticalStrategyType(legs []domain.SpreadLeg) (domain.OptionStrategyType, error) {
	if len(legs) != 2 {
		return "", errors.New("portfolio: two option legs are required")
	}
	var long, short domain.SpreadLeg
	for _, leg := range legs {
		switch leg.Side {
		case domain.OrderSideBuy:
			long = leg
		case domain.OrderSideSell:
			short = leg
		}
	}
	if long.Contract.OCCSymbol == "" || short.Contract.OCCSymbol == "" {
		return "", errors.New("portfolio: long and protective legs are required")
	}
	switch long.Contract.OptionType {
	case domain.OptionTypeCall:
		if long.Contract.Strike < short.Contract.Strike {
			return domain.StrategyBullCallSpread, nil
		}
		return domain.StrategyBearCallSpread, nil
	case domain.OptionTypePut:
		if long.Contract.Strike > short.Contract.Strike {
			return domain.StrategyBearPutSpread, nil
		}
		return domain.StrategyBullPutSpread, nil
	default:
		return "", fmt.Errorf("portfolio: unsupported option type %q", long.Contract.OptionType)
	}
}

var (
	_ PaperOptionsOrderProcessor = (*OptionsPaperOrderProcessor)(nil)
	_ PaperOrderReconciler       = (*OptionsPaperOrderProcessor)(nil)
)
