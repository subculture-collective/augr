package portfolio

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/google/uuid"
)

func TestAllocatorModeOwnsExecutionOnlyInPaperMode(t *testing.T) {
	t.Parallel()
	if !AllocatorModePaper.OwnsExecution() {
		t.Fatal("paper mode must own execution")
	}
	for _, mode := range []AllocatorMode{AllocatorModeShadow, AllocatorModeQueue, AllocatorModeDiagnostics, ""} {
		if mode.OwnsExecution() {
			t.Fatalf("mode %q must not own execution", mode)
		}
	}
}

func TestStockLiquidityUnknownScoresNeutrallyWithWarning(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 19, 15, 0, 0, 0, time.UTC)
	cfg := DefaultAllocatorConfig()
	cfg.Now = func() time.Time { return now }
	opp := strongOpportunity("AAPL", now, 0.95)
	opp.LiquidityUSD = 0
	opp.Evidence = json.RawMessage(`{"entry_type":"market"}`)

	res := AllocateShadow([]domain.Opportunity{opp}, PortfolioState{Equity: 100000, BuyingPower: 100000}, cfg)
	if res.Summary.Selected != 1 {
		t.Fatalf("expected selection without liquidity evidence, got %+v", res.Decisions[0].Reasons)
	}
	if !containsReason(res.Decisions[0].Reasons, WarningLiquidityUnknown) {
		t.Fatalf("expected %s warning, got %v", WarningLiquidityUnknown, res.Decisions[0].Reasons)
	}
	if containsReason(res.Decisions[0].Reasons, reasonBelowMinLiquidity) {
		t.Fatalf("unknown liquidity must not hard-reject: %v", res.Decisions[0].Reasons)
	}
}

func TestStockLiquidityDerivedFromEvidenceVolume(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 19, 15, 0, 0, 0, time.UTC)
	cfg := DefaultAllocatorConfig()
	cfg.Now = func() time.Time { return now }
	opp := strongOpportunity("AAPL", now, 0.95)
	opp.LiquidityUSD = 0
	opp.EntryPrice = 100
	// 1,000 shares at $100 = $100k, below the 500k stock floor.
	opp.Evidence = json.RawMessage(`{"features":{"average_volume":1000}}`)

	res := AllocateShadow([]domain.Opportunity{opp}, PortfolioState{Equity: 100000, BuyingPower: 100000}, cfg)
	if !containsReason(res.Decisions[0].Reasons, reasonBelowMinLiquidity) {
		t.Fatalf("derived liquidity should hard-reject below the floor: %v", res.Decisions[0].Reasons)
	}
	if containsReason(res.Decisions[0].Reasons, WarningLiquidityUnknown) {
		t.Fatalf("derived liquidity must not be flagged unknown: %v", res.Decisions[0].Reasons)
	}
	if got := LiquidityUSDFromEvidence(json.RawMessage(`{"avg_dollar_volume":"2500000"}`), 0); got != 2_500_000 {
		t.Fatalf("usd key = %v, want 2500000", got)
	}
	if got := LiquidityUSDFromEvidence(json.RawMessage(`not json`), 10); got != 0 {
		t.Fatalf("invalid evidence = %v, want 0", got)
	}
}

func TestCryptoQuantityIsFractional(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 19, 15, 0, 0, 0, time.UTC)
	cfg := DefaultAllocatorConfig()
	cfg.Now = func() time.Time { return now }
	opp := strongOpportunity("BTC", now, 0.98)
	opp.MarketType = domain.MarketTypeCrypto
	opp.EdgePct = 0.05
	opp.EntryPrice = 60000
	opp.ProposedNotional = 900
	opp.MaxLossPct = 0.05

	res := AllocateShadow([]domain.Opportunity{opp}, PortfolioState{Equity: 100000, BuyingPower: 100000}, cfg)
	if res.Summary.Selected != 1 {
		t.Fatalf("crypto should size fractionally, got %+v", res.Decisions[0].Reasons)
	}
	quantity := res.Decisions[0].Quantity
	if quantity <= 0 || quantity >= 1 || quantity != math.Floor(quantity*1e6)/1e6 {
		t.Fatalf("quantity = %v, want fractional rounded to 6 decimals", quantity)
	}
	if math.Abs(res.Decisions[0].NotionalUSD-quantity*60000) > 1e-6 {
		t.Fatalf("notional %v does not equal quantity*price", res.Decisions[0].NotionalUSD)
	}
	stock := strongOpportunity("AAPL", now, 0.98)
	stock.EntryPrice = 60000
	stock.ProposedNotional = 900
	res = AllocateShadow([]domain.Opportunity{stock}, PortfolioState{Equity: 100000, BuyingPower: 100000}, cfg)
	if res.Summary.Selected != 0 || !containsReason(res.Decisions[0].Reasons, reasonSizingZero) {
		t.Fatalf("stock must keep whole units: %+v", res.Decisions[0])
	}
}

func TestInternalAccountSizesDefinedRiskOptionsFromCash(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 3, 15, 0, 0, 0, time.UTC)
	cfg := DefaultAllocatorConfig()
	cfg.Now = func() time.Time { return now }
	opp := strongOptionOpportunity(now, now)
	opp.RequiredCapitalUnit = opp.MaxLossPerUnit
	baseState := PortfolioState{Equity: 100000, BuyingPower: 100000, OptionsBuyingPower: 0, AccountSnapshotID: uuid.New(), MarketExposure: map[domain.MarketType]float64{}}

	external := AllocateShadow([]domain.Opportunity{opp}, baseState, cfg)
	if external.Summary.Selected != 0 || !containsReason(external.Decisions[0].Reasons, reasonOptionsMarginUnsupported) {
		t.Fatalf("external account without options margin must reject explicitly: %v", external.Decisions[0].Reasons)
	}
	if containsReason(external.Decisions[0].Reasons, reasonSizingZero) {
		t.Fatalf("explicit margin rejection must not be reported as sizing_zero: %v", external.Decisions[0].Reasons)
	}

	internalState := baseState
	internalState.InternalAccount = true
	internal := AllocateShadow([]domain.Opportunity{opp}, internalState, cfg)
	if internal.Summary.Selected != 1 {
		t.Fatalf("internal account should size defined-risk package from cash: %v", internal.Decisions[0].Reasons)
	}
	if internal.Decisions[0].Quantity < 1 || internal.Decisions[0].ReservedCapitalUSD <= 0 {
		t.Fatalf("expected positive contracts, got %+v", internal.Decisions[0])
	}

	undefined := opp
	undefined.RequiredCapitalUnit = opp.MaxLossPerUnit * 2
	res := AllocateShadow([]domain.Opportunity{undefined}, internalState, cfg)
	if res.Summary.Selected != 0 || !containsReason(res.Decisions[0].Reasons, reasonOptionsMarginUnsupported) {
		t.Fatalf("non defined-risk package on internal account must reject: %v", res.Decisions[0].Reasons)
	}
}

func TestOptionQuoteFreshnessIsSessionAware(t *testing.T) {
	t.Parallel()
	maxAge := 5 * time.Minute
	// Friday 2026-09-04 15:00 ET = 19:00 UTC (regular session).
	inSession := time.Date(2026, 9, 4, 19, 0, 0, 0, time.UTC)
	stale := inSession.Add(-time.Hour)
	if fresh, _ := quoteFreshness(&stale, inSession, maxAge); fresh {
		t.Fatal("hour-old quote during the session must be stale")
	}
	live := inSession.Add(-time.Minute)
	if fresh, prior := quoteFreshness(&live, inSession, maxAge); !fresh || prior {
		t.Fatal("minute-old quote during the session must be fresh and live")
	}
	// Friday 20:00 ET = Saturday 00:00 UTC, after the close.
	afterHours := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	lastSession := time.Date(2026, 9, 4, 19, 45, 0, 0, time.UTC) // 15:45 ET
	if fresh, prior := quoteFreshness(&lastSession, afterHours, maxAge); !fresh || !prior {
		t.Fatalf("quote from the last regular session must be accepted after hours (fresh=%v prior=%v)", fresh, prior)
	}
	preOpen := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC) // 08:00 ET, before the session
	if fresh, _ := quoteFreshness(&preOpen, afterHours, maxAge); fresh {
		t.Fatal("pre-open quote must not count as last-session")
	}
	// Saturday 20:00 ET is 28h after Friday 16:00 ET, beyond the 20h bound.
	saturdayEvening := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	if fresh, _ := quoteFreshness(&lastSession, saturdayEvening, maxAge); fresh {
		t.Fatal("quote older than 20h must be stale even outside the session")
	}
	future := afterHours.Add(time.Minute)
	if fresh, _ := quoteFreshness(&future, afterHours, maxAge); fresh {
		t.Fatal("future quote must be stale")
	}
	if fresh, _ := quoteFreshness(nil, afterHours, maxAge); fresh {
		t.Fatal("missing quote must be stale")
	}
}

func TestAllocatorTagsPriorSessionQuoteAfterHours(t *testing.T) {
	t.Parallel()
	afterHours := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	quoteAt := time.Date(2026, 9, 4, 19, 45, 0, 0, time.UTC)
	cfg := DefaultAllocatorConfig()
	cfg.Now = func() time.Time { return afterHours }
	opp := strongOptionOpportunity(afterHours, quoteAt)
	opp.CreatedAt = quoteAt
	opp.ExpiresAt = afterHours.Add(6 * time.Hour)
	res := AllocateShadow([]domain.Opportunity{opp}, PortfolioState{
		Equity: 100000, BuyingPower: 100000, OptionsBuyingPower: 100000, AccountSnapshotID: uuid.New(), MarketExposure: map[domain.MarketType]float64{},
	}, cfg)
	if res.Summary.Selected != 1 {
		t.Fatalf("prior-session quote should be selectable after hours: %v", res.Decisions[0].Reasons)
	}
	if !containsReason(res.Decisions[0].Reasons, WarningQuoteFromPriorSession) {
		t.Fatalf("expected %s tag, got %v", WarningQuoteFromPriorSession, res.Decisions[0].Reasons)
	}
}

func TestRiskPolicyMismatchIsReportedOnce(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 3, 15, 0, 0, 0, time.UTC)
	cfg := DefaultAllocatorConfig()
	opp := strongOptionOpportunity(now, now)
	opp.RiskPolicyVersion = "other"
	reasons, _ := portfolioRiskRejectionReasons(opp, PortfolioState{Equity: 1, BuyingPower: 1, OptionsBuyingPower: 1, AccountSnapshotID: uuid.New()}, cfg, now)
	count := 0
	for _, reason := range reasons {
		if reason == reasonRiskPolicyMismatch {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("risk_policy_mismatch reported %d times: %v", count, reasons)
	}
	stock := strongOpportunity("AAPL", now, 0.9)
	reasons, _ = portfolioRiskRejectionReasons(stock, PortfolioState{}, cfg, now)
	if containsReason(reasons, reasonRiskPolicyMismatch) {
		t.Fatalf("stock without a policy version must not mismatch: %v", reasons)
	}
}

func TestCapitalLadderStepScalesSizing(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 19, 15, 0, 0, 0, time.UTC)
	cfg := DefaultAllocatorConfig()
	cfg.Now = func() time.Time { return now }
	opp := strongOpportunity("AAPL", now, 0.98)
	opp.ProposedNotional = 0
	opp.MarketCapUSD = 0
	opp.EntryPrice = 1
	full := AllocateShadow([]domain.Opportunity{opp}, PortfolioState{Equity: 100000, BuyingPower: 100000}, cfg)
	stepped := AllocateShadow([]domain.Opportunity{opp}, PortfolioState{Equity: 100000, BuyingPower: 100000, StrategyStepPct: map[uuid.UUID]float64{opp.StrategyID: 0.25}}, cfg)
	if full.Summary.Selected != 1 || stepped.Summary.Selected != 1 {
		t.Fatalf("expected both selections: %+v %+v", full.Decisions[0].Reasons, stepped.Decisions[0].Reasons)
	}
	if math.Abs(stepped.Decisions[0].NotionalUSD-full.Decisions[0].NotionalUSD*0.25) > 1e-6 {
		t.Fatalf("stepped notional %v, want quarter of %v", stepped.Decisions[0].NotionalUSD, full.Decisions[0].NotionalUSD)
	}
	if !containsReason(stepped.Decisions[0].Reasons, WarningCapitalLadderStepApply) {
		t.Fatalf("expected ladder tag: %v", stepped.Decisions[0].Reasons)
	}
}

func TestPositionsMissingMarkWarningIsRecorded(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 19, 15, 0, 0, 0, time.UTC)
	cfg := DefaultAllocatorConfig()
	cfg.Now = func() time.Time { return now }
	opp := strongOpportunity("AAPL", now, 0.98)
	res := AllocateShadow([]domain.Opportunity{opp}, PortfolioState{Equity: 100000, BuyingPower: 100000, PositionsMissingMark: 2}, cfg)
	if res.Summary.Selected != 1 || !containsReason(res.Decisions[0].Reasons, WarningPositionsMissingMark) {
		t.Fatalf("expected selection with %s warning: %+v", WarningPositionsMissingMark, res.Decisions[0])
	}
}

func TestEventMarketMaxNotionalDefault(t *testing.T) {
	t.Parallel()
	cfg := DefaultAllocatorConfig()
	if got := cfg.EventMarketMaxNotional(10000); got != 25 {
		t.Fatalf("small equity cap = %v, want 25", got)
	}
	if got := cfg.EventMarketMaxNotional(1_000_000); got != 1000 {
		t.Fatalf("large equity cap = %v, want 1000", got)
	}
	if applyAllocatorDefaults(AllocatorConfig{Mode: AllocatorModePaper}).DrawdownWindowDays != DefaultDrawdownWindowDays {
		t.Fatal("drawdown window default not applied")
	}
}

func TestApproxEqualUsesRelativeTolerance(t *testing.T) {
	t.Parallel()
	if !approxEqual(1000, 1000.0000005) {
		t.Fatal("values within 1e-6 relative must be equal")
	}
	if approxEqual(1000, 1000.01) {
		t.Fatal("values beyond tolerance must differ")
	}
	if !approxEqual(0, 0) || approxEqual(0, 1e-9) {
		t.Fatal("zero handling incorrect")
	}
}
