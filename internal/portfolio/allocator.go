package portfolio

import (
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/google/uuid"
)

type AllocatorMode string

const (
	AllocatorModeDiagnostics AllocatorMode = "diagnostics"
	AllocatorModeQueue       AllocatorMode = "queue"
	AllocatorModeShadow      AllocatorMode = "shadow"
	AllocatorModePaper       AllocatorMode = "paper"
)

type AllocatorConfig struct {
	Mode                   AllocatorMode
	PaperOnly              bool
	TargetGrossExposurePct float64
	HardGrossExposurePct   float64
	CashReservePct         float64
	MaxNewOrdersPerRun     int
	MaxNewOrdersPerDay     int
	MaxPerPositionPct      map[domain.MarketType]float64
	MaxPerMarketPct        map[domain.MarketType]float64
	MinScoreByMarket       map[domain.MarketType]float64
	MinEdgePctByMarket     map[domain.MarketType]float64
	MinLiquidityUSD        map[domain.MarketType]float64
	MaxSpreadPct           map[domain.MarketType]float64
	RiskPolicyVersion      string
	MaxPositionRiskPct     float64
	MaxDailyLossPct        float64
	MaxDrawdownPct         float64
	MaxOpenPositions       int
	MaxQuoteAge            time.Duration
	MaxAbsoluteDelta       float64
	MaxAbsoluteGamma       float64
	MaxAbsoluteTheta       float64
	MaxAbsoluteVega        float64
	Now                    func() time.Time
}

type PortfolioState struct {
	Equity                 float64
	BuyingPower            float64
	OptionsBuyingPower     float64
	GrossExposure          float64
	MarketExposure         map[domain.MarketType]float64
	OpenTickers            map[string]bool
	NewOrdersToday         int
	AccountSnapshotID      uuid.UUID
	AccountBalanceFallback bool
	DailyLossPct           float64
	DrawdownPct            float64
	OpenPositionCount      int
	CircuitBreakerOpen     bool
	UnderlyingRisk         map[string]float64
	Delta                  float64
	Gamma                  float64
	Theta                  float64
	Vega                   float64
	ReconciliationID       string
	RiskStateSHA256        string
	RiskStateBytes         []byte
}

type AllocationSummary struct {
	Evaluated        int            `json:"evaluated"`
	Eligible         int            `json:"eligible"`
	Selected         int            `json:"selected"`
	Rejected         int            `json:"rejected"`
	RejectedByReason map[string]int `json:"rejected_by_reason"`
	SelectedByMarket map[string]int `json:"selected_by_market"`
	SelectedNotional float64        `json:"selected_notional_usd"`
}

type AllocationResult struct {
	Decisions []domain.AllocationDecision `json:"decisions"`
	Summary   AllocationSummary           `json:"summary"`
}

type scoredOpportunity struct {
	opp            domain.Opportunity
	score          float64
	reasons        []string
	notional       float64
	quantity       float64
	binding        string
	caps           []domain.AllocationRiskCap
	exposureBefore float64
	exposureAfter  float64
	action         domain.AllocationDecisionAction
}

const (
	reasonNotQueued                = "not_queued"
	reasonExpired                  = "expired"
	reasonUndefinedOptionRisk      = "undefined_option_maximum_loss"
	reasonUndefinedStockRisk       = "undefined_stock_maximum_loss"
	reasonUnsupportedOptionPackage = "unsupported_option_package"
	reasonStaleOptionQuote         = "stale_option_quote"
	reasonRiskPolicyMismatch       = "risk_policy_mismatch"
	reasonAccountBalanceFallback   = "account_balance_fallback"
	reasonCircuitBreakerOpen       = "circuit_breaker_open"
	reasonDailyLossLimit           = "daily_loss_limit"
	reasonDrawdownLimit            = "drawdown_limit"
	reasonOpenPositionLimit        = "open_position_limit"
	reasonGreekLimit               = "greek_limit"
	reasonMissingAccountSnapshot   = "missing_account_snapshot"
	reasonInsufficientBuyingPower  = "insufficient_buying_power"
	reasonBelowMinScore            = "below_min_score"
	reasonBelowMinEdge             = "below_min_edge"
	reasonBelowMinLiquidity        = "below_min_liquidity"
	reasonAboveMaxSpread           = "above_max_spread"
	reasonDuplicateTicker          = "duplicate_ticker"
	reasonNearMarketCap            = "near_market_cap"
	reasonCashReservePressure      = "cash_reserve_pressure"
	reasonTargetExposureExceeded   = "target_gross_exposure_exceeded"
	reasonHardExposureExceeded     = "hard_gross_exposure_exceeded"
	reasonMarketExposureExceeded   = "market_exposure_exceeded"
	reasonCashReserveInsufficient  = "cash_reserve_insufficient"
	reasonMaxOrdersPerRun          = "max_orders_per_run"
	reasonMaxOrdersPerDay          = "max_orders_per_day"
	reasonSizingZero               = "sizing_zero"
	reasonBudgetClamped            = "budget_clamped"
)

func DefaultAllocatorConfig() AllocatorConfig {
	policy, err := ReviewedPortfolioRiskPolicyV1()
	if err != nil {
		panic(err)
	}
	return AllocatorConfig{
		Mode:                   AllocatorModeShadow,
		PaperOnly:              true,
		TargetGrossExposurePct: 0.35,
		HardGrossExposurePct:   0.50,
		CashReservePct:         0.20,
		MaxNewOrdersPerRun:     2,
		MaxNewOrdersPerDay:     5,
		MaxPerPositionPct: map[domain.MarketType]float64{
			domain.MarketTypeStock:      0.02,
			domain.MarketTypeCrypto:     0.01,
			domain.MarketTypeKalshi:     0.01,
			domain.MarketTypePolymarket: 0.01,
			domain.MarketTypeOptions:    policy.MaxPositionRiskPct,
		},
		MaxPerMarketPct: map[domain.MarketType]float64{
			domain.MarketTypeStock:      0.50,
			domain.MarketTypeCrypto:     0.05,
			domain.MarketTypeKalshi:     0.10,
			domain.MarketTypePolymarket: 0.05,
			domain.MarketTypeOptions:    policy.MaxOptionsMarketRiskPct,
		},
		MinScoreByMarket: map[domain.MarketType]float64{
			domain.MarketTypeStock:      65,
			domain.MarketTypeCrypto:     70,
			domain.MarketTypeKalshi:     70,
			domain.MarketTypePolymarket: 75,
			domain.MarketTypeOptions:    65,
		},
		MinEdgePctByMarket: map[domain.MarketType]float64{
			domain.MarketTypeStock:      0.015,
			domain.MarketTypeCrypto:     0.03,
			domain.MarketTypeKalshi:     0.04,
			domain.MarketTypePolymarket: 0.05,
			domain.MarketTypeOptions:    0.015,
		},
		MinLiquidityUSD: map[domain.MarketType]float64{
			domain.MarketTypeStock:      500000,
			domain.MarketTypeCrypto:     100000,
			domain.MarketTypeKalshi:     1000,
			domain.MarketTypePolymarket: 2500,
			domain.MarketTypeOptions:    policy.MinOptionLiquidityUSD,
		},
		MaxSpreadPct: map[domain.MarketType]float64{
			domain.MarketTypeStock:      0.01,
			domain.MarketTypeCrypto:     0.02,
			domain.MarketTypeKalshi:     0.08,
			domain.MarketTypePolymarket: 0.08,
			domain.MarketTypeOptions:    policy.MaxOptionSpreadPct,
		},
		RiskPolicyVersion: policy.Reference(), MaxPositionRiskPct: policy.MaxPositionRiskPct,
		MaxDailyLossPct: policy.MaxDailyLossPct, MaxDrawdownPct: policy.MaxDrawdownPct,
		MaxOpenPositions: policy.MaxOpenPositions, MaxQuoteAge: policy.MaxQuoteAge(),
		MaxAbsoluteDelta: policy.MaxAbsoluteDelta, MaxAbsoluteGamma: policy.MaxAbsoluteGamma,
		MaxAbsoluteTheta: policy.MaxAbsoluteTheta, MaxAbsoluteVega: policy.MaxAbsoluteVega,
		Now: time.Now,
	}
}

func AllocateShadow(opportunities []domain.Opportunity, state PortfolioState, cfg AllocatorConfig) AllocationResult {
	cfg = applyAllocatorDefaults(cfg)
	now := cfg.Now()

	result := AllocationResult{
		Summary: AllocationSummary{
			RejectedByReason: map[string]int{},
			SelectedByMarket: map[string]int{},
		},
	}

	eligible := make([]scoredOpportunity, 0, len(opportunities))
	decisions := make([]scoredOpportunity, 0, len(opportunities))

	for _, opp := range opportunities {
		result.Summary.Evaluated++

		if !strings.EqualFold(opp.Status.String(), domain.OpportunityStatusQueued.String()) {
			decisions = append(decisions, scoredOpportunity{opp: opp, score: -1, reasons: []string{reasonNotQueued}, action: domain.AllocationDecisionActionShadowRejected})
			incrementStringReason(result.Summary.RejectedByReason, reasonNotQueued)
			result.Summary.Rejected++
			continue
		}
		if isExpired(opp.ExpiresAt, now) {
			decisions = append(decisions, scoredOpportunity{opp: opp, score: -1, reasons: []string{reasonExpired}, action: domain.AllocationDecisionActionShadowRejected})
			incrementStringReason(result.Summary.RejectedByReason, reasonExpired)
			result.Summary.Rejected++
			continue
		}

		result.Summary.Eligible++
		score, reasons := scoreOpportunity(opp, state, cfg, now)
		reasons = append(reasons, portfolioRiskRejectionReasons(opp, state, cfg, now)...)
		minScore := lookup(cfg.MinScoreByMarket, opp.MarketType)
		minEdge := lookup(cfg.MinEdgePctByMarket, opp.MarketType)
		minLiquidity := lookup(cfg.MinLiquidityUSD, opp.MarketType)
		maxSpread := lookup(cfg.MaxSpreadPct, opp.MarketType)

		if score < minScore {
			reasons = append(reasons, reasonBelowMinScore)
		}
		if opp.EdgePct < minEdge {
			reasons = append(reasons, reasonBelowMinEdge)
		}
		if opp.LiquidityUSD < minLiquidity {
			reasons = append(reasons, reasonBelowMinLiquidity)
		}
		if maxSpread > 0 && opp.SpreadPct > maxSpread {
			reasons = append(reasons, reasonAboveMaxSpread)
		}

		if len(reasons) > 0 {
			decisions = append(decisions, scoredOpportunity{opp: opp, score: score, reasons: uniqueStrings(reasons), action: domain.AllocationDecisionActionShadowRejected})
			result.Summary.Rejected++
			for _, reason := range uniqueStrings(reasons) {
				incrementStringReason(result.Summary.RejectedByReason, reason)
			}
			continue
		}

		eligible = append(eligible, scoredOpportunity{opp: opp, score: score})
	}

	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].score != eligible[j].score {
			return eligible[i].score > eligible[j].score
		}
		if eligible[i].opp.Ticker != eligible[j].opp.Ticker {
			return strings.ToLower(eligible[i].opp.Ticker) < strings.ToLower(eligible[j].opp.Ticker)
		}
		return eligible[i].opp.ID.String() < eligible[j].opp.ID.String()
	})

	selectedCount := 0
	selectedNotional := 0.0
	for _, item := range eligible {
		reasons := make([]string, 0, 4)
		if cfg.MaxNewOrdersPerRun > 0 && selectedCount >= cfg.MaxNewOrdersPerRun {
			reasons = append(reasons, reasonMaxOrdersPerRun)
		}
		if cfg.MaxNewOrdersPerDay > 0 && state.NewOrdersToday+selectedCount >= cfg.MaxNewOrdersPerDay {
			reasons = append(reasons, reasonMaxOrdersPerDay)
		}
		if len(reasons) == 0 {
			item.exposureBefore = state.GrossExposure
			notional, quantity, binding, caps, sizingReasons := sizeOpportunity(item.opp, item.score, state, cfg)
			reasons = append(reasons, sizingReasons...)
			if notional > 0 {
				item.notional = notional
				item.quantity = quantity
				item.binding = binding
				item.caps = caps
				item.action = domain.AllocationDecisionActionShadowSelected
				item.reasons = append([]string{fmt.Sprintf("score=%.1f", item.score), fmt.Sprintf("multiplier=%.2f", scoreMultiplier(item.score))}, reasons...)
				riskConsumed := notional
				if item.opp.MarketType == domain.MarketTypeOptions {
					riskConsumed = quantity * item.opp.MaxLossPerUnit
					state.OptionsBuyingPower = math.Max(0, state.OptionsBuyingPower-notional)
					if state.UnderlyingRisk == nil {
						state.UnderlyingRisk = make(map[string]float64)
					}
					state.UnderlyingRisk[strings.ToUpper(item.opp.Ticker)] += riskConsumed
					state.Delta += quantity * item.opp.Delta
					state.Gamma += quantity * item.opp.Gamma
					state.Theta += quantity * item.opp.Theta
					state.Vega += quantity * item.opp.Vega
				}
				state.GrossExposure += riskConsumed
				if state.MarketExposure == nil {
					state.MarketExposure = make(map[domain.MarketType]float64)
				}
				state.MarketExposure[item.opp.MarketType] += riskConsumed
				state.BuyingPower = math.Max(0, state.BuyingPower-notional)
				item.exposureAfter = state.GrossExposure
				selectedCount++
				selectedNotional += notional
				result.Summary.Selected++
				result.Summary.SelectedNotional += notional
				incrementMarket(result.Summary.SelectedByMarket, item.opp.MarketType)
				decisions = append(decisions, item)
				continue
			}
			reasons = append(reasons, reasonSizingZero)
		}
		decisions = append(decisions, scoredOpportunity{opp: item.opp, score: item.score, reasons: uniqueStrings(reasons), action: domain.AllocationDecisionActionShadowRejected})
		result.Summary.Rejected++
		for _, reason := range uniqueStrings(reasons) {
			incrementStringReason(result.Summary.RejectedByReason, reason)
		}
	}

	sort.SliceStable(decisions, func(i, j int) bool {
		if decisions[i].score != decisions[j].score {
			return decisions[i].score > decisions[j].score
		}
		if decisions[i].action != decisions[j].action {
			return decisions[i].action == domain.AllocationDecisionActionShadowSelected
		}
		if decisions[i].opp.Ticker != decisions[j].opp.Ticker {
			return strings.ToLower(decisions[i].opp.Ticker) < strings.ToLower(decisions[j].opp.Ticker)
		}
		return decisions[i].opp.ID.String() < decisions[j].opp.ID.String()
	})

	result.Decisions = make([]domain.AllocationDecision, 0, len(decisions))
	for _, item := range decisions {
		result.Decisions = append(result.Decisions, toAllocationDecision(item))
	}
	result.Summary.SelectedNotional = selectedNotional
	return result
}

func applyAllocatorDefaults(cfg AllocatorConfig) AllocatorConfig {
	defaults := DefaultAllocatorConfig()
	if isZeroAllocatorConfig(cfg) {
		return defaults
	}

	if cfg.Mode == "" {
		cfg.Mode = defaults.Mode
	}
	if cfg.Now == nil {
		cfg.Now = defaults.Now
	}
	if cfg.RiskPolicyVersion == "" {
		cfg.RiskPolicyVersion = defaults.RiskPolicyVersion
	}
	if cfg.MaxPositionRiskPct == 0 {
		cfg.MaxPositionRiskPct = defaults.MaxPositionRiskPct
	}
	if cfg.MaxDailyLossPct == 0 {
		cfg.MaxDailyLossPct = defaults.MaxDailyLossPct
	}
	if cfg.MaxDrawdownPct == 0 {
		cfg.MaxDrawdownPct = defaults.MaxDrawdownPct
	}
	if cfg.MaxOpenPositions == 0 {
		cfg.MaxOpenPositions = defaults.MaxOpenPositions
	}
	if cfg.MaxQuoteAge == 0 {
		cfg.MaxQuoteAge = defaults.MaxQuoteAge
	}
	if cfg.MaxAbsoluteDelta == 0 {
		cfg.MaxAbsoluteDelta = defaults.MaxAbsoluteDelta
	}
	if cfg.MaxAbsoluteGamma == 0 {
		cfg.MaxAbsoluteGamma = defaults.MaxAbsoluteGamma
	}
	if cfg.MaxAbsoluteTheta == 0 {
		cfg.MaxAbsoluteTheta = defaults.MaxAbsoluteTheta
	}
	if cfg.MaxAbsoluteVega == 0 {
		cfg.MaxAbsoluteVega = defaults.MaxAbsoluteVega
	}
	if cfg.TargetGrossExposurePct == 0 {
		cfg.TargetGrossExposurePct = defaults.TargetGrossExposurePct
	}
	if cfg.HardGrossExposurePct == 0 {
		cfg.HardGrossExposurePct = defaults.HardGrossExposurePct
	}
	if cfg.CashReservePct == 0 {
		cfg.CashReservePct = defaults.CashReservePct
	}
	if cfg.MaxNewOrdersPerRun == 0 {
		cfg.MaxNewOrdersPerRun = defaults.MaxNewOrdersPerRun
	}
	if cfg.MaxNewOrdersPerDay == 0 {
		cfg.MaxNewOrdersPerDay = defaults.MaxNewOrdersPerDay
	}
	cfg.MaxPerPositionPct = mergeMarketMap(cfg.MaxPerPositionPct, defaults.MaxPerPositionPct)
	cfg.MaxPerMarketPct = mergeMarketMap(cfg.MaxPerMarketPct, defaults.MaxPerMarketPct)
	cfg.MinScoreByMarket = mergeMarketMap(cfg.MinScoreByMarket, defaults.MinScoreByMarket)
	cfg.MinEdgePctByMarket = mergeMarketMap(cfg.MinEdgePctByMarket, defaults.MinEdgePctByMarket)
	cfg.MinLiquidityUSD = mergeMarketMap(cfg.MinLiquidityUSD, defaults.MinLiquidityUSD)
	cfg.MaxSpreadPct = mergeMarketMap(cfg.MaxSpreadPct, defaults.MaxSpreadPct)
	return cfg
}

func isZeroAllocatorConfig(cfg AllocatorConfig) bool {
	return cfg.Mode == "" && !cfg.PaperOnly && cfg.TargetGrossExposurePct == 0 && cfg.HardGrossExposurePct == 0 && cfg.CashReservePct == 0 && cfg.MaxNewOrdersPerRun == 0 && cfg.MaxNewOrdersPerDay == 0 && cfg.MaxPerPositionPct == nil && cfg.MaxPerMarketPct == nil && cfg.MinScoreByMarket == nil && cfg.MinEdgePctByMarket == nil && cfg.MinLiquidityUSD == nil && cfg.MaxSpreadPct == nil && cfg.Now == nil
}

func mergeMarketMap[T ~float64](provided, defaults map[domain.MarketType]T) map[domain.MarketType]T {
	out := make(map[domain.MarketType]T, len(defaults))
	for k, v := range defaults {
		out[k] = v
	}
	for k, v := range provided {
		out[k] = v
	}
	return out
}

func lookup(m map[domain.MarketType]float64, market domain.MarketType) float64 {
	if m == nil {
		return 0
	}
	if v, ok := m[market]; ok {
		return v
	}
	return 0
}

func isExpired(expiresAt, now time.Time) bool {
	if expiresAt.IsZero() {
		return false
	}
	return !now.Before(expiresAt)
}

func scoreOpportunity(opp domain.Opportunity, state PortfolioState, cfg AllocatorConfig, now time.Time) (float64, []string) {
	market := opp.MarketType
	edgeMin := lookup(cfg.MinEdgePctByMarket, market)
	liqMin := lookup(cfg.MinLiquidityUSD, market)
	spreadMax := lookup(cfg.MaxSpreadPct, market)
	marketCap := opp.MarketCapUSD
	marketExposure := lookupMarketExposure(state.MarketExposure, market)
	marketCapPct := lookup(cfg.MaxPerMarketPct, market)
	if marketCapPct < 0 {
		marketCapPct = 0
	}

	edgeScore := ratioScore(opp.EdgePct, edgeMin)
	confidenceScore := clamp01(opp.Confidence) * 100
	liquidityScore := 0.0
	if !math.IsInf(liqMin, 0) && liqMin > 0 {
		liquidityScore = ratioScore(opp.LiquidityUSD, liqMin)
	} else if opp.LiquidityUSD > 0 {
		liquidityScore = 100
	}
	spreadScore := 100.0
	if spreadMax > 0 {
		spreadScore = clamp01(1-(opp.SpreadPct/spreadMax)) * 100
	}

	diversificationScore := 100.0
	if isOpenTicker(state.OpenTickers, opp.Ticker) {
		diversificationScore -= 40
	}
	if state.Equity > 0 && marketCapPct > 0 {
		marketPressure := (marketExposure / state.Equity) / marketCapPct
		if marketPressure > 0.8 {
			diversificationScore -= 30 * clamp01((marketPressure-0.8)/0.2)
		}
	}

	freshnessScore := 100.0
	if !opp.CreatedAt.IsZero() && !opp.ExpiresAt.IsZero() && opp.ExpiresAt.After(opp.CreatedAt) && !now.Before(opp.CreatedAt) {
		ttl := opp.ExpiresAt.Sub(opp.CreatedAt)
		age := now.Sub(opp.CreatedAt)
		freshnessScore = clamp01(1-(float64(age)/float64(ttl))) * 100
	}

	score := edgeScore*0.35 + confidenceScore*0.20 + liquidityScore*0.15 + spreadScore*0.10 + diversificationScore*0.10 + freshnessScore*0.10

	penalties := 0.0
	reasons := make([]string, 0, 3)
	if isOpenTicker(state.OpenTickers, opp.Ticker) {
		penalties += 14
		reasons = append(reasons, reasonDuplicateTicker)
	}
	if marketCap > 0 {
		base := state.Equity * lookup(cfg.MaxPerPositionPct, market)
		if base <= 0 {
			base = opp.ProposedNotional
		}
		marketCapPressure := clamp01(base / marketCap)
		if marketCapPressure > 0.01 {
			penalties += 20 * clamp01(marketCapPressure/0.02)
			reasons = append(reasons, reasonNearMarketCap)
		}
	}
	if state.BuyingPower > 0 && state.Equity > 0 {
		reserve := state.Equity * cfg.CashReservePct
		if state.BuyingPower < reserve*1.25 {
			penalties += 8 * clamp01(1-(state.BuyingPower/reserve))
			reasons = append(reasons, reasonCashReservePressure)
		}
	}

	score = clampScore(score - penalties)
	return score, uniqueStrings(reasons)
}

func sizeOpportunity(opp domain.Opportunity, score float64, state PortfolioState, cfg AllocatorConfig) (float64, float64, string, []domain.AllocationRiskCap, []string) {
	market := opp.MarketType
	multiplier := scoreMultiplier(score)
	if multiplier <= 0 {
		return 0, 0, reasonBelowMinScore, nil, []string{reasonBelowMinScore}
	}
	perPosition := lookup(cfg.MaxPerPositionPct, market)
	base := state.Equity * perPosition * multiplier
	if opp.MarketType == domain.MarketTypeKalshi || opp.MarketType == domain.MarketTypePolymarket {
		base = math.Min(base, 25)
	}

	reasons := make([]string, 0, 4)

	remainingTarget := state.Equity*cfg.TargetGrossExposurePct - state.GrossExposure
	if remainingTarget <= 0 {
		return 0, 0, reasonTargetExposureExceeded, nil, []string{reasonTargetExposureExceeded}
	}
	remainingHard := state.Equity*cfg.HardGrossExposurePct - state.GrossExposure
	if remainingHard <= 0 {
		return 0, 0, reasonHardExposureExceeded, nil, []string{reasonHardExposureExceeded}
	}
	remainingMarket := state.Equity*lookup(cfg.MaxPerMarketPct, market) - lookupMarketExposure(state.MarketExposure, market)
	if remainingMarket <= 0 {
		return 0, 0, reasonMarketExposureExceeded, nil, []string{reasonMarketExposureExceeded}
	}
	remainingBuyingPower := state.BuyingPower - state.Equity*cfg.CashReservePct
	if remainingBuyingPower <= 0 {
		return 0, 0, reasonCashReserveInsufficient, nil, []string{reasonCashReserveInsufficient}
	}
	if opp.MarketType == domain.MarketTypeOptions {
		return sizeOptionsOpportunity(opp, base, remainingTarget, remainingHard, remainingMarket, remainingBuyingPower, state, cfg)
	}

	capValues := []struct {
		name  string
		value float64
	}{{"position_exposure", base}, {"target_exposure", remainingTarget}, {"hard_exposure", remainingHard}, {"market_exposure", remainingMarket}, {"buying_power", remainingBuyingPower}}
	if opp.MaxLossPct > 0 {
		capValues = append(capValues, struct {
			name  string
			value float64
		}{"position_risk", state.Equity * cfg.MaxPositionRiskPct * multiplier / opp.MaxLossPct})
	}
	if opp.ProposedNotional > 0 {
		capValues = append(capValues, struct {
			name  string
			value float64
		}{"opportunity_proposal", opp.ProposedNotional})
	}
	if opp.MarketCapUSD > 0 {
		capValues = append(capValues, struct {
			name  string
			value float64
		}{"market_liquidity", opp.MarketCapUSD * 0.02})
	}
	if opp.DeploymentBudgetUSD > 0 {
		capValues = append(capValues, struct {
			name  string
			value float64
		}{"deployment_budget", opp.DeploymentBudgetUSD})
	}
	final, binding := smallestPositiveCap(capValues)
	caps := make([]domain.AllocationRiskCap, 0, len(capValues))
	for sequence, capValue := range capValues {
		unit, quantityCap := opp.EntryPrice, 0.0
		if unit > 0 {
			quantityCap = math.Floor(capValue.value / unit)
		}
		caps = append(caps, domain.AllocationRiskCap{Sequence: sequence, Name: capValue.name, AvailableAmount: capValue.value, UnitAmount: unit, QuantityCap: quantityCap, Binding: capValue.name == binding})
	}
	if final <= 0 {
		return 0, 0, reasonSizingZero, caps, []string{reasonSizingZero}
	}
	quantity := 0.0
	if opp.EntryPrice > 0 {
		quantity = math.Floor(final / opp.EntryPrice)
		if quantity < 1 {
			return 0, 0, binding, caps, []string{reasonSizingZero, binding}
		}
		final = quantity * opp.EntryPrice
	}
	if final < base {
		reasons = append(reasons, reasonBudgetClamped)
	}
	return final, quantity, binding, caps, uniqueStrings(reasons)
}

func sizeOptionsOpportunity(opp domain.Opportunity, positionRisk, remainingTarget, remainingHard, remainingMarket, remainingBuyingPower float64, state PortfolioState, cfg AllocatorConfig) (float64, float64, string, []domain.AllocationRiskCap, []string) {
	type unitCap struct {
		name      string
		available float64
		unit      float64
		units     float64
	}
	caps := []unitCap{
		{"position_risk", positionRisk, opp.MaxLossPerUnit, math.Floor(positionRisk / opp.MaxLossPerUnit)},
		{"target_exposure", remainingTarget, opp.MaxLossPerUnit, math.Floor(remainingTarget / opp.MaxLossPerUnit)},
		{"hard_exposure", remainingHard, opp.MaxLossPerUnit, math.Floor(remainingHard / opp.MaxLossPerUnit)},
		{"market_exposure", remainingMarket, opp.MaxLossPerUnit, math.Floor(remainingMarket / opp.MaxLossPerUnit)},
		{"buying_power", math.Min(remainingBuyingPower, state.OptionsBuyingPower), opp.RequiredCapitalUnit, math.Floor(math.Min(remainingBuyingPower, state.OptionsBuyingPower) / opp.RequiredCapitalUnit)},
	}
	if opp.ProposedNotional > 0 {
		caps = append(caps, unitCap{"opportunity_proposal", opp.ProposedNotional, opp.RequiredCapitalUnit, math.Floor(opp.ProposedNotional / opp.RequiredCapitalUnit)})
	}
	if opp.DeploymentBudgetUSD > 0 {
		caps = append(caps, unitCap{"deployment_budget", opp.DeploymentBudgetUSD, opp.RequiredCapitalUnit, math.Floor(opp.DeploymentBudgetUSD / opp.RequiredCapitalUnit)})
	}
	if state.UnderlyingRisk != nil {
		remaining := state.Equity*cfg.MaxPositionRiskPct - state.UnderlyingRisk[strings.ToUpper(opp.Ticker)]
		caps = append(caps, unitCap{"same_underlying", remaining, opp.MaxLossPerUnit, math.Floor(remaining / opp.MaxLossPerUnit)})
	}
	greekCap := func(name string, current, perUnit, maximum float64) {
		if perUnit == 0 || maximum <= 0 {
			return
		}
		remaining := maximum - current
		if perUnit < 0 {
			remaining = maximum + current
		}
		caps = append(caps, unitCap{name, remaining, math.Abs(perUnit), math.Floor(remaining / math.Abs(perUnit))})
	}
	greekCap("delta", state.Delta, opp.Delta, cfg.MaxAbsoluteDelta)
	greekCap("gamma", state.Gamma, opp.Gamma, cfg.MaxAbsoluteGamma)
	greekCap("theta", state.Theta, opp.Theta, cfg.MaxAbsoluteTheta)
	greekCap("vega", state.Vega, opp.Vega, cfg.MaxAbsoluteVega)
	quantity, binding := math.Inf(1), ""
	for _, cap := range caps {
		if cap.units < quantity {
			quantity, binding = cap.units, cap.name
		}
	}
	recorded := make([]domain.AllocationRiskCap, 0, len(caps))
	for sequence, cap := range caps {
		recorded = append(recorded, domain.AllocationRiskCap{Sequence: sequence, Name: cap.name, AvailableAmount: cap.available, UnitAmount: cap.unit, QuantityCap: cap.units, Binding: cap.name == binding})
	}
	if math.IsInf(quantity, 1) || quantity < 1 {
		return 0, 0, binding, recorded, []string{reasonSizingZero, binding}
	}
	return quantity * opp.RequiredCapitalUnit, quantity, binding, recorded, nil
}

func smallestPositiveCap(values []struct {
	name  string
	value float64
},
) (float64, string) {
	best, binding := 0.0, ""
	for _, candidate := range values {
		if candidate.value <= 0 {
			continue
		}
		if best == 0 || candidate.value < best {
			best, binding = candidate.value, candidate.name
		}
	}
	return best, binding
}

func portfolioRiskRejectionReasons(opp domain.Opportunity, state PortfolioState, cfg AllocatorConfig, now time.Time) []string {
	reasons := make([]string, 0)
	if state.AccountBalanceFallback {
		reasons = append(reasons, reasonAccountBalanceFallback)
	}
	if state.CircuitBreakerOpen {
		reasons = append(reasons, reasonCircuitBreakerOpen)
	}
	if cfg.MaxDailyLossPct > 0 && state.DailyLossPct >= cfg.MaxDailyLossPct {
		reasons = append(reasons, reasonDailyLossLimit)
	}
	if cfg.MaxDrawdownPct > 0 && state.DrawdownPct >= cfg.MaxDrawdownPct {
		reasons = append(reasons, reasonDrawdownLimit)
	}
	if cfg.MaxOpenPositions > 0 && state.OpenPositionCount >= cfg.MaxOpenPositions {
		reasons = append(reasons, reasonOpenPositionLimit)
	}
	if opp.RiskPolicyVersion != "" && opp.RiskPolicyVersion != cfg.RiskPolicyVersion {
		reasons = append(reasons, reasonRiskPolicyMismatch)
	}
	if opp.MarketType == domain.MarketTypeStock || opp.MarketType == domain.MarketTypeCrypto {
		if opp.MaxLossPct <= 0 || opp.MaxLossPct > 1 {
			reasons = append(reasons, reasonUndefinedStockRisk)
		}
	}
	if opp.MarketType != domain.MarketTypeOptions {
		return reasons
	}
	if opp.RiskPolicyVersion != cfg.RiskPolicyVersion {
		reasons = append(reasons, reasonRiskPolicyMismatch)
	}
	if state.AccountSnapshotID == uuid.Nil {
		reasons = append(reasons, reasonMissingAccountSnapshot)
	}
	if state.BuyingPower <= 0 {
		reasons = append(reasons, reasonInsufficientBuyingPower)
	}
	if opp.MaxLossPerUnit <= 0 || opp.RequiredCapitalUnit <= 0 {
		reasons = append(reasons, reasonUndefinedOptionRisk)
	}
	if !validVertical(opp.OptionLegs) {
		reasons = append(reasons, reasonUnsupportedOptionPackage)
	}
	if opp.QuoteObservedAt == nil || now.Sub(opp.QuoteObservedAt.UTC()) > cfg.MaxQuoteAge || opp.QuoteObservedAt.After(now) {
		reasons = append(reasons, reasonStaleOptionQuote)
	}
	if math.Abs(state.Delta+opp.Delta) > cfg.MaxAbsoluteDelta || math.Abs(state.Gamma+opp.Gamma) > cfg.MaxAbsoluteGamma ||
		math.Abs(state.Theta+opp.Theta) > cfg.MaxAbsoluteTheta || math.Abs(state.Vega+opp.Vega) > cfg.MaxAbsoluteVega {
		reasons = append(reasons, reasonGreekLimit)
	}
	return reasons
}

func validVertical(legs []domain.OpportunityOptionLeg) bool {
	if len(legs) != 2 || legs[0].Sequence != 0 || legs[1].Sequence != 1 {
		return false
	}
	a, b := legs[0], legs[1]
	if a.ContractID == uuid.Nil || b.ContractID == uuid.Nil || a.ContractID == b.ContractID || a.OCCSymbol == "" || b.OCCSymbol == "" ||
		a.ContractPayloadID == uuid.Nil || b.ContractPayloadID == uuid.Nil || a.QuotePayloadID == uuid.Nil || b.QuotePayloadID == uuid.Nil ||
		a.SnapshotPayloadID == uuid.Nil || b.SnapshotPayloadID == uuid.Nil || !validEvidenceSHA(a.ContractSHA256) || !validEvidenceSHA(b.ContractSHA256) ||
		!validEvidenceSHA(a.QuoteSHA256) || !validEvidenceSHA(b.QuoteSHA256) || !validEvidenceSHA(a.SnapshotSHA256) || !validEvidenceSHA(b.SnapshotSHA256) ||
		a.Underlying == "" || a.Underlying != b.Underlying || !a.Expiry.Equal(b.Expiry) || a.OptionType != b.OptionType ||
		(a.OptionType != "call" && a.OptionType != "put") || a.Strike == b.Strike || a.Ratio != 1 || b.Ratio != 1 ||
		a.Multiplier != 100 || b.Multiplier != 100 || a.Side == b.Side {
		return false
	}
	longs, shorts := 0, 0
	for _, leg := range legs {
		if leg.PositionIntent == "buy_to_open" && leg.Side == domain.OrderSideBuy {
			longs++
		}
		if leg.PositionIntent == "sell_to_open" && leg.Side == domain.OrderSideSell {
			shorts++
		}
		if leg.Bid <= 0 || leg.Ask < leg.Bid {
			return false
		}
	}
	return longs == 1 && shorts == 1
}

func validEvidenceSHA(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func scoreMultiplier(score float64) float64 {
	switch {
	case score >= 85:
		return 1
	case score >= 75:
		return 0.75
	case score >= 65:
		return 0.5
	default:
		return 0
	}
}

func toAllocationDecision(item scoredOpportunity) domain.AllocationDecision {
	opportunityID := item.opp.ID
	strategyID := item.opp.StrategyID
	decision := domain.AllocationDecision{
		Mode:               domain.AllocationDecisionModeShadow,
		Action:             item.action,
		Score:              clampScore(item.score),
		NotionalUSD:        item.notional,
		Quantity:           0,
		RiskPolicyVersion:  item.opp.RiskPolicyVersion,
		ProposedQuantity:   proposedQuantity(item.opp),
		MaxLossPerUnit:     item.opp.MaxLossPerUnit,
		ReservedRiskUSD:    item.quantity * item.opp.MaxLossPerUnit,
		ReservedCapitalUSD: item.notional,
		ExposureBeforeUSD:  item.exposureBefore,
		ExposureAfterUSD:   item.exposureAfter,
		BindingConstraint:  item.binding,
		ExecutionRoute: func() string {
			if item.opp.MarketType == domain.MarketTypeOptions {
				return "alpaca_mleg"
			}
			return "stock_order_manager"
		}(),
		RiskCaps: append([]domain.AllocationRiskCap(nil), item.caps...),
		Reasons:  append([]string(nil), item.reasons...),
	}
	decision.Quantity = item.quantity
	if opportunityID != uuid.Nil {
		decision.OpportunityID = &opportunityID
	}
	if strategyID != uuid.Nil {
		decision.StrategyID = &strategyID
	}
	return decision
}

func proposedQuantity(opportunity domain.Opportunity) float64 {
	if opportunity.ProposedNotional <= 0 {
		return 0
	}
	unit := opportunity.EntryPrice
	if opportunity.MarketType == domain.MarketTypeOptions {
		unit = opportunity.RequiredCapitalUnit
	}
	if unit <= 0 {
		return 0
	}
	return math.Floor(opportunity.ProposedNotional / unit)
}

func clamp01(v float64) float64 {
	switch {
	case math.IsNaN(v):
		return 0
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

func clampScore(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func ratioScore(value, threshold float64) float64 {
	if threshold <= 0 {
		return 100
	}
	return clamp01(value/threshold) * 100
}

func lookupMarketExposure(m map[domain.MarketType]float64, market domain.MarketType) float64 {
	if m == nil {
		return 0
	}
	return m[market]
}

func isOpenTicker(open map[string]bool, ticker string) bool {
	if len(open) == 0 {
		return false
	}
	ticker = strings.TrimSpace(ticker)
	if ticker == "" {
		return false
	}
	if open[ticker] {
		return true
	}
	if open[strings.ToUpper(ticker)] {
		return true
	}
	if open[strings.ToLower(ticker)] {
		return true
	}
	return false
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func incrementMarket(m map[string]int, market domain.MarketType) {
	m[market.String()]++
}

func incrementStringReason(m map[string]int, reason string) {
	m[reason]++
}
