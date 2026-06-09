package edge

import (
	"math"
	"testing"
)

func TestBrierScore(t *testing.T) {
	got := BrierScore([]ProbabilityOutcome{{P: 0.8, Outcome: true}, {P: 0.3, Outcome: false}})
	if !almostEqual(got, 0.065, 1e-9) {
		t.Fatalf("BrierScore = %v, want %v", got, 0.065)
	}
}

func TestLogLoss(t *testing.T) {
	got := LogLoss([]ProbabilityOutcome{{P: 0.8, Outcome: true}, {P: 0.3, Outcome: false}})
	want := (-math.Log(0.8) - math.Log(0.7)) / 2
	if !almostEqual(got, want, 1e-12) {
		t.Fatalf("LogLoss = %v, want %v", got, want)
	}
}

func TestLogLossClampsExtremeProbabilities(t *testing.T) {
	got := LogLoss([]ProbabilityOutcome{{P: 0, Outcome: true}, {P: 1, Outcome: false}})
	want := (-math.Log(clampProbabilityForLogLoss(0)) - math.Log(1-clampProbabilityForLogLoss(1))) / 2
	if !almostEqual(got, want, 1e-12) {
		t.Fatalf("LogLoss = %v, want %v", got, want)
	}
}

func TestCalibrationBuckets(t *testing.T) {
	buckets := BucketCalibration([]ProbabilityOutcome{
		{P: 0.12, Outcome: false},
		{P: 0.18, Outcome: true},
		{P: 0.82, Outcome: true},
	}, 10)

	if len(buckets) != 10 {
		t.Fatalf("len(buckets) = %d, want %d", len(buckets), 10)
	}
	if buckets[1].Count != 2 {
		t.Fatalf("buckets[1].Count = %d, want %d", buckets[1].Count, 2)
	}
	if buckets[8].Count != 1 {
		t.Fatalf("buckets[8].Count = %d, want %d", buckets[8].Count, 1)
	}
	if !almostEqual(buckets[1].AvgForecast, 0.15, 1e-9) {
		t.Fatalf("buckets[1].AvgForecast = %v, want %v", buckets[1].AvgForecast, 0.15)
	}
	if !almostEqual(buckets[1].HitRate, 0.5, 1e-9) {
		t.Fatalf("buckets[1].HitRate = %v, want %v", buckets[1].HitRate, 0.5)
	}
}

func TestBucketCalibrationEdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		outcomes    []ProbabilityOutcome
		buckets     int
		wantLen     int
		wantCounts  []int
		wantAvg     []float64
		wantHitRate []float64
	}{
		{name: "empty input", outcomes: nil, buckets: 4, wantLen: 4, wantCounts: []int{0, 0, 0, 0}},
		{name: "buckets non-positive", outcomes: []ProbabilityOutcome{{P: 0.5, Outcome: true}}, buckets: 0, wantLen: 0},
		{name: "p zero and one", outcomes: []ProbabilityOutcome{{P: 0, Outcome: false}, {P: 1, Outcome: true}}, buckets: 2, wantLen: 2, wantCounts: []int{1, 1}, wantAvg: []float64{0, 1}, wantHitRate: []float64{0, 1}},
		{name: "out of range finite probabilities", outcomes: []ProbabilityOutcome{{P: -0.25, Outcome: false}, {P: 1.25, Outcome: true}}, buckets: 2, wantLen: 2, wantCounts: []int{1, 1}, wantAvg: []float64{0, 1}, wantHitRate: []float64{0, 1}},
		{name: "exact bucket boundaries", outcomes: []ProbabilityOutcome{{P: 0, Outcome: false}, {P: 0.25, Outcome: true}, {P: 0.5, Outcome: false}, {P: 0.75, Outcome: true}, {P: 1, Outcome: true}}, buckets: 4, wantLen: 4, wantCounts: []int{1, 1, 1, 2}, wantAvg: []float64{0, 0.25, 0.5, 0.875}, wantHitRate: []float64{0, 1, 0, 1}},
		{name: "non-finite probabilities ignored", outcomes: []ProbabilityOutcome{{P: math.NaN(), Outcome: true}, {P: math.Inf(1), Outcome: true}, {P: math.Inf(-1), Outcome: false}}, buckets: 3, wantLen: 3, wantCounts: []int{0, 0, 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BucketCalibration(tt.outcomes, tt.buckets)
			if len(got) != tt.wantLen {
				t.Fatalf("len(BucketCalibration()) = %d, want %d", len(got), tt.wantLen)
			}
			if tt.buckets <= 0 {
				if got != nil {
					t.Fatalf("BucketCalibration() = %v, want nil", got)
				}
				return
			}
			for i := range tt.wantCounts {
				if got[i].Count != tt.wantCounts[i] {
					t.Fatalf("bucket %d count = %d, want %d", i, got[i].Count, tt.wantCounts[i])
				}
			}
			if tt.wantAvg != nil {
				for i := range tt.wantAvg {
					if !almostEqual(got[i].AvgForecast, tt.wantAvg[i], 1e-9) {
						t.Fatalf("bucket %d avg forecast = %v, want %v", i, got[i].AvgForecast, tt.wantAvg[i])
					}
				}
			}
			if tt.wantHitRate != nil {
				for i := range tt.wantHitRate {
					if !almostEqual(got[i].HitRate, tt.wantHitRate[i], 1e-9) {
						t.Fatalf("bucket %d hit rate = %v, want %v", i, got[i].HitRate, tt.wantHitRate[i])
					}
				}
			}
		})
	}
}
