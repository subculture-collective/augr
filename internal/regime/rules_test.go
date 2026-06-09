package regime

import (
	"math"
	"testing"
)

func TestEvaluate_EachReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  RuleConfig
		snap Snapshot
		want string
	}{
		{
			name: "loss cluster pause",
			cfg:  RuleConfig{MaxConsecutiveLosses: 3},
			snap: Snapshot{ConsecutiveLosses: 3},
			want: ReasonLossClusterPause,
		},
		{
			name: "win rate below floor",
			cfg:  RuleConfig{MinRollingWinRate: 0.55},
			snap: Snapshot{RollingWinRate: 0.54},
			want: ReasonWinRateBelowFloor,
		},
		{
			name: "fill rate collapse",
			cfg:  RuleConfig{MinFillRate: 0.8},
			snap: Snapshot{FillRate: 0.79},
			want: ReasonFillRateCollapse,
		},
		{
			name: "slippage above baseline",
			cfg:  RuleConfig{MaxSlippagePct: 0.02},
			snap: Snapshot{SlippagePct: 0.021},
			want: ReasonSlippageAboveBaseline,
		},
		{
			name: "volatility out of range",
			cfg:  RuleConfig{MaxVolatilityPct: 0.25},
			snap: Snapshot{VolatilityPct: 0.3},
			want: ReasonVolatilityOutOfRange,
		},
		{
			name: "liquidity disappeared",
			cfg:  RuleConfig{MinLiquidityUSD: 100_000},
			snap: Snapshot{LiquidityUSD: 99_999},
			want: ReasonLiquidityDisappeared,
		},
		{
			name: "api latency spike",
			cfg:  RuleConfig{MaxAPILatencyMs: 250},
			snap: Snapshot{APILatencyMs: 250},
			want: ReasonAPILatencySpike,
		},
		{
			name: "data source conflict",
			cfg:  RuleConfig{},
			snap: Snapshot{DataSourceConflict: true},
			want: ReasonDataSourceConflict,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Evaluate(tt.snap, tt.cfg)
			if !got.Paused {
				t.Fatal("Paused = false, want true")
			}
			if len(got.Reasons) != 1 || got.Reasons[0] != tt.want {
				t.Fatalf("Reasons = %#v, want [%q]", got.Reasons, tt.want)
			}
		})
	}
}

func TestEvaluate_ReasonOrdering(t *testing.T) {
	t.Parallel()

	got := Evaluate(Snapshot{
		ConsecutiveLosses:  5,
		RollingWinRate:     0.1,
		FillRate:           0.1,
		SlippagePct:        0.5,
		VolatilityPct:      1.0,
		LiquidityUSD:       1,
		APILatencyMs:       999,
		DataSourceConflict: true,
	}, RuleConfig{
		MaxConsecutiveLosses: 3,
		MinRollingWinRate:    0.5,
		MinFillRate:          0.8,
		MaxSlippagePct:       0.05,
		MaxVolatilityPct:     0.3,
		MinLiquidityUSD:      100,
		MaxAPILatencyMs:      250,
	})

	want := []string{
		ReasonLossClusterPause,
		ReasonWinRateBelowFloor,
		ReasonFillRateCollapse,
		ReasonSlippageAboveBaseline,
		ReasonVolatilityOutOfRange,
		ReasonLiquidityDisappeared,
		ReasonAPILatencySpike,
		ReasonDataSourceConflict,
	}
	if len(got.Reasons) != len(want) {
		t.Fatalf("len(Reasons) = %d, want %d", len(got.Reasons), len(want))
	}
	for i := range want {
		if got.Reasons[i] != want[i] {
			t.Fatalf("reason[%d] = %q, want %q", i, got.Reasons[i], want[i])
		}
	}
	if !got.Paused {
		t.Fatal("Paused = false, want true")
	}
}

func TestEvaluate_ZeroValueConfigIsSafe(t *testing.T) {
	t.Parallel()

	got := Evaluate(Snapshot{
		ConsecutiveLosses:  99,
		RollingWinRate:     0.0,
		FillRate:           0.0,
		SlippagePct:        1.0,
		VolatilityPct:      1.0,
		LiquidityUSD:       0,
		APILatencyMs:       9999,
		DataSourceConflict: false,
	}, RuleConfig{})

	if got.Paused {
		t.Fatalf("Paused = true, want false")
	}
	if len(got.Reasons) != 0 {
		t.Fatalf("Reasons = %#v, want empty", got.Reasons)
	}

	conflict := Evaluate(Snapshot{DataSourceConflict: true}, RuleConfig{})
	if !conflict.Paused || len(conflict.Reasons) != 1 || conflict.Reasons[0] != ReasonDataSourceConflict {
		t.Fatalf("conflict decision = %#v, want data_source_conflict only", conflict)
	}
}

func TestEvaluate_HandlesNaNAndInfInputs(t *testing.T) {
	t.Parallel()

	got := Evaluate(Snapshot{
		ConsecutiveLosses: 4,
		RollingWinRate:    math.NaN(),
		FillRate:          math.Inf(1),
		SlippagePct:       math.Inf(-1),
		VolatilityPct:     math.NaN(),
		LiquidityUSD:      math.Inf(1),
		APILatencyMs:      300,
	}, RuleConfig{
		MaxConsecutiveLosses: 3,
		MinRollingWinRate:    0.5,
		MinFillRate:          0.8,
		MaxSlippagePct:       0.05,
		MaxVolatilityPct:     0.3,
		MinLiquidityUSD:      100,
		MaxAPILatencyMs:      250,
	})

	want := []string{ReasonLossClusterPause, ReasonAPILatencySpike}
	if len(got.Reasons) != len(want) {
		t.Fatalf("Reasons = %#v, want %v", got.Reasons, want)
	}
	for i := range want {
		if got.Reasons[i] != want[i] {
			t.Fatalf("reason[%d] = %q, want %q", i, got.Reasons[i], want[i])
		}
	}
	if !got.Paused {
		t.Fatal("Paused = false, want true")
	}
}
