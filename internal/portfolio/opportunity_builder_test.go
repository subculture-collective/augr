package portfolio

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/google/uuid"
)

func scopedOpportunitySource(market domain.MarketType, ticker string) (domain.Strategy, *domain.PipelineRun, execution.ExecutionScope) {
	strategyID, versionID := uuid.New(), uuid.New()
	run := &domain.PipelineRun{ID: uuid.New(), AccountID: uuid.New(), Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(), StrategyID: strategyID, TradeDate: time.Date(2026, 6, 19, 0, 0, 0, 0, time.UTC)}
	deploymentID, decisionID, scopeID := uuid.New(), uuid.New(), uuid.New()
	manifestID, qualityID, capitalBindingID := uuid.New(), uuid.New(), uuid.New()
	riskPolicyVersion := "portfolio-risk-policy-v1@sha256:" + strings.Repeat("a", 64)
	config, _ := json.Marshal(map[string]any{"research_lifecycle": map[string]any{
		"stage": "shadow", "activation": "promotion_evaluator_v1", "auto_activation_blocked": false,
		"deployment_id": deploymentID, "promotion_decision_id": decisionID, "evaluation_scope_id": scopeID,
		"account_id": run.AccountID, "manifest_id": manifestID, "quality_result_id": qualityID, "capital_binding_id": capitalBindingID,
		"deployment_budget_usd": 2500, "risk_policy_version": riskPolicyVersion,
	}})
	run.ExecutionVersionID, run.EvaluationScopeID, run.ManifestID, run.QualityResultID = versionID, scopeID, manifestID, qualityID
	run.DeploymentID, run.PromotionDecisionID, run.CapitalBindingID, run.RiskPolicyVersion = deploymentID, decisionID, capitalBindingID, riskPolicyVersion
	strategy := domain.Strategy{ID: strategyID, Ticker: ticker, MarketType: market, Status: domain.StrategyStatusActive, ExecutionStrategyVersionID: &versionID, Config: config}
	scope, err := execution.NewStrategyExecutionScope(run.AccountID, run.Environment, versionID, domain.PipelineRunRef{ID: run.ID, TradeDate: run.TradeDate}, strategyID)
	if err != nil {
		panic(err)
	}
	return strategy, run, scope
}

func TestBuildOpportunityBuyStock(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 19, 15, 4, 5, 0, time.UTC)
	strategy, run, scope := scopedOpportunitySource(domain.MarketTypeStock, "AAPL")
	strategyID, runID := strategy.ID, run.ID

	opportunity, reason, err := BuildOpportunity(OpportunityBuildInput{
		Scope:             scope,
		Strategy:          strategy,
		Run:               run,
		Signal:            domain.PipelineSignalBuy,
		PredictionSide:    "yes",
		Confidence:        0.8,
		EdgePct:           1.2,
		ExpectedReturnPct: 2.4,
		MaxLossPct:        0.6,
		EntryPrice:        100,
		LiquidityUSD:      1000,
		SpreadPct:         0.2,
		ProposedNotional:  250,
		Reason:            "good setup",
	}, OpportunityBuilderConfig{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("BuildOpportunity() error = %v", err)
	}
	if reason != "" {
		t.Fatalf("BuildOpportunity() reason = %q, want empty", reason)
	}
	if opportunity == nil {
		t.Fatal("BuildOpportunity() opportunity = nil")
	}
	if opportunity.Status != domain.OpportunityStatusQueued {
		t.Fatalf("status = %q, want queued", opportunity.Status)
	}
	if opportunity.Side != domain.OrderSideBuy {
		t.Fatalf("side = %q, want buy", opportunity.Side)
	}
	if opportunity.EntryPrice != 100 {
		t.Fatalf("entry price = %v, want 100", opportunity.EntryPrice)
	}
	if opportunity.PredictionSide != "YES" {
		t.Fatalf("prediction side = %q, want YES", opportunity.PredictionSide)
	}
	if opportunity.CapitalBindingID != run.CapitalBindingID {
		t.Fatalf("capital binding = %s, want %s", opportunity.CapitalBindingID, run.CapitalBindingID)
	}
	if opportunity.MarketType != domain.MarketTypeStock {
		t.Fatalf("market type = %q, want stock", opportunity.MarketType)
	}
	if opportunity.ExpiresAt.Sub(now) != 24*time.Hour {
		t.Fatalf("expires at delta = %v, want 24h", opportunity.ExpiresAt.Sub(now))
	}
	wantDedupe := strings.ToLower("2026-06-19:" + run.AccountID.String() + ":" + string(run.Environment) + ":" + run.OriginType + ":" + run.OriginID + ":" + run.ID.String() + ":" + run.TradeDate.Format("2006-01-02") + ":" + strategyID.String() + ":stock:aapl:buy:buy")
	if opportunity.DedupeKey != wantDedupe {
		t.Fatalf("dedupe key = %q", opportunity.DedupeKey)
	}
	if opportunity.PipelineRunID == nil || *opportunity.PipelineRunID != runID {
		t.Fatalf("pipeline run id = %#v, want %s", opportunity.PipelineRunID, runID)
	}
	if opportunity.AccountID != run.AccountID || opportunity.Environment != run.Environment || opportunity.OriginType != run.OriginType || opportunity.OriginID != run.OriginID || opportunity.PipelineRunTradeDate == nil || !opportunity.PipelineRunTradeDate.Equal(run.TradeDate) {
		t.Fatalf("opportunity source scope=%+v run=%+v", opportunity, run)
	}
	if opportunity.Evidence == nil || string(opportunity.Evidence) != "{}" {
		t.Fatalf("evidence = %s, want {}", opportunity.Evidence)
	}
}

func TestBuildOpportunityHold(t *testing.T) {
	t.Parallel()

	opportunity, reason, err := BuildOpportunity(OpportunityBuildInput{
		Strategy: domain.Strategy{
			ID:         uuid.New(),
			Ticker:     "AAPL",
			MarketType: domain.MarketTypeStock,
			Status:     domain.StrategyStatusActive,
		},
		Signal: domain.PipelineSignalHold,
	}, OpportunityBuilderConfig{})
	if err != nil {
		t.Fatalf("BuildOpportunity() error = %v", err)
	}
	if opportunity != nil {
		t.Fatalf("BuildOpportunity() opportunity = %#v, want nil", opportunity)
	}
	if reason != NoActionReasonHoldSignal {
		t.Fatalf("reason = %q, want %q", reason, NoActionReasonHoldSignal)
	}
}

func TestBuildOpportunityKalshiTTL(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 19, 15, 4, 5, 0, time.UTC)
	strategy, run, scope := scopedOpportunitySource(domain.MarketTypeKalshi, "ELECTION")
	opportunity, reason, err := BuildOpportunity(OpportunityBuildInput{
		Scope:    scope,
		Strategy: strategy,
		Run:      run,
		Signal:   domain.PipelineSignalSell,
	}, OpportunityBuilderConfig{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("BuildOpportunity() error = %v", err)
	}
	if reason != "" {
		t.Fatalf("BuildOpportunity() reason = %q, want empty", reason)
	}
	if opportunity == nil {
		t.Fatal("BuildOpportunity() opportunity = nil")
	}
	if opportunity.ExpiresAt.Sub(now) != 6*time.Hour {
		t.Fatalf("expires at delta = %v, want 6h", opportunity.ExpiresAt.Sub(now))
	}
	if opportunity.Side != domain.OrderSideSell {
		t.Fatalf("side = %q, want sell", opportunity.Side)
	}
}

func TestBuildOpportunityInactiveStrategy(t *testing.T) {
	t.Parallel()

	opportunity, reason, err := BuildOpportunity(OpportunityBuildInput{
		Strategy: domain.Strategy{
			ID:         uuid.New(),
			Ticker:     "AAPL",
			MarketType: domain.MarketTypeStock,
			Status:     domain.StrategyStatusInactive,
		},
		Signal: domain.PipelineSignalBuy,
	}, OpportunityBuilderConfig{})

	if err == nil {
		t.Fatal("BuildOpportunity() error = nil, want non-nil")
	}
	if opportunity != nil {
		t.Fatalf("BuildOpportunity() opportunity = %#v, want nil", opportunity)
	}
	if reason != NoActionReasonUnknown {
		t.Fatalf("reason = %q, want %q", reason, NoActionReasonUnknown)
	}
}

func TestBuildOpportunityDecisionSideOverridesSignal(t *testing.T) {
	t.Parallel()
	strategy, run, scope := scopedOpportunitySource(domain.MarketTypeStock, "TSLA")

	opportunity, reason, err := BuildOpportunity(OpportunityBuildInput{
		Scope:    scope,
		Strategy: strategy,
		Run:      run,
		Decision: &domain.TradeDecision{Side: domain.OrderSideSell},
		Signal:   domain.PipelineSignalBuy,
	}, OpportunityBuilderConfig{})
	if err != nil {
		t.Fatalf("BuildOpportunity() error = %v", err)
	}
	if reason != "" {
		t.Fatalf("reason = %q, want empty", reason)
	}
	if opportunity == nil {
		t.Fatal("BuildOpportunity() opportunity = nil")
	}
	if opportunity.Side != domain.OrderSideSell {
		t.Fatalf("side = %q, want sell", opportunity.Side)
	}
}

func TestBuildOpportunityClampsNegativeMetrics(t *testing.T) {
	t.Parallel()
	strategy, run, scope := scopedOpportunitySource(domain.MarketTypeStock, "SPY")

	opportunity, reason, err := BuildOpportunity(OpportunityBuildInput{
		Scope:             scope,
		Strategy:          strategy,
		Run:               run,
		Signal:            domain.PipelineSignalSell,
		Confidence:        -0.5,
		EdgePct:           -1,
		ExpectedReturnPct: -2,
		MaxLossPct:        -3,
		EntryPrice:        -4,
		LiquidityUSD:      -4,
		SpreadPct:         -5,
		ProposedNotional:  -6,
		Evidence:          nil,
	}, OpportunityBuilderConfig{})
	if err != nil {
		t.Fatalf("BuildOpportunity() error = %v", err)
	}
	if reason != "" {
		t.Fatalf("reason = %q, want empty", reason)
	}
	if opportunity == nil {
		t.Fatal("BuildOpportunity() opportunity = nil")
	}
	if opportunity.Confidence != 0 || opportunity.EdgePct != 0 || opportunity.ExpectedReturnPct != 0 || opportunity.MaxLossPct != 0 || opportunity.EntryPrice != 0 || opportunity.LiquidityUSD != 0 || opportunity.SpreadPct != 0 || opportunity.ProposedNotional != 0 {
		t.Fatalf("metrics not clamped: %#v", opportunity)
	}
	if string(opportunity.Evidence) != "{}" {
		t.Fatalf("evidence = %s, want {}", opportunity.Evidence)
	}
}

func TestBuildOpportunityRejectsUnsupportedSignal(t *testing.T) {
	t.Parallel()

	opportunity, reason, err := BuildOpportunity(OpportunityBuildInput{
		Strategy: domain.Strategy{
			ID:         uuid.New(),
			Ticker:     "AAPL",
			MarketType: domain.MarketTypeStock,
			Status:     domain.StrategyStatusActive,
		},
		Signal: domain.PipelineSignal("unknown"),
	}, OpportunityBuilderConfig{})

	if err == nil {
		t.Fatal("BuildOpportunity() error = nil, want non-nil")
	}
	if opportunity != nil {
		t.Fatalf("BuildOpportunity() opportunity = %#v, want nil", opportunity)
	}
	if reason != NoActionReasonUnknown {
		t.Fatalf("reason = %q, want %q", reason, NoActionReasonUnknown)
	}
}

func TestBuildOpportunityRejectsScopeThatConflictsWithPersistedRun(t *testing.T) {
	t.Parallel()
	strategy, run, _ := scopedOpportunitySource(domain.MarketTypeStock, "AAPL")
	conflicting, err := execution.NewStrategyExecutionScope(uuid.New(), run.Environment, *strategy.ExecutionStrategyVersionID, domain.PipelineRunRef{ID: run.ID, TradeDate: run.TradeDate}, strategy.ID)
	if err != nil {
		t.Fatal(err)
	}
	opportunity, _, err := BuildOpportunity(OpportunityBuildInput{Scope: conflicting, Strategy: strategy, Run: run, Signal: domain.PipelineSignalBuy}, OpportunityBuilderConfig{})
	if err == nil || opportunity != nil {
		t.Fatalf("BuildOpportunity() = %#v, %v; want scope mismatch", opportunity, err)
	}
}

func TestBuildOpportunityRejectsMissingPromotionLifecycle(t *testing.T) {
	t.Parallel()
	strategy, run, scope := scopedOpportunitySource(domain.MarketTypeStock, "AAPL")
	strategy.Config = json.RawMessage(`{}`)

	opportunity, _, err := BuildOpportunity(OpportunityBuildInput{
		Scope: scope, Strategy: strategy, Run: run, Signal: domain.PipelineSignalBuy,
	}, OpportunityBuilderConfig{})
	if err == nil || opportunity != nil || !strings.Contains(err.Error(), "lacks promotion lifecycle") {
		t.Fatalf("BuildOpportunity() = %#v, %v; want missing promotion lifecycle rejection", opportunity, err)
	}
}

func TestBuildOpportunityRejectsRunPromotionLineageMismatch(t *testing.T) {
	t.Parallel()
	strategy, run, scope := scopedOpportunitySource(domain.MarketTypeStock, "AAPL")
	run.ManifestID = uuid.New()
	opportunity, _, err := BuildOpportunity(OpportunityBuildInput{
		Scope: scope, Strategy: strategy, Run: run, Signal: domain.PipelineSignalBuy,
	}, OpportunityBuilderConfig{})
	if err == nil || opportunity != nil || !strings.Contains(err.Error(), "source run promotion lineage") {
		t.Fatalf("BuildOpportunity() = %#v, %v; want run lineage rejection", opportunity, err)
	}
}

func TestBuildOpportunityBindsDefinedRiskOptionPackage(t *testing.T) {
	t.Parallel()
	strategy, run, scope := scopedOpportunitySource(domain.MarketTypeOptions, "AAPL")
	observedAt := time.Date(2026, 6, 19, 15, 0, 0, 0, time.UTC)
	expiry := time.Date(2026, 7, 17, 20, 0, 0, 0, time.UTC)
	longID, shortID := uuid.New(), uuid.New()
	spread := &domain.OptionSpread{
		StrategyType: domain.StrategyBullCallSpread,
		Underlying:   "AAPL",
		MaxRisk:      250,
		MaxReward:    250,
		Legs: []domain.SpreadLeg{
			{Contract: domain.OptionContract{InstrumentID: longID, OCCSymbol: "AAPL260717C00200000", Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 200, Expiry: expiry, Multiplier: 100}, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToOpen, Ratio: 1, Bid: 4.9, Ask: 5.0, Greeks: domain.OptionGreeks{Delta: .55, Gamma: .03, Theta: -.05, Vega: .12}},
			{Contract: domain.OptionContract{InstrumentID: shortID, OCCSymbol: "AAPL260717C00205000", Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 205, Expiry: expiry, Multiplier: 100}, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToOpen, Ratio: 1, Bid: 2.5, Ask: 2.6, Greeks: domain.OptionGreeks{Delta: .4, Gamma: .02, Theta: -.03, Vega: .09}},
		},
	}

	opportunity, _, err := BuildOpportunity(OpportunityBuildInput{
		Scope: scope, Strategy: strategy, Run: run, Signal: domain.PipelineSignalBuy,
		EntryPrice: 2.5, ProposedNotional: 250, OptionSpread: spread, QuoteObservedAt: &observedAt,
	}, OpportunityBuilderConfig{})
	if err != nil {
		t.Fatalf("BuildOpportunity() error = %v", err)
	}
	if opportunity.MaxLossPerUnit != 250 || opportunity.RequiredCapitalUnit != 250 || opportunity.QuoteObservedAt == nil || !opportunity.QuoteObservedAt.Equal(observedAt) {
		t.Fatalf("option risk binding = %+v", opportunity)
	}
	if len(opportunity.OptionLegs) != 2 || opportunity.OptionLegs[0].ContractID != longID || opportunity.OptionLegs[1].ContractID != shortID || opportunity.OptionLegs[0].Sequence != 0 || opportunity.OptionLegs[1].Sequence != 1 {
		t.Fatalf("option legs = %+v", opportunity.OptionLegs)
	}
	if math.Abs(opportunity.Delta-.95) > 1e-12 || math.Abs(opportunity.Gamma-.05) > 1e-12 || math.Abs(opportunity.Theta+.08) > 1e-12 || math.Abs(opportunity.Vega-.21) > 1e-12 {
		t.Fatalf("aggregate greeks = delta %v gamma %v theta %v vega %v", opportunity.Delta, opportunity.Gamma, opportunity.Theta, opportunity.Vega)
	}
}

func TestNormalizeEvidencePreservesExplicitPayload(t *testing.T) {
	t.Parallel()

	payload := json.RawMessage(`{"foo":"bar"}`)
	if got := normalizeEvidence(payload); string(got) != string(payload) {
		t.Fatalf("normalizeEvidence() = %s, want %s", got, payload)
	}
}
