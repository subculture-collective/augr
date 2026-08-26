package backtest

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestMetricsJSONRoundTripEveryNumericField(t *testing.T) {
	t.Parallel()

	type metricField struct {
		name string
		set  func(*Metrics, float64)
		get  func(Metrics) float64
	}
	fields := []metricField{
		{"fill_rate", func(m *Metrics, v float64) { m.FillRate = v }, func(m Metrics) float64 { return m.FillRate }},
		{"total_return", func(m *Metrics, v float64) { m.TotalReturn = v }, func(m Metrics) float64 { return m.TotalReturn }},
		{"buy_and_hold_return", func(m *Metrics, v float64) { m.BuyAndHoldReturn = v }, func(m Metrics) float64 { return m.BuyAndHoldReturn }},
		{"max_drawdown", func(m *Metrics, v float64) { m.MaxDrawdown = v }, func(m Metrics) float64 { return m.MaxDrawdown }},
		{"calmar_ratio", func(m *Metrics, v float64) { m.CalmarRatio = v }, func(m Metrics) float64 { return m.CalmarRatio }},
		{"sharpe_ratio", func(m *Metrics, v float64) { m.SharpeRatio = v }, func(m Metrics) float64 { return m.SharpeRatio }},
		{"sortino_ratio", func(m *Metrics, v float64) { m.SortinoRatio = v }, func(m Metrics) float64 { return m.SortinoRatio }},
		{"alpha", func(m *Metrics, v float64) { m.Alpha = v }, func(m Metrics) float64 { return m.Alpha }},
		{"beta", func(m *Metrics, v float64) { m.Beta = v }, func(m Metrics) float64 { return m.Beta }},
		{"information_ratio", func(m *Metrics, v float64) { m.InformationRatio = v }, func(m Metrics) float64 { return m.InformationRatio }},
		{"win_rate", func(m *Metrics, v float64) { m.WinRate = v }, func(m Metrics) float64 { return m.WinRate }},
		{"profit_factor", func(m *Metrics, v float64) { m.ProfitFactor = v }, func(m Metrics) float64 { return m.ProfitFactor }},
		{"avg_win_loss_ratio", func(m *Metrics, v float64) { m.AvgWinLossRatio = v }, func(m Metrics) float64 { return m.AvgWinLossRatio }},
		{"volatility", func(m *Metrics, v float64) { m.Volatility = v }, func(m Metrics) float64 { return m.Volatility }},
		{"start_equity", func(m *Metrics, v float64) { m.StartEquity = v }, func(m Metrics) float64 { return m.StartEquity }},
		{"end_equity", func(m *Metrics, v float64) { m.EndEquity = v }, func(m Metrics) float64 { return m.EndEquity }},
		{"realized_pnl", func(m *Metrics, v float64) { m.RealizedPnL = v }, func(m Metrics) float64 { return m.RealizedPnL }},
		{"unrealized_pnl", func(m *Metrics, v float64) { m.UnrealizedPnL = v }, func(m Metrics) float64 { return m.UnrealizedPnL }},
	}
	values := []struct {
		name  string
		value float64
	}{
		{"finite", -123.456},
		{"NaN", math.NaN()},
		{"positive_infinity", math.Inf(1)},
		{"negative_infinity", math.Inf(-1)},
	}
	start := time.Date(2025, 2, 3, 4, 5, 6, 7, time.FixedZone("test", -5*60*60))
	end := start.Add(37*time.Hour + 11*time.Nanosecond)

	for _, field := range fields {
		field := field
		for _, testValue := range values {
			testValue := testValue
			t.Run(field.name+"/"+testValue.name, func(t *testing.T) {
				t.Parallel()
				input := Metrics{OrderAttempts: 17, OrderFills: 13, TotalBars: 991, StartTime: start, EndTime: end}
				for i, otherField := range fields {
					otherField.set(&input, float64(i)+0.125)
				}
				field.set(&input, testValue.value)

				encoded, err := json.Marshal(input)
				if err != nil {
					t.Fatal(err)
				}
				var output Metrics
				if err := json.Unmarshal(encoded, &output); err != nil {
					t.Fatalf("json.Unmarshal(%s): %v", encoded, err)
				}
				if output.OrderAttempts != input.OrderAttempts || output.OrderFills != input.OrderFills || output.TotalBars != input.TotalBars {
					t.Fatalf("integer fields changed: got %#v, want %#v", output, input)
				}
				if !output.StartTime.Equal(input.StartTime) || !output.EndTime.Equal(input.EndTime) {
					t.Fatalf("timestamps changed: got %v..%v, want %v..%v", output.StartTime, output.EndTime, input.StartTime, input.EndTime)
				}
				for _, otherField := range fields {
					assertSameFloat(t, otherField.name, otherField.get(output), otherField.get(input))
				}
			})
		}
	}
}

func TestMetricsUnmarshalJSONRejectsMalformedValues(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		`{"sharpe_ratio":"nan"}`,
		`{"sharpe_ratio":"+Infinity"}`,
		`{"sharpe_ratio":"infinity"}`,
		`{"sharpe_ratio":null}`,
		`{"sharpe_ratio":true}`,
		`{"sharpe_ratio":[]}`,
		`{"sharpe_ratio":1e400}`,
		`{"total_bars":92233720368547758070}`,
		`{"sharpe_ratio":1} trailing`,
		`{"sharpe_ratio":1,"sharpe_ratio":"NaN"}`,
		`{"sharpe_ratio":01}`,
		`[1]`,
	} {
		input := input
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			var metrics Metrics
			if err := json.Unmarshal([]byte(input), &metrics); err == nil {
				t.Fatalf("json.Unmarshal(%s) error = nil", input)
			}
		})
	}
}

func TestMetricsUnmarshalJSONAcceptsBackwardFiniteJSON(t *testing.T) {
	t.Parallel()

	var metrics Metrics
	if err := json.Unmarshal([]byte(`{"order_attempts":9,"fill_rate":0.75,"sharpe_ratio":1.5,"start_equity":1000,"total_bars":42}`), &metrics); err != nil {
		t.Fatal(err)
	}
	if metrics.OrderAttempts != 9 || metrics.FillRate != 0.75 || metrics.SharpeRatio != 1.5 || metrics.StartEquity != 1000 || metrics.TotalBars != 42 {
		t.Fatalf("decoded metrics = %#v", metrics)
	}
}

func assertSameFloat(t *testing.T, field string, got, want float64) {
	t.Helper()
	switch {
	case math.IsNaN(want):
		if !math.IsNaN(got) {
			t.Errorf("%s = %v, want NaN", field, got)
		}
	case math.IsInf(want, 1):
		if !math.IsInf(got, 1) {
			t.Errorf("%s = %v, want +Inf", field, got)
		}
	case math.IsInf(want, -1):
		if !math.IsInf(got, -1) {
			t.Errorf("%s = %v, want -Inf", field, got)
		}
	case got != want:
		t.Errorf("%s = %v, want %v", field, got, want)
	}
}

func TestComputeMetricsEmpty(t *testing.T) {
	t.Parallel()

	m := ComputeMetrics(nil, nil)
	if m.TotalBars != 0 {
		t.Errorf("TotalBars = %d, want 0", m.TotalBars)
	}
}

func TestComputeMetricsSinglePoint(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	curve := []EquityPoint{
		{Timestamp: ts, Equity: 100_000, Cash: 100_000},
	}
	m := ComputeMetrics(curve, nil)

	if m.TotalBars != 1 {
		t.Errorf("TotalBars = %d, want 1", m.TotalBars)
	}
	if m.StartEquity != 100_000 {
		t.Errorf("StartEquity = %f, want 100000", m.StartEquity)
	}
	if m.EndEquity != 100_000 {
		t.Errorf("EndEquity = %f, want 100000", m.EndEquity)
	}
	// With a single point, return-based metrics should be zero.
	if m.TotalReturn != 0 {
		t.Errorf("TotalReturn = %f, want 0", m.TotalReturn)
	}
}

func TestComputeMetricsTotalReturn(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	curve := []EquityPoint{
		{Timestamp: base, Equity: 100_000},
		{Timestamp: base.Add(24 * time.Hour), Equity: 110_000},
	}
	m := ComputeMetrics(curve, nil)

	wantReturn := 0.1
	if math.Abs(m.TotalReturn-wantReturn) > 1e-9 {
		t.Errorf("TotalReturn = %f, want %f", m.TotalReturn, wantReturn)
	}
}

func TestComputeMetricsMaxDrawdown(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Equity rises to 110, drops to 88 (20% drawdown from peak), recovers.
	curve := []EquityPoint{
		{Timestamp: base, Equity: 100},
		{Timestamp: base.Add(1 * 24 * time.Hour), Equity: 110},
		{Timestamp: base.Add(2 * 24 * time.Hour), Equity: 88},
		{Timestamp: base.Add(3 * 24 * time.Hour), Equity: 105},
	}
	m := ComputeMetrics(curve, nil)

	wantDD := (110.0 - 88.0) / 110.0 // 0.2
	if math.Abs(m.MaxDrawdown-wantDD) > 1e-9 {
		t.Errorf("MaxDrawdown = %f, want %f", m.MaxDrawdown, wantDD)
	}
}

func TestComputeMetricsWinRateAndProfitFactor(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// 3 returns: +10%, -5%, +3% → 2 wins, 1 loss
	curve := []EquityPoint{
		{Timestamp: base, Equity: 100},
		{Timestamp: base.Add(1 * 24 * time.Hour), Equity: 110},
		{Timestamp: base.Add(2 * 24 * time.Hour), Equity: 104.5},
		{Timestamp: base.Add(3 * 24 * time.Hour), Equity: 107.635},
	}
	m := ComputeMetrics(curve, nil)

	// Win rate: 2 wins / 3 total (flat excluded) → 0.6667
	wantWR := 2.0 / 3.0
	if math.Abs(m.WinRate-wantWR) > 1e-4 {
		t.Errorf("WinRate = %f, want %f", m.WinRate, wantWR)
	}

	if m.ProfitFactor <= 0 {
		t.Errorf("ProfitFactor = %f, want > 0", m.ProfitFactor)
	}
}

func TestComputeMetricsSharpeAndSortino(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Mixed returns with an overall upward trend and some down bars so
	// both Sharpe and Sortino can be meaningfully computed.
	curve := []EquityPoint{
		{Timestamp: base, Equity: 100_000},
		{Timestamp: base.Add(1 * 24 * time.Hour), Equity: 101_000},
		{Timestamp: base.Add(2 * 24 * time.Hour), Equity: 100_500},
		{Timestamp: base.Add(3 * 24 * time.Hour), Equity: 102_000},
		{Timestamp: base.Add(4 * 24 * time.Hour), Equity: 101_500},
		{Timestamp: base.Add(5 * 24 * time.Hour), Equity: 103_000},
	}
	m := ComputeMetrics(curve, nil)

	if m.SharpeRatio <= 0 {
		t.Errorf("SharpeRatio = %f, want > 0", m.SharpeRatio)
	}
	if m.SortinoRatio <= 0 {
		t.Errorf("SortinoRatio = %f, want > 0", m.SortinoRatio)
	}
	if m.Volatility <= 0 {
		t.Errorf("Volatility = %f, want > 0", m.Volatility)
	}
}

func TestComputeMetricsNoLosses(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	curve := []EquityPoint{
		{Timestamp: base, Equity: 100},
		{Timestamp: base.Add(24 * time.Hour), Equity: 110},
		{Timestamp: base.Add(48 * time.Hour), Equity: 120},
	}
	m := ComputeMetrics(curve, nil)

	if m.WinRate != 1.0 {
		t.Errorf("WinRate = %f, want 1.0", m.WinRate)
	}
	if !math.IsInf(m.ProfitFactor, 1) {
		t.Errorf("ProfitFactor = %f, want +Inf", m.ProfitFactor)
	}
	if !math.IsInf(m.AvgWinLossRatio, 1) {
		t.Errorf("AvgWinLossRatio = %f, want +Inf", m.AvgWinLossRatio)
	}
}

func TestComputeMetricsCalmarAndAvgWinLossRatio(t *testing.T) {
	t.Parallel()

	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	// Returns: +25%, -20%, +10%, -5% over >1 year.
	curve := []EquityPoint{
		{Timestamp: start, Equity: 100},
		{Timestamp: start.Add(24 * time.Hour), Equity: 125},
		{Timestamp: start.Add(48 * time.Hour), Equity: 100},
		{Timestamp: start.Add(72 * time.Hour), Equity: 110},
		{Timestamp: start.Add(96 * time.Hour), Equity: 104.5},
		{Timestamp: start.Add(366 * 24 * time.Hour), Equity: 104.5},
	}

	m := ComputeMetrics(curve, nil)

	wantAvgWinLoss := ((0.25 + 0.10) / 2.0) / ((0.20 + 0.05) / 2.0) // 1.4
	if math.Abs(m.AvgWinLossRatio-wantAvgWinLoss) > 1e-9 {
		t.Errorf("AvgWinLossRatio = %f, want %f", m.AvgWinLossRatio, wantAvgWinLoss)
	}
	if m.CalmarRatio <= 0 {
		t.Errorf("CalmarRatio = %f, want > 0", m.CalmarRatio)
	}
}

func TestComputeMetricsCalmarZeroWhenNoDrawdown(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	curve := []EquityPoint{
		{Timestamp: base, Equity: 100},
		{Timestamp: base.Add(24 * time.Hour), Equity: 110},
		{Timestamp: base.Add(48 * time.Hour), Equity: 120},
	}
	m := ComputeMetrics(curve, nil)

	if m.MaxDrawdown != 0 {
		t.Errorf("MaxDrawdown = %f, want 0", m.MaxDrawdown)
	}
	if m.CalmarRatio != 0 {
		t.Errorf("CalmarRatio = %f, want 0", m.CalmarRatio)
	}
}

func TestComputeMetricsCalmarZeroWhenEndingEquityNonPositive(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	curve := []EquityPoint{
		{Timestamp: base, Equity: 100},
		{Timestamp: base.Add(24 * time.Hour), Equity: 120},
		{Timestamp: base.Add(48 * time.Hour), Equity: -10},
		{Timestamp: base.Add(366 * 24 * time.Hour), Equity: -10},
	}
	m := ComputeMetrics(curve, nil)

	if m.MaxDrawdown <= 0 {
		t.Errorf("MaxDrawdown = %f, want > 0", m.MaxDrawdown)
	}
	if m.CalmarRatio != 0 {
		t.Errorf("CalmarRatio = %f, want 0 for non-positive equity ratio", m.CalmarRatio)
	}
}

func TestComputeMetricsTimestamps(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	curve := []EquityPoint{
		{Timestamp: start, Equity: 100},
		{Timestamp: end, Equity: 105},
	}
	m := ComputeMetrics(curve, nil)

	if !m.StartTime.Equal(start) {
		t.Errorf("StartTime = %v, want %v", m.StartTime, start)
	}
	if !m.EndTime.Equal(end) {
		t.Errorf("EndTime = %v, want %v", m.EndTime, end)
	}
}

func TestComputeMetricsBenchmarkComparison(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	curve := []EquityPoint{
		{Timestamp: base, Equity: 100},
		{Timestamp: base.Add(24 * time.Hour), Equity: 110},
		{Timestamp: base.Add(48 * time.Hour), Equity: 116.6},
	}
	benchmark := []domain.OHLCV{
		makeBar(base, 100),
		makeBar(base.Add(24*time.Hour), 105),
		makeBar(base.Add(48*time.Hour), 108.15),
	}

	m := ComputeMetrics(curve, benchmark)

	if math.Abs(m.BuyAndHoldReturn-0.0815) > 1e-9 {
		t.Errorf("BuyAndHoldReturn = %f, want %f", m.BuyAndHoldReturn, 0.0815)
	}
	if math.Abs(m.Beta-2.0) > 1e-9 {
		t.Errorf("Beta = %f, want %f", m.Beta, 2.0)
	}
	if math.Abs(m.Alpha) > 1e-9 {
		t.Errorf("Alpha = %f, want 0", m.Alpha)
	}
}

func TestComputeMetricsInformationRatio(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	curve := []EquityPoint{
		{Timestamp: base, Equity: 100},
		{Timestamp: base.Add(24 * time.Hour), Equity: 109},
		{Timestamp: base.Add(48 * time.Hour), Equity: 114.45},
	}
	benchmark := []domain.OHLCV{
		makeBar(base, 100),
		makeBar(base.Add(24*time.Hour), 105),
		makeBar(base.Add(48*time.Hour), 109.2),
	}

	m := ComputeMetrics(curve, benchmark)
	if m.InformationRatio <= 0 {
		t.Errorf("InformationRatio = %f, want > 0", m.InformationRatio)
	}
}

func TestComputeMetricsBenchmarkZeroTrackingError(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	curve := []EquityPoint{
		{Timestamp: base, Equity: 100},
		{Timestamp: base.Add(24 * time.Hour), Equity: 110},
		{Timestamp: base.Add(48 * time.Hour), Equity: 121},
	}
	benchmark := []domain.OHLCV{
		makeBar(base, 100),
		makeBar(base.Add(24*time.Hour), 110),
		makeBar(base.Add(48*time.Hour), 121),
	}

	m := ComputeMetrics(curve, benchmark)

	if m.InformationRatio != 0 {
		t.Errorf("InformationRatio = %f, want 0 when tracking error is zero", m.InformationRatio)
	}
	if m.Beta != 0 {
		t.Errorf("Beta = %f, want 0 when benchmark variance is zero", m.Beta)
	}
	if m.Alpha != 0 {
		t.Errorf("Alpha = %f, want 0 when strategy returns match zero-variance benchmark", m.Alpha)
	}
}

func TestComputeMetricsBenchmarkOrderIndependence(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	curve := []EquityPoint{
		{Timestamp: base, Equity: 100},
		{Timestamp: base.Add(24 * time.Hour), Equity: 110},
		{Timestamp: base.Add(48 * time.Hour), Equity: 116.6},
	}
	sortedBenchmark := []domain.OHLCV{
		makeBar(base, 100),
		makeBar(base.Add(24*time.Hour), 105),
		makeBar(base.Add(48*time.Hour), 108.15),
	}
	unsortedBenchmark := []domain.OHLCV{
		makeBar(base.Add(48*time.Hour), 108.15),
		makeBar(base, 100),
		makeBar(base.Add(24*time.Hour), 105),
	}

	sortedMetrics := ComputeMetrics(curve, sortedBenchmark)
	unsortedMetrics := ComputeMetrics(curve, unsortedBenchmark)

	if math.Abs(sortedMetrics.BuyAndHoldReturn-unsortedMetrics.BuyAndHoldReturn) > 1e-9 {
		t.Errorf("BuyAndHoldReturn mismatch for unsorted bars: sorted=%f unsorted=%f", sortedMetrics.BuyAndHoldReturn, unsortedMetrics.BuyAndHoldReturn)
	}
	if math.Abs(sortedMetrics.Beta-unsortedMetrics.Beta) > 1e-9 {
		t.Errorf("Beta mismatch for unsorted bars: sorted=%f unsorted=%f", sortedMetrics.Beta, unsortedMetrics.Beta)
	}
	if math.Abs(sortedMetrics.Alpha-unsortedMetrics.Alpha) > 1e-9 {
		t.Errorf("Alpha mismatch for unsorted bars: sorted=%f unsorted=%f", sortedMetrics.Alpha, unsortedMetrics.Alpha)
	}
	if math.Abs(sortedMetrics.InformationRatio-unsortedMetrics.InformationRatio) > 1e-9 {
		t.Errorf("InformationRatio mismatch for unsorted bars: sorted=%f unsorted=%f", sortedMetrics.InformationRatio, unsortedMetrics.InformationRatio)
	}
}

func TestComputeMetricsBenchmarkSampleTooShort(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	curve := []EquityPoint{
		{Timestamp: base, Equity: 100},
		{Timestamp: base.Add(24 * time.Hour), Equity: 110},
	}
	benchmark := []domain.OHLCV{
		makeBar(base, 100),
		makeBar(base.Add(24*time.Hour), 105),
	}

	m := ComputeMetrics(curve, benchmark)
	if m.Beta != 0 {
		t.Errorf("Beta = %f, want 0 for single aligned return observation", m.Beta)
	}
	if m.Alpha != 0 {
		t.Errorf("Alpha = %f, want 0 for single aligned return observation", m.Alpha)
	}
	if m.InformationRatio != 0 {
		t.Errorf("InformationRatio = %f, want 0 for single aligned return observation", m.InformationRatio)
	}
}
