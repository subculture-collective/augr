package backtest

import (
	"math"
	"sort"
	"testing"
	"time"
)

func TestLatencyModel_DeterministicWithSeed(t *testing.T) {
	t.Parallel()
	m1 := DefaultLatencyModel(42)
	m2 := DefaultLatencyModel(42)
	for i := 0; i < 1000; i++ {
		if got1, got2 := m1.Sample(), m2.Sample(); got1 != got2 {
			t.Fatalf("sample %d mismatch: %v != %v", i, got1, got2)
		}
	}
}

func TestLatencyModel_DistributionShape(t *testing.T) {
	t.Parallel()
	m := DefaultLatencyModel(7)
	samples := make([]float64, 10_000)
	for i := range samples {
		samples[i] = float64(m.Sample()) / float64(time.Second)
	}
	sort.Float64s(samples)
	median := samples[len(samples)/2]
	p95 := samples[int(math.Ceil(0.95*float64(len(samples))))-1]
	if median < 0.032 || median > 0.048 {
		t.Fatalf("median=%v, want within ±20%% of 0.040", median)
	}
	if p95 <= median {
		t.Fatalf("p95=%v, want > median=%v", p95, median)
	}
}

func TestLatencyModel_AlwaysPositive(t *testing.T) {
	t.Parallel()
	m := DefaultLatencyModel(99)
	for i := 0; i < 10_000; i++ {
		if got := m.Sample(); got <= 0 {
			t.Fatalf("sample %d = %v, want > 0", i, got)
		}
	}
}
