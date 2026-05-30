package polymarketdiscovery

import (
	"sort"
	"time"
)

// ScreenerConfig controls which Gamma markets become discovery candidates.
type ScreenerConfig struct {
	// FetchLimit caps the number of markets pulled from Gamma per run.
	FetchLimit int
	// MinVolume24h is the minimum 24-hour volume in USDC.
	MinVolume24h float64
	// MinLiquidity is the minimum orderbook liquidity in USDC.
	MinLiquidity float64
	// MaxSpread is the maximum allowed YES bid/ask spread (price units).
	MaxSpread float64
	// MinDaysToResolution requires the market to resolve at least this far out.
	MinDaysToResolution int
	// MaxDaysToResolution requires the market to resolve within this window.
	MaxDaysToResolution int
	// BinaryOnly restricts to Yes/No markets only.
	BinaryOnly bool
	// MaxCandidates caps the number of screened markets returned.
	MaxCandidates int
}

// DefaultScreenerConfig returns sensible defaults for discovery screening.
func DefaultScreenerConfig() ScreenerConfig {
	return ScreenerConfig{
		FetchLimit:          200,
		MinVolume24h:        5_000,
		MinLiquidity:        2_000,
		MaxSpread:           0.05,
		MinDaysToResolution: 1,
		MaxDaysToResolution: 120,
		BinaryOnly:          true,
		MaxCandidates:       15,
	}
}

// ScreenMarkets applies the configured filters and returns candidates ranked
// by 24-hour volume desc.
func ScreenMarkets(markets []GammaMarket, cfg ScreenerConfig) []GammaMarket {
	now := time.Now()
	var passed []GammaMarket
	for _, m := range markets {
		if m.Closed || m.Archived || !m.AcceptingOrders {
			continue
		}
		if cfg.BinaryOnly && !m.IsBinaryYesNo() {
			continue
		}
		v24 := m.Volume24HrFloat()
		if v24 < cfg.MinVolume24h {
			continue
		}
		liq := m.LiquidityFloat()
		if liq < cfg.MinLiquidity {
			continue
		}
		if spread, ok := m.SpreadFloat(); ok && cfg.MaxSpread > 0 && spread > cfg.MaxSpread {
			continue
		}
		end := m.EndTime()
		if end.IsZero() {
			continue
		}
		days := int(end.Sub(now).Hours() / 24)
		if days < cfg.MinDaysToResolution || days > cfg.MaxDaysToResolution {
			continue
		}
		passed = append(passed, m)
	}
	sort.SliceStable(passed, func(i, j int) bool {
		return passed[i].Volume24HrFloat() > passed[j].Volume24HrFloat()
	})
	if cfg.MaxCandidates > 0 && len(passed) > cfg.MaxCandidates {
		passed = passed[:cfg.MaxCandidates]
	}
	return passed
}
