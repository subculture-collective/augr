package portfolio

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/google/uuid"
)

const (
	defaultStockTTL       = 24 * time.Hour
	defaultEventMarketTTL = 6 * time.Hour
)

type OpportunityBuilderConfig struct {
	StockTTL       time.Duration
	EventMarketTTL time.Duration
	Now            func() time.Time
}

type OpportunityBuildInput struct {
	Scope             execution.ExecutionScope
	Strategy          domain.Strategy
	Run               *domain.PipelineRun
	Decision          *domain.TradeDecision
	Signal            domain.PipelineSignal
	PredictionSide    string
	Confidence        float64
	EdgePct           float64
	ExpectedReturnPct float64
	MaxLossPct        float64
	EntryPrice        float64
	LiquidityUSD      float64
	SpreadPct         float64
	ProposedNotional  float64
	Reason            string
	Evidence          json.RawMessage
	OptionSpread      *domain.OptionSpread
	QuoteObservedAt   *time.Time
}

func BuildOpportunity(input OpportunityBuildInput, cfg OpportunityBuilderConfig) (*domain.Opportunity, NoActionReason, error) {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	stockTTL := cfg.StockTTL
	if stockTTL <= 0 {
		stockTTL = defaultStockTTL
	}
	eventTTL := cfg.EventMarketTTL
	if eventTTL <= 0 {
		eventTTL = defaultEventMarketTTL
	}

	strategyStatus := strings.ToLower(strings.TrimSpace(input.Strategy.Status))
	marketType := input.Strategy.MarketType.Normalize()
	ticker := strings.TrimSpace(input.Strategy.Ticker)
	if input.Strategy.ID == uuid.Nil || ticker == "" || !marketType.IsValid() || strategyStatus != domain.StrategyStatusActive {
		if strategyStatus != domain.StrategyStatusActive {
			return nil, NoActionReasonUnknown, fmt.Errorf("strategy must be active")
		}
		return nil, NoActionReasonUnknown, fmt.Errorf("strategy must include id, market type, and ticker")
	}

	if input.Signal == domain.PipelineSignalHold {
		return nil, NoActionReasonHoldSignal, nil
	}
	if input.Signal != domain.PipelineSignalBuy && input.Signal != domain.PipelineSignalSell {
		return nil, NoActionReasonUnknown, fmt.Errorf("unsupported signal: %q", input.Signal)
	}
	if input.Run == nil || input.Run.ID == uuid.Nil || input.Run.AccountID == uuid.Nil || !input.Run.Environment.IsValid() || input.Run.OriginType != "strategy_version" || input.Run.OriginID == "" || input.Run.TradeDate.IsZero() {
		return nil, NoActionReasonUnknown, fmt.Errorf("complete source run scope is required")
	}
	if input.Strategy.ExecutionStrategyVersionID == nil || input.Run.OriginID != input.Strategy.ExecutionStrategyVersionID.String() || input.Run.StrategyID != input.Strategy.ID {
		return nil, NoActionReasonUnknown, fmt.Errorf("source run scope does not match strategy binding")
	}
	runRef, ok := input.Scope.PipelineRun()
	originType, originID := input.Scope.Origin()
	if !ok || input.Scope.AccountID() != input.Run.AccountID || input.Scope.Environment() != input.Run.Environment || string(originType) != input.Run.OriginType || originID != input.Run.OriginID || runRef.ID != input.Run.ID || !runRef.TradeDate.Equal(input.Run.TradeDate) {
		return nil, NoActionReasonUnknown, fmt.Errorf("execution scope does not match persisted source run")
	}
	legacyStrategyID := input.Scope.LegacyStrategyID()
	if legacyStrategyID == nil || *legacyStrategyID != input.Strategy.ID {
		return nil, NoActionReasonUnknown, fmt.Errorf("execution scope does not match strategy binding")
	}

	side := orderSideFromSignal(input.Signal)
	if input.Decision != nil && input.Decision.Side.IsValid() {
		side = input.Decision.Side
	}

	createdAt := now().UTC()
	opportunity := &domain.Opportunity{
		AccountID:         input.Scope.AccountID(),
		Environment:       input.Scope.Environment(),
		OriginType:        string(originType),
		OriginID:          originID,
		StrategyID:        input.Strategy.ID,
		MarketType:        marketType,
		Ticker:            ticker,
		Side:              side,
		PredictionSide:    strings.ToUpper(strings.TrimSpace(input.PredictionSide)),
		Signal:            input.Signal,
		Status:            domain.OpportunityStatusQueued,
		Confidence:        clampFloat(input.Confidence, 0, 1),
		EdgePct:           clampMin(input.EdgePct, 0),
		ExpectedReturnPct: clampMin(input.ExpectedReturnPct, 0),
		MaxLossPct:        clampMin(input.MaxLossPct, 0),
		EntryPrice:        clampMin(input.EntryPrice, 0),
		LiquidityUSD:      clampMin(input.LiquidityUSD, 0),
		SpreadPct:         clampMin(input.SpreadPct, 0),
		ProposedNotional:  clampMin(input.ProposedNotional, 0),
		Reason:            input.Reason,
		Evidence:          normalizeEvidence(input.Evidence),
		CreatedAt:         createdAt,
		UpdatedAt:         createdAt,
	}
	if err := bindPromotedOpportunityLineage(opportunity, input.Strategy, input.Run); err != nil {
		return nil, NoActionReasonUnknown, err
	}
	if marketType == domain.MarketTypeOptions {
		if err := bindDefinedRiskOptionIntent(opportunity, input.OptionSpread, input.QuoteObservedAt); err != nil {
			return nil, NoActionReasonUnknown, err
		}
	}

	runID, tradeDate := runRef.ID, runRef.TradeDate
	opportunity.PipelineRunID = &runID
	opportunity.PipelineRunTradeDate = &tradeDate

	switch opportunity.MarketType {
	case domain.MarketTypeStock, domain.MarketTypeCrypto, domain.MarketTypeOptions:
		opportunity.ExpiresAt = createdAt.Add(stockTTL)
	case domain.MarketTypeKalshi, domain.MarketTypePolymarket:
		opportunity.ExpiresAt = createdAt.Add(eventTTL)
	default:
		return nil, NoActionReasonUnknown, fmt.Errorf("unsupported market type: %q", input.Strategy.MarketType)
	}

	opportunity.DedupeKey = dedupeKey(createdAt, opportunity.AccountID, opportunity.Environment, opportunity.OriginType, opportunity.OriginID, runRef, input.Strategy.ID, opportunity.MarketType, opportunity.Ticker, side, input.Signal)
	return opportunity, "", nil
}

func bindPromotedOpportunityLineage(opportunity *domain.Opportunity, strategy domain.Strategy, run *domain.PipelineRun) error {
	if opportunity == nil || strategy.ExecutionStrategyVersionID == nil {
		return errors.New("promoted opportunity requires an execution version")
	}
	lifecycle, err := domain.ParseActivePromotionExecutionLineage(strategy.Config, opportunity.AccountID)
	if err != nil {
		return err
	}
	if err := validateOpportunityRunLineage(run, *strategy.ExecutionStrategyVersionID, lifecycle); err != nil {
		return err
	}
	opportunity.ExecutionVersionID = *strategy.ExecutionStrategyVersionID
	opportunity.EvaluationScopeID = lifecycle.EvaluationScopeID
	opportunity.ManifestID = lifecycle.ManifestID
	opportunity.QualityResultID = lifecycle.QualityResultID
	opportunity.DeploymentID = lifecycle.DeploymentID
	opportunity.PromotionDecisionID = lifecycle.PromotionDecisionID
	opportunity.CapitalBindingID = lifecycle.CapitalBindingID
	opportunity.RiskPolicyVersion = lifecycle.RiskPolicyVersion
	opportunity.DeploymentBudgetUSD = lifecycle.DeploymentBudgetUSD
	return nil
}

func validateOpportunityRunLineage(run *domain.PipelineRun, versionID uuid.UUID, lifecycle domain.PromotionExecutionLineage) error {
	if run == nil || run.ExecutionVersionID != versionID || run.EvaluationScopeID != lifecycle.EvaluationScopeID ||
		run.ManifestID != lifecycle.ManifestID || run.QualityResultID != lifecycle.QualityResultID ||
		run.DeploymentID != lifecycle.DeploymentID || run.PromotionDecisionID != lifecycle.PromotionDecisionID ||
		run.CapitalBindingID != lifecycle.CapitalBindingID || run.RiskPolicyVersion != lifecycle.RiskPolicyVersion {
		return errors.New("source run promotion lineage does not match scheduled strategy")
	}
	return nil
}

func bindDefinedRiskOptionIntent(opportunity *domain.Opportunity, spread *domain.OptionSpread, quoteObservedAt *time.Time) error {
	if opportunity == nil || spread == nil || len(spread.Legs) != 2 || spread.MaxRisk <= 0 || quoteObservedAt == nil {
		return errors.New("options opportunity requires an exact quoted defined-risk spread")
	}
	opportunity.MaxLossPerUnit = spread.MaxRisk
	opportunity.RequiredCapitalUnit = spread.MaxRisk
	opportunity.QuoteObservedAt = quoteObservedAt
	opportunity.OptionLegs = make([]domain.OpportunityOptionLeg, 0, 2)
	for sequence, leg := range spread.Legs {
		if leg.Contract.InstrumentID == uuid.Nil {
			return errors.New("option opportunity contract lacks immutable instrument identity")
		}
		opportunity.OptionLegs = append(opportunity.OptionLegs, domain.OpportunityOptionLeg{
			Sequence: sequence, ContractID: leg.Contract.InstrumentID, OCCSymbol: leg.Contract.OCCSymbol, Underlying: leg.Contract.Underlying,
			Expiry: leg.Contract.Expiry, OptionType: string(leg.Contract.OptionType), Strike: leg.Contract.Strike, Ratio: leg.Ratio,
			Side: leg.Side, PositionIntent: string(leg.PositionIntent), Bid: leg.Bid, Ask: leg.Ask,
			Multiplier: int(leg.Contract.Multiplier),
		})
		opportunity.Delta += leg.Greeks.Delta * float64(leg.Ratio)
		opportunity.Gamma += leg.Greeks.Gamma * float64(leg.Ratio)
		opportunity.Theta += leg.Greeks.Theta * float64(leg.Ratio)
		opportunity.Vega += leg.Greeks.Vega * float64(leg.Ratio)
	}
	return nil
}

func orderSideFromSignal(signal domain.PipelineSignal) domain.OrderSide {
	switch signal {
	case domain.PipelineSignalSell:
		return domain.OrderSideSell
	default:
		return domain.OrderSideBuy
	}
}

func clampMin(value, minimum float64) float64 {
	if value < minimum || value != value {
		return minimum
	}
	return value
}

func clampFloat(value, minimum, maximum float64) float64 {
	if value != value {
		return minimum
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func normalizeEvidence(evidence json.RawMessage) json.RawMessage {
	if len(evidence) == 0 {
		return json.RawMessage("{}")
	}
	return evidence
}

func dedupeKey(now time.Time, accountID uuid.UUID, environment domain.AccountEnvironment, originType, originID string, run domain.PipelineRunRef, strategyID uuid.UUID, marketType domain.MarketType, ticker string, side domain.OrderSide, signal domain.PipelineSignal) string {
	return strings.ToLower(fmt.Sprintf("%s:%s:%s:%s:%s:%s:%s:%s:%s:%s:%s:%s",
		now.UTC().Format("2006-01-02"),
		accountID,
		environment,
		originType,
		originID,
		run.ID,
		run.TradeDate.UTC().Format("2006-01-02"),
		strategyID,
		marketType.Normalize(),
		ticker,
		side,
		signal,
	))
}
