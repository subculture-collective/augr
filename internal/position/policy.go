package position

import (
	"math"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
)

// HistoryStats captures the closed-trade statistics required for Kelly sizing.
type HistoryStats struct {
	ClosedTrades int
	WinRate      float64
	WinLossRatio float64
}

const (
	// DefaultPolymarketFractionPct is the ADR-005 fixed-fractional default.
	DefaultPolymarketFractionPct = 0.02

	// KellyHistoryThreshold is the minimum closed-trade history required before
	// Kelly sizing is allowed.
	KellyHistoryThreshold = 100

	// HalfKellyMultiplier is the conservative Kelly default once eligible.
	HalfKellyMultiplier = 0.5
)

// SizingPolicy describes the default sizing choice for a market.
type SizingPolicy struct {
	Method        execution.PositionSizingMethod
	RiskPct       float64
	ATRMultiplier float64
	WinRate       float64
	WinLossRatio  float64
	FractionPct   float64
	HalfKelly     bool
}

// DefaultForMarket returns the ADR-005 default sizing policy for a market.
func DefaultForMarket(market domain.MarketType, positionSizePct, stopLossMultiplier float64) SizingPolicy {
	switch market.Normalize() {
	case domain.MarketTypeStock, domain.MarketTypeCrypto:
		return SizingPolicy{Method: execution.PositionSizingMethodATR, RiskPct: positionSizePct / 100.0, ATRMultiplier: stopLossMultiplier}
	case domain.MarketTypePolymarket:
		return SizingPolicy{Method: execution.PositionSizingMethodFixedFractional, FractionPct: DefaultPolymarketFractionPct}
	default:
		return SizingPolicy{}
	}
}

// ResolveForMarket returns the market default unless Kelly is explicitly opted in
// and the history stats satisfy ADR-005 eligibility requirements.
func ResolveForMarket(market domain.MarketType, positionSizePct, stopLossMultiplier float64, useKelly bool, stats HistoryStats) SizingPolicy {
	defaultPolicy := DefaultForMarket(market, positionSizePct, stopLossMultiplier)
	if !useKelly {
		return defaultPolicy
	}

	if kelly := HalfKellyForHistory(stats.ClosedTrades, stats.WinRate, stats.WinLossRatio); kelly.Method != "" {
		return kelly
	}

	return defaultPolicy
}

// HalfKellyForHistory returns the conservative Kelly sizing policy once the
// strategy has enough closed trades and the edge inputs are usable.
func HalfKellyForHistory(closedTrades int, winRate, winLossRatio float64) SizingPolicy {
	if closedTrades < KellyHistoryThreshold || !isValidKellyInput(winRate, winLossRatio) {
		return SizingPolicy{}
	}

	return SizingPolicy{Method: execution.PositionSizingMethodKelly, WinRate: winRate, WinLossRatio: winLossRatio, HalfKelly: true}
}

// ExecutionSizingConfig converts the policy into the order-manager sizing config.
func (p SizingPolicy) ExecutionSizingConfig() execution.SizingConfig {
	return execution.SizingConfig{
		Method:        p.Method,
		RiskPct:       p.RiskPct,
		ATRMultiplier: p.ATRMultiplier,
		WinRate:       p.WinRate,
		WinLossRatio:  p.WinLossRatio,
		FractionPct:   p.FractionPct,
		HalfKelly:     p.HalfKelly,
	}
}

func isValidKellyInput(winRate, winLossRatio float64) bool {
	return finiteWinRate(winRate) && finiteWinLossRatio(winLossRatio)
}

func finiteWinRate(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 && v < 1
}

func finiteWinLossRatio(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0
}
