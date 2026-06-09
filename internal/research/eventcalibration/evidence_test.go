package eventcalibration

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestSummarizeEvidenceEmptySamples(t *testing.T) {
	generatedAt := time.Date(2026, time.June, 9, 12, 0, 0, 0, time.UTC)

	got := SummarizeEvidence(nil, generatedAt)
	if !got.GeneratedAt.Equal(generatedAt) {
		t.Fatalf("GeneratedAt = %v, want %v", got.GeneratedAt, generatedAt)
	}
	if got.SampleCount != 0 {
		t.Fatalf("SampleCount = %d, want 0", got.SampleCount)
	}
	if len(got.Sources) != 0 {
		t.Fatalf("len(Sources) = %d, want 0", len(got.Sources))
	}
	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
}

func TestSummarizeEvidenceSortsSourcesAndAggregatesWeights(t *testing.T) {
	generatedAt := time.Date(2026, time.June, 9, 12, 0, 0, 0, time.UTC)

	got := SummarizeEvidence([]EvidenceSample{
		{Source: "beta", ForecastProbability: 0.25, Outcome: false, Reliability: 0.6, Weight: 2},
		{Source: "alpha", ForecastProbability: 0.9, Outcome: true, Reliability: 0.8, Weight: 1},
		{Source: "beta", ForecastProbability: 0.75, Outcome: true, Reliability: 0.4, Weight: 1},
	}, generatedAt)

	if got.SampleCount != 3 {
		t.Fatalf("SampleCount = %d, want 3", got.SampleCount)
	}
	if len(got.Sources) != 2 {
		t.Fatalf("len(Sources) = %d, want 2", len(got.Sources))
	}
	if got.Sources[0].Source != "alpha" || got.Sources[1].Source != "beta" {
		t.Fatalf("Sources order = %#v, want alpha then beta", got.Sources)
	}

	assertFloatEqual(t, got.Sources[0].MeanProbability, 0.9)
	assertFloatEqual(t, got.Sources[0].OutcomeRate, 1)
	assertFloatEqual(t, got.Sources[0].MeanReliability, 0.8)
	assertFloatEqual(t, got.Sources[0].BrierScore, 0.01)
	assertFloatEqual(t, got.Sources[0].LogLoss, -math.Log(0.9))

	assertFloatEqual(t, got.Sources[1].MeanProbability, (0.25*2+0.75)/3)
	assertFloatEqual(t, got.Sources[1].OutcomeRate, 1.0/3.0)
	assertFloatEqual(t, got.Sources[1].MeanReliability, (0.6*2+0.4)/3)
	assertFloatEqual(t, got.Sources[1].BrierScore, 0.0625)
	assertFloatEqual(t, got.Sources[1].LogLoss, -math.Log(0.75))

	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
}

func TestSummarizeEvidenceIgnoresNonFiniteProbabilities(t *testing.T) {
	got := SummarizeEvidence([]EvidenceSample{
		{Source: "invalid-nan", ForecastProbability: math.NaN(), Outcome: true, Reliability: 0.9, Weight: 1},
		{Source: "invalid-pos-inf", ForecastProbability: math.Inf(1), Outcome: false, Reliability: 0.2, Weight: 1},
		{Source: "invalid-neg-inf", ForecastProbability: math.Inf(-1), Outcome: true, Reliability: 0.4, Weight: 1},
		{Source: "stable", ForecastProbability: 1.2, Outcome: true, Reliability: math.NaN(), Weight: math.Inf(1)},
	}, time.Unix(0, 0).UTC())

	if got.SampleCount != 1 {
		t.Fatalf("SampleCount = %d, want 1", got.SampleCount)
	}
	if len(got.Sources) != 1 {
		t.Fatalf("len(Sources) = %d, want 1", len(got.Sources))
	}
	if got.Sources[0].Source != "stable" {
		t.Fatalf("Source = %q, want %q", got.Sources[0].Source, "stable")
	}
	assertFinite(t, got.Sources[0].MeanProbability)
	assertFinite(t, got.Sources[0].OutcomeRate)
	assertFinite(t, got.Sources[0].MeanReliability)
	assertFinite(t, got.Sources[0].BrierScore)
	assertFinite(t, got.Sources[0].LogLoss)

	if got.Sources[0].MeanProbability != 1 {
		t.Fatalf("MeanProbability = %v, want 1", got.Sources[0].MeanProbability)
	}
	if got.Sources[0].OutcomeRate != 1 {
		t.Fatalf("OutcomeRate = %v, want 1", got.Sources[0].OutcomeRate)
	}
	if got.Sources[0].MeanReliability != 0 {
		t.Fatalf("MeanReliability = %v, want 0", got.Sources[0].MeanReliability)
	}

	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
}

func TestSummarizeEvidenceKeepsHugeFiniteInputsJSONSafe(t *testing.T) {
	got := SummarizeEvidence([]EvidenceSample{
		{Source: "huge", ForecastProbability: 0.6, Outcome: true, Reliability: math.MaxFloat64, Weight: math.MaxFloat64},
		{Source: "huge", ForecastProbability: 0.4, Outcome: false, Reliability: 2, Weight: math.MaxFloat64},
	}, time.Unix(0, 0).UTC())

	if got.SampleCount != 2 {
		t.Fatalf("SampleCount = %d, want 2", got.SampleCount)
	}
	if len(got.Sources) != 1 {
		t.Fatalf("len(Sources) = %d, want 1", len(got.Sources))
	}
	source := got.Sources[0]
	assertFinite(t, source.MeanProbability)
	assertFinite(t, source.OutcomeRate)
	assertFinite(t, source.MeanReliability)
	assertFinite(t, source.BrierScore)
	assertFinite(t, source.LogLoss)
	if source.MeanReliability != 1 {
		t.Fatalf("MeanReliability = %v, want clamped 1", source.MeanReliability)
	}
	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
}

func assertFloatEqual(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func assertFinite(t *testing.T, got float64) {
	t.Helper()
	if math.IsNaN(got) || math.IsInf(got, 0) {
		t.Fatalf("value = %v, want finite", got)
	}
}
