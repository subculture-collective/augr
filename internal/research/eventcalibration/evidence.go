package eventcalibration

import (
	"math"
	"sort"
	"time"
)

const (
	logLossClamp      = 1e-12
	maxEvidenceWeight = 1_000_000
)

// EvidenceSample captures one forecasted event and its observed outcome.
type EvidenceSample struct {
	Source              string  `json:"source"`
	ForecastProbability float64 `json:"forecastProbability"`
	Outcome             bool    `json:"outcome"`
	Reliability         float64 `json:"reliability"`
	Weight              float64 `json:"weight"`
}

// SourceCalibration aggregates evidence calibration for a single source.
type SourceCalibration struct {
	Source          string  `json:"source"`
	SampleCount     int     `json:"sampleCount"`
	MeanProbability float64 `json:"meanProbability"`
	OutcomeRate     float64 `json:"outcomeRate"`
	MeanReliability float64 `json:"meanReliability"`
	BrierScore      float64 `json:"brierScore"`
	LogLoss         float64 `json:"logLoss"`
}

// EvidenceCalibrationSummary summarizes evidence calibration across sources.
type EvidenceCalibrationSummary struct {
	GeneratedAt time.Time           `json:"generatedAt"`
	SampleCount int                 `json:"sampleCount"`
	Sources     []SourceCalibration `json:"sources"`
}

type sourceAccumulator struct {
	sampleCount    int
	weightSum      float64
	probabilitySum float64
	outcomeSum     float64
	reliabilitySum float64
	brierSum       float64
	logLossSum     float64
}

// SummarizeEvidence aggregates valid evidence samples into a deterministic summary.
func SummarizeEvidence(samples []EvidenceSample, generatedAt time.Time) EvidenceCalibrationSummary {
	accumulators := make(map[string]*sourceAccumulator)
	totalSamples := 0

	for _, sample := range samples {
		if sample.Source == "" {
			continue
		}

		p, ok := sanitizeProbability(sample.ForecastProbability)
		if !ok {
			continue
		}

		reliability := sanitizeUnit(sample.Reliability)
		weight := sanitizeWeight(sample.Weight)

		y := 0.0
		if sample.Outcome {
			y = 1
		}

		acc := accumulators[sample.Source]
		if acc == nil {
			acc = &sourceAccumulator{}
			accumulators[sample.Source] = acc
		}

		probabilityDelta := weight * p
		outcomeDelta := weight * y
		reliabilityDelta := weight * reliability
		brierDelta := weight * brierContribution(p, sample.Outcome)
		logLossDelta := weight * logLossContribution(p, sample.Outcome)
		if !isFinite(probabilityDelta) || !isFinite(outcomeDelta) || !isFinite(reliabilityDelta) || !isFinite(brierDelta) || !isFinite(logLossDelta) {
			continue
		}

		if !canAddFinite(acc.weightSum, weight) || !canAddFinite(acc.probabilitySum, probabilityDelta) || !canAddFinite(acc.outcomeSum, outcomeDelta) || !canAddFinite(acc.reliabilitySum, reliabilityDelta) || !canAddFinite(acc.brierSum, brierDelta) || !canAddFinite(acc.logLossSum, logLossDelta) {
			continue
		}

		acc.sampleCount++
		acc.weightSum += weight
		acc.probabilitySum += probabilityDelta
		acc.outcomeSum += outcomeDelta
		acc.reliabilitySum += reliabilityDelta
		acc.brierSum += brierDelta
		acc.logLossSum += logLossDelta
		totalSamples++
	}

	sources := make([]SourceCalibration, 0, len(accumulators))
	if totalSamples == 0 {
		return EvidenceCalibrationSummary{GeneratedAt: generatedAt, SampleCount: 0, Sources: sources}
	}

	orderedSources := make([]string, 0, len(accumulators))
	for source := range accumulators {
		orderedSources = append(orderedSources, source)
	}
	sort.Strings(orderedSources)

	for _, source := range orderedSources {
		acc := accumulators[source]
		weightSum := acc.weightSum
		if weightSum <= 0 || !isFinite(weightSum) {
			weightSum = 1
		}

		calibration := SourceCalibration{
			Source:          source,
			SampleCount:     acc.sampleCount,
			MeanProbability: acc.probabilitySum / weightSum,
			OutcomeRate:     acc.outcomeSum / weightSum,
			MeanReliability: acc.reliabilitySum / weightSum,
			BrierScore:      acc.brierSum / weightSum,
			LogLoss:         acc.logLossSum / weightSum,
		}
		if !calibration.isFinite() {
			continue
		}
		sources = append(sources, calibration)
	}

	return EvidenceCalibrationSummary{
		GeneratedAt: generatedAt,
		SampleCount: totalSamples,
		Sources:     sources,
	}
}

func sanitizeProbability(p float64) (float64, bool) {
	if !isFinite(p) {
		return 0, false
	}
	if p < 0 {
		return 0, true
	}
	if p > 1 {
		return 1, true
	}
	return p, true
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func sanitizeUnit(v float64) float64 {
	if !isFinite(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func sanitizeWeight(v float64) float64 {
	if !isFinite(v) || v <= 0 {
		return 1
	}
	if v > maxEvidenceWeight {
		return maxEvidenceWeight
	}
	return v
}

func canAddFinite(a, b float64) bool {
	return isFinite(a) && isFinite(b) && isFinite(a+b)
}

func (c SourceCalibration) isFinite() bool {
	return isFinite(c.MeanProbability) &&
		isFinite(c.OutcomeRate) &&
		isFinite(c.MeanReliability) &&
		isFinite(c.BrierScore) &&
		isFinite(c.LogLoss)
}

func brierContribution(p float64, outcome bool) float64 {
	y := 0.0
	if outcome {
		y = 1
	}
	delta := p - y
	return delta * delta
}

func logLossContribution(p float64, outcome bool) float64 {
	if p < logLossClamp {
		p = logLossClamp
	}
	if p > 1-logLossClamp {
		p = 1 - logLossClamp
	}
	if outcome {
		return -math.Log(p)
	}
	return -math.Log(1 - p)
}
