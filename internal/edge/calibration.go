package edge

import "math"

// ProbabilityOutcome pairs a forecast with an observed outcome.
type ProbabilityOutcome struct {
	P       float64
	Outcome bool
}

// CalibrationBucket aggregates forecast calibration in a probability bin.
type CalibrationBucket struct {
	Index       int     `json:"index"`
	Lower       float64 `json:"lower"`
	Upper       float64 `json:"upper"`
	Count       int     `json:"count"`
	AvgForecast float64 `json:"avg_forecast"`
	HitRate     float64 `json:"hit_rate"`
}

const logLossClamp = 1e-12

// BrierScore computes the mean squared error of probabilistic forecasts.
func BrierScore(outcomes []ProbabilityOutcome) float64 {
	if len(outcomes) == 0 {
		return 0
	}

	var sum float64
	for _, o := range outcomes {
		y := 0.0
		if o.Outcome {
			y = 1
		}
		d := o.P - y
		sum += d * d
	}
	return sum / float64(len(outcomes))
}

// LogLoss computes the mean binary cross-entropy with clamped probabilities.
func LogLoss(outcomes []ProbabilityOutcome) float64 {
	if len(outcomes) == 0 {
		return 0
	}

	var sum float64
	for _, o := range outcomes {
		p := clampProbabilityForLogLoss(o.P)
		if o.Outcome {
			sum += -math.Log(p)
		} else {
			sum += -math.Log(1 - p)
		}
	}
	return sum / float64(len(outcomes))
}

// BucketCalibration groups forecasts into equal-width probability bins.
func BucketCalibration(outcomes []ProbabilityOutcome, buckets int) []CalibrationBucket {
	if buckets <= 0 {
		return nil
	}

	result := make([]CalibrationBucket, buckets)
	forecastSum := make([]float64, buckets)
	hitSum := make([]float64, buckets)

	width := 1.0 / float64(buckets)
	for i := 0; i < buckets; i++ {
		result[i] = CalibrationBucket{
			Index: i,
			Lower: float64(i) * width,
			Upper: float64(i+1) * width,
		}
		if i == buckets-1 {
			result[i].Upper = 1
		}
	}

	for _, o := range outcomes {
		if math.IsNaN(o.P) || math.IsInf(o.P, 0) {
			continue
		}
		p := clampProbability(o.P)
		idx := int(math.Floor(p * float64(buckets)))
		if idx >= buckets {
			idx = buckets - 1
		}
		if idx < 0 {
			idx = 0
		}
		bucket := &result[idx]
		bucket.Count++
		forecastSum[idx] += p
		if o.Outcome {
			hitSum[idx]++
		}
	}

	for i := range result {
		if result[i].Count == 0 {
			continue
		}
		count := float64(result[i].Count)
		result[i].AvgForecast = forecastSum[i] / count
		result[i].HitRate = hitSum[i] / count
	}

	return result
}

func clampProbability(p float64) float64 {
	if p < 0 {
		return 0
	}
	if p > 1 {
		return 1
	}
	return p
}

func clampProbabilityForLogLoss(p float64) float64 {
	if p < logLossClamp {
		return logLossClamp
	}
	if p > 1-logLossClamp {
		return 1 - logLossClamp
	}
	return p
}
