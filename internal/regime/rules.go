package regime

import "math"

const (
	ReasonLossClusterPause      = "loss_cluster_pause"
	ReasonWinRateBelowFloor     = "win_rate_below_floor"
	ReasonFillRateCollapse      = "fill_rate_collapse"
	ReasonSlippageAboveBaseline = "slippage_above_baseline"
	ReasonVolatilityOutOfRange  = "volatility_out_of_range"
	ReasonLiquidityDisappeared  = "liquidity_disappeared"
	ReasonAPILatencySpike       = "api_latency_spike"
	ReasonDataSourceConflict    = "data_source_conflict"
)

// Snapshot captures the latest regime inputs.
type Snapshot struct {
	ConsecutiveLosses  int
	RollingWinRate     float64
	FillRate           float64
	SlippagePct        float64
	VolatilityPct      float64
	LiquidityUSD       float64
	APILatencyMs       int
	DataSourceConflict bool
}

// RuleConfig controls pause/reject thresholds.
// Zero-value config is safe: thresholds disabled at 0 or below, leaving only
// the explicit DataSourceConflict boolean as a hard pause condition.
type RuleConfig struct {
	MaxConsecutiveLosses int
	MinRollingWinRate    float64
	MinFillRate          float64
	MaxSlippagePct       float64
	MaxVolatilityPct     float64
	MinLiquidityUSD      float64
	MaxAPILatencyMs      int
}

// Decision reports whether the strategy should pause and why.
type Decision struct {
	Paused  bool
	Reasons []string
}

// Evaluate applies the regime rules deterministically.
func Evaluate(snapshot Snapshot, cfg RuleConfig) Decision {
	reasons := make([]string, 0, 8)

	if cfg.MaxConsecutiveLosses > 0 && snapshot.ConsecutiveLosses >= cfg.MaxConsecutiveLosses {
		reasons = append(reasons, ReasonLossClusterPause)
	}
	if cfg.MinRollingWinRate > 0 && isFinite(snapshot.RollingWinRate) && snapshot.RollingWinRate < cfg.MinRollingWinRate {
		reasons = append(reasons, ReasonWinRateBelowFloor)
	}
	if cfg.MinFillRate > 0 && isFinite(snapshot.FillRate) && snapshot.FillRate < cfg.MinFillRate {
		reasons = append(reasons, ReasonFillRateCollapse)
	}
	if cfg.MaxSlippagePct > 0 && isFinite(snapshot.SlippagePct) && snapshot.SlippagePct > cfg.MaxSlippagePct {
		reasons = append(reasons, ReasonSlippageAboveBaseline)
	}
	if cfg.MaxVolatilityPct > 0 && isFinite(snapshot.VolatilityPct) && snapshot.VolatilityPct > cfg.MaxVolatilityPct {
		reasons = append(reasons, ReasonVolatilityOutOfRange)
	}
	if cfg.MinLiquidityUSD > 0 && isFinite(snapshot.LiquidityUSD) && snapshot.LiquidityUSD < cfg.MinLiquidityUSD {
		reasons = append(reasons, ReasonLiquidityDisappeared)
	}
	if cfg.MaxAPILatencyMs > 0 && snapshot.APILatencyMs >= cfg.MaxAPILatencyMs {
		reasons = append(reasons, ReasonAPILatencySpike)
	}
	if snapshot.DataSourceConflict {
		reasons = append(reasons, ReasonDataSourceConflict)
	}

	return Decision{Paused: len(reasons) > 0, Reasons: reasons}
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
