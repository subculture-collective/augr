package portfolio

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/google/uuid"
)

func TestAllocatorSelectsAndSizesHighQualityStock(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 19, 15, 0, 0, 0, time.UTC)
	cfg := DefaultAllocatorConfig()
	cfg.Now = func() time.Time { return now }
	state := PortfolioState{
		Equity:        100000,
		BuyingPower:   100000,
		GrossExposure: 1000,
		MarketExposure: map[domain.MarketType]float64{
			domain.MarketTypeStock: 1000,
		},
	}

	res := AllocateShadow([]domain.Opportunity{{
		ID:               uuid.New(),
		StrategyID:       uuid.New(),
		MarketType:       domain.MarketTypeStock,
		Ticker:           "AAPL",
		Status:           domain.OpportunityStatusQueued,
		Confidence:       0.95,
		EdgePct:          0.03,
		LiquidityUSD:     2_000_000,
		SpreadPct:        0.004,
		ProposedNotional: 500,
		MarketCapUSD:     3_000_000_000_000,
		CreatedAt:        now.Add(-30 * time.Minute),
		ExpiresAt:        now.Add(6 * time.Hour),
	}}, state, cfg)

	if len(res.Decisions) != 1 {
		t.Fatalf("decisions len = %d, want 1", len(res.Decisions))
	}
	dec := res.Decisions[0]
	if dec.Action != domain.AllocationDecisionActionShadowSelected {
		t.Fatalf("action = %q, want shadow_selected", dec.Action)
	}
	if dec.Mode != domain.AllocationDecisionModeShadow {
		t.Fatalf("mode = %q, want shadow", dec.Mode)
	}
	if math.Abs(dec.NotionalUSD-2000) > 1e-9 {
		t.Fatalf("notional = %v, want 2000", dec.NotionalUSD)
	}
	if res.Summary.Selected != 1 || res.Summary.Rejected != 0 {
		t.Fatalf("summary = %+v, want 1 selected / 0 rejected", res.Summary)
	}
	if res.Summary.SelectedByMarket[domain.MarketTypeStock.String()] != 1 {
		t.Fatalf("selected by market = %#v, want stock=1", res.Summary.SelectedByMarket)
	}
	if len(dec.Reasons) == 0 {
		t.Fatal("selected decision reasons empty")
	}
}

func TestAllocatorRejectsLowScoreEdgeLiquiditySpread(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 19, 15, 0, 0, 0, time.UTC)
	cfg := DefaultAllocatorConfig()
	cfg.Now = func() time.Time { return now }
	res := AllocateShadow([]domain.Opportunity{{
		ID:               uuid.New(),
		StrategyID:       uuid.New(),
		MarketType:       domain.MarketTypeStock,
		Ticker:           "XYZ",
		Status:           domain.OpportunityStatusQueued,
		Confidence:       0.2,
		EdgePct:          0.005,
		LiquidityUSD:     1_000,
		SpreadPct:        0.03,
		ProposedNotional: 100,
		CreatedAt:        now,
		ExpiresAt:        now.Add(1 * time.Hour),
	}}, PortfolioState{Equity: 100000, BuyingPower: 100000}, cfg)

	if len(res.Decisions) != 1 {
		t.Fatalf("decisions len = %d, want 1", len(res.Decisions))
	}
	dec := res.Decisions[0]
	if dec.Action != domain.AllocationDecisionActionShadowRejected {
		t.Fatalf("action = %q, want shadow_rejected", dec.Action)
	}
	want := map[string]bool{
		reasonBelowMinScore:     true,
		reasonBelowMinEdge:      true,
		reasonBelowMinLiquidity: true,
		reasonAboveMaxSpread:    true,
	}
	for _, reason := range dec.Reasons {
		delete(want, reason)
	}
	if len(want) != 0 {
		t.Fatalf("missing rejection reasons: %#v", want)
	}
}

func TestAllocatorPenalizesDuplicateTickerBelowThreshold(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 19, 15, 0, 0, 0, time.UTC)
	cfg := DefaultAllocatorConfig()
	cfg.Now = func() time.Time { return now }
	state := PortfolioState{
		Equity:         100000,
		BuyingPower:    100000,
		OpenTickers:    map[string]bool{"AAPL": true},
		MarketExposure: map[domain.MarketType]float64{},
	}
	res := AllocateShadow([]domain.Opportunity{{
		ID:               uuid.New(),
		StrategyID:       uuid.New(),
		MarketType:       domain.MarketTypeStock,
		Ticker:           "AAPL",
		Status:           domain.OpportunityStatusQueued,
		Confidence:       0.4,
		EdgePct:          0.015,
		LiquidityUSD:     500_000,
		SpreadPct:        0.009,
		ProposedNotional: 100,
		CreatedAt:        now,
		ExpiresAt:        now.Add(1 * time.Hour),
	}}, state, cfg)

	dec := res.Decisions[0]
	if dec.Action != domain.AllocationDecisionActionShadowRejected {
		t.Fatalf("action = %q, want shadow_rejected", dec.Action)
	}
	if dec.Score >= 65 {
		t.Fatalf("score = %v, want below threshold", dec.Score)
	}
	if !containsReason(dec.Reasons, reasonDuplicateTicker) {
		t.Fatalf("reasons = %#v, want duplicate_ticker", dec.Reasons)
	}
	if !containsReason(dec.Reasons, reasonBelowMinScore) {
		t.Fatalf("reasons = %#v, want below_min_score", dec.Reasons)
	}
}

func TestAllocatorRespectsRunAndDayCaps(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 19, 15, 0, 0, 0, time.UTC)
	cfg := DefaultAllocatorConfig()
	cfg.Now = func() time.Time { return now }
	opps := []domain.Opportunity{
		strongOpportunity("AAA", now, 0.96),
		strongOpportunity("BBB", now, 0.94),
		strongOpportunity("CCC", now, 0.92),
	}

	runLimited := AllocateShadow(opps, PortfolioState{Equity: 100000, BuyingPower: 100000}, cfg)
	if runLimited.Summary.Selected != 2 {
		t.Fatalf("selected = %d, want 2", runLimited.Summary.Selected)
	}
	if !containsReason(runLimited.Decisions[2].Reasons, reasonMaxOrdersPerRun) {
		t.Fatalf("third decision reasons = %#v, want max_orders_per_run", runLimited.Decisions[2].Reasons)
	}

	dayLimited := cfg
	dayLimited.MaxNewOrdersPerRun = 2
	dayLimited.MaxNewOrdersPerDay = 5
	dayLimited.Now = func() time.Time { return now }
	dayLimitedRes := AllocateShadow(opps[:2], PortfolioState{Equity: 100000, BuyingPower: 100000, NewOrdersToday: 4}, dayLimited)
	if dayLimitedRes.Summary.Selected != 1 {
		t.Fatalf("selected = %d, want 1", dayLimitedRes.Summary.Selected)
	}
	if !containsReason(dayLimitedRes.Decisions[1].Reasons, reasonMaxOrdersPerDay) {
		t.Fatalf("second decision reasons = %#v, want max_orders_per_day", dayLimitedRes.Decisions[1].Reasons)
	}
}

func TestAllocatorCapsEventMarketsAtTwentyFive(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 19, 15, 0, 0, 0, time.UTC)
	cfg := DefaultAllocatorConfig()
	cfg.Now = func() time.Time { return now }
	res := AllocateShadow([]domain.Opportunity{{
		ID:               uuid.New(),
		StrategyID:       uuid.New(),
		MarketType:       domain.MarketTypeKalshi,
		Ticker:           "ELECTION",
		Status:           domain.OpportunityStatusQueued,
		Confidence:       0.98,
		EdgePct:          0.12,
		LiquidityUSD:     20_000,
		SpreadPct:        0.01,
		ProposedNotional: 100,
		CreatedAt:        now,
		ExpiresAt:        now.Add(1 * time.Hour),
	}}, PortfolioState{Equity: 100000, BuyingPower: 100000}, cfg)

	if got := res.Decisions[0].NotionalUSD; math.Abs(got-25) > 1e-9 {
		t.Fatalf("notional = %v, want 25", got)
	}
}

func TestAllocatorRejectsExpiredAndNonQueuedDeterministically(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 19, 15, 0, 0, 0, time.UTC)
	cfg := DefaultAllocatorConfig()
	cfg.Now = func() time.Time { return now }
	res := AllocateShadow([]domain.Opportunity{
		strongOpportunity("ACTIVE", now, 0.97),
		{
			ID:               uuid.New(),
			StrategyID:       uuid.New(),
			MarketType:       domain.MarketTypeStock,
			Ticker:           "EXPIRED",
			Status:           domain.OpportunityStatusQueued,
			Confidence:       0.95,
			EdgePct:          0.03,
			LiquidityUSD:     2_000_000,
			SpreadPct:        0.004,
			ProposedNotional: 100,
			CreatedAt:        now.Add(-2 * time.Hour),
			ExpiresAt:        now.Add(-1 * time.Hour),
		},
		{
			ID:               uuid.New(),
			StrategyID:       uuid.New(),
			MarketType:       domain.MarketTypeStock,
			Ticker:           "DONE",
			Status:           domain.OpportunityStatusSelected,
			Confidence:       0.95,
			EdgePct:          0.03,
			LiquidityUSD:     2_000_000,
			SpreadPct:        0.004,
			ProposedNotional: 100,
			CreatedAt:        now,
			ExpiresAt:        now.Add(1 * time.Hour),
		},
	}, PortfolioState{Equity: 100000, BuyingPower: 100000}, cfg)

	if len(res.Decisions) != 3 {
		t.Fatalf("decisions len = %d, want 3", len(res.Decisions))
	}
	if res.Summary.Selected != 1 || res.Summary.Rejected != 2 {
		t.Fatalf("summary = %+v, want 1 selected / 2 rejected", res.Summary)
	}
	if !containsReason(res.Decisions[1].Reasons, reasonExpired) && !containsReason(res.Decisions[2].Reasons, reasonExpired) {
		t.Fatalf("expected one rejected decision with expired reason: %#v", res.Decisions)
	}
	if !containsReason(res.Decisions[1].Reasons, reasonNotQueued) && !containsReason(res.Decisions[2].Reasons, reasonNotQueued) {
		t.Fatalf("expected one rejected decision with not_queued reason: %#v", res.Decisions)
	}
}

func TestAllocatorSizesDefinedRiskVerticalInWholeContracts(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 3, 15, 0, 0, 0, time.UTC)
	cfg := DefaultAllocatorConfig()
	cfg.Now = func() time.Time { return now }
	quoteAt := now.Add(-time.Minute)
	opp := strongOptionOpportunity(now, quoteAt)
	opp.DeploymentBudgetUSD = 1250
	opp.ProposedNotional = 2200
	result := AllocateShadow([]domain.Opportunity{opp}, PortfolioState{
		Equity: 100000, BuyingPower: 100000, OptionsBuyingPower: 100000, AccountSnapshotID: uuid.New(), MarketExposure: map[domain.MarketType]float64{},
	}, cfg)
	if len(result.Decisions) != 1 || result.Decisions[0].Action != domain.AllocationDecisionActionShadowSelected {
		t.Fatalf("result = %+v", result)
	}
	decision := result.Decisions[0]
	if decision.ProposedQuantity != 4 || decision.Quantity != 2 || decision.ReservedRiskUSD != 1000 || decision.ReservedCapitalUSD != 900 || decision.BindingConstraint != "deployment_budget" || decision.ExecutionRoute != "alpaca_mleg" {
		t.Fatalf("defined-risk decision = %+v", decision)
	}
}

func TestAllocatorGreekCapsAllowRiskReducingOptionPackages(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 3, 15, 0, 0, 0, time.UTC)
	cfg := DefaultAllocatorConfig()
	cfg.Now = func() time.Time { return now }
	for name, values := range map[string]struct{ current, perUnit float64 }{
		"reduce negative delta": {-490, 20},
		"reduce positive delta": {490, -20},
	} {
		t.Run(name, func(t *testing.T) {
			opp := strongOptionOpportunity(now, now)
			opp.Delta = values.perUnit
			result := AllocateShadow([]domain.Opportunity{opp}, PortfolioState{
				Equity: 100000, BuyingPower: 100000, OptionsBuyingPower: 100000, AccountSnapshotID: uuid.New(),
				MarketExposure: map[domain.MarketType]float64{}, Delta: values.current,
			}, cfg)
			if result.Decisions[0].Action != domain.AllocationDecisionActionShadowSelected || containsReason(result.Decisions[0].Reasons, reasonGreekLimit) {
				t.Fatalf("risk-reducing package rejected: %+v", result.Decisions[0])
			}
		})
	}
}

func TestAllocatorConsumesMaximumLossAcrossSelectedOptionPackages(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 3, 15, 0, 0, 0, time.UTC)
	cfg := DefaultAllocatorConfig()
	cfg.Now = func() time.Time { return now }
	cfg.TargetGrossExposurePct = .02
	first := strongOptionOpportunity(now, now)
	first.Ticker = "AAA"
	first.DeploymentBudgetUSD = 1500
	second := strongOptionOpportunity(now, now)
	second.Ticker = "BBB"
	second.DeploymentBudgetUSD = 1500
	result := AllocateShadow([]domain.Opportunity{first, second}, PortfolioState{
		Equity: 100000, BuyingPower: 100000, OptionsBuyingPower: 100000, AccountSnapshotID: uuid.New(), MarketExposure: map[domain.MarketType]float64{},
	}, cfg)
	if result.Summary.Selected != 2 {
		t.Fatalf("selected=%d decisions=%+v", result.Summary.Selected, result.Decisions)
	}
	if result.Decisions[0].ExposureBeforeUSD != 0 || result.Decisions[0].ExposureAfterUSD != 1500 ||
		result.Decisions[1].ExposureBeforeUSD != 1500 || result.Decisions[1].ExposureAfterUSD != 2000 {
		t.Fatalf("maximum-loss exposure was not consumed sequentially: %+v", result.Decisions)
	}
}

func TestAllocatorRejectsUnsafeOptionEvidenceAndRiskState(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 3, 15, 0, 0, 0, time.UTC)
	cfg := DefaultAllocatorConfig()
	cfg.Now = func() time.Time { return now }
	oldQuote := now.Add(-10 * time.Minute)
	opp := strongOptionOpportunity(now, oldQuote)
	opp.OptionLegs = opp.OptionLegs[:1]
	opp.MaxLossPerUnit = 0
	opp.RiskPolicyVersion = "wrong@sha256:" + strings.Repeat("0", 64)
	result := AllocateShadow([]domain.Opportunity{opp}, PortfolioState{
		Equity: 100000, BuyingPower: 100000, AccountSnapshotID: uuid.New(), CircuitBreakerOpen: true,
	}, cfg)
	decision := result.Decisions[0]
	for _, reason := range []string{reasonCircuitBreakerOpen, reasonRiskPolicyMismatch, reasonUndefinedOptionRisk, reasonUnsupportedOptionPackage, reasonStaleOptionQuote} {
		if !containsReason(decision.Reasons, reason) {
			t.Fatalf("reasons=%v missing %s", decision.Reasons, reason)
		}
	}
}

func TestValidVerticalAcceptsOnlyAtomicOneToOneSameExpiryPackage(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 3, 15, 0, 0, 0, time.UTC)
	base := strongOptionOpportunity(now, now).OptionLegs
	if !validVertical(base) {
		t.Fatal("valid bull call rejected")
	}
	mutations := []func([]domain.OpportunityOptionLeg){
		func(legs []domain.OpportunityOptionLeg) { legs[1].Ratio = 2 },
		func(legs []domain.OpportunityOptionLeg) { legs[1].Expiry = legs[1].Expiry.AddDate(0, 1, 0) },
		func(legs []domain.OpportunityOptionLeg) { legs[1].Underlying = "QQQ" },
		func(legs []domain.OpportunityOptionLeg) { legs[1].PositionIntent = "sell_to_close" },
		func(legs []domain.OpportunityOptionLeg) { legs[1].Side = domain.OrderSideBuy },
	}
	for index, mutate := range mutations {
		legs := append([]domain.OpportunityOptionLeg(nil), base...)
		mutate(legs)
		if validVertical(legs) {
			t.Fatalf("unsafe mutation %d accepted: %+v", index, legs)
		}
	}
}

func strongOptionOpportunity(now, quoteAt time.Time) domain.Opportunity {
	policy, _ := ReviewedPortfolioRiskPolicyV1()
	expiry := time.Date(2026, 10, 16, 20, 0, 0, 0, time.UTC)
	return domain.Opportunity{
		ID: uuid.New(), StrategyID: uuid.New(), MarketType: domain.MarketTypeOptions, Ticker: "SPY",
		Side:   domain.OrderSideBuy,
		Status: domain.OpportunityStatusQueued, Confidence: .98, EdgePct: .04, LiquidityUSD: 100000,
		SpreadPct: .02, ProposedNotional: 1000, MaxLossPerUnit: 500, RequiredCapitalUnit: 450,
		QuoteObservedAt: &quoteAt, Delta: 20, Gamma: 2, Theta: -5, Vega: 10, RiskPolicyVersion: policy.Reference(),
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		OptionLegs: []domain.OpportunityOptionLeg{
			{Sequence: 0, ContractID: uuid.New(), OCCSymbol: "SPY261016C00500000", Underlying: "SPY", Expiry: expiry, OptionType: "call", Strike: 500, Ratio: 1, Side: domain.OrderSideBuy, PositionIntent: "buy_to_open", Bid: 10, Ask: 10.2, Multiplier: 100},
			{Sequence: 1, ContractID: uuid.New(), OCCSymbol: "SPY261016C00505000", Underlying: "SPY", Expiry: expiry, OptionType: "call", Strike: 505, Ratio: 1, Side: domain.OrderSideSell, PositionIntent: "sell_to_open", Bid: 5.5, Ask: 5.7, Multiplier: 100},
		},
	}
}

func strongOpportunity(ticker string, now time.Time, confidence float64) domain.Opportunity {
	return domain.Opportunity{
		ID:               uuid.New(),
		StrategyID:       uuid.New(),
		MarketType:       domain.MarketTypeStock,
		Ticker:           ticker,
		Status:           domain.OpportunityStatusQueued,
		Confidence:       confidence,
		EdgePct:          0.03,
		LiquidityUSD:     2_000_000,
		SpreadPct:        0.004,
		ProposedNotional: 100,
		MarketCapUSD:     3_000_000_000_000,
		CreatedAt:        now,
		ExpiresAt:        now.Add(1 * time.Hour),
	}
}

func containsReason(reasons []string, reason string) bool {
	for _, r := range reasons {
		if r == reason {
			return true
		}
	}
	return false
}
