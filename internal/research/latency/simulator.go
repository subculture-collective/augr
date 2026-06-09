package latency

import "math"

type SimulationInput struct {
	LatencyMS            float64 `json:"latency_ms"`
	ResolutionWindowMS   float64 `json:"resolution_window_ms"`
	StaleBookProbability float64 `json:"stale_book_probability"`
	ReversalProbability  float64 `json:"reversal_probability"`
	Stake                float64 `json:"stake"`
	EdgeBeforeLatency    float64 `json:"edge_before_latency"`
	MaxLatencyMS         float64 `json:"max_latency_ms"`
	MaxExpectedLoss      float64 `json:"max_expected_loss"`
}

type SimulationResult struct {
	ExpectedLoss        float64  `json:"expected_loss"`
	NetEdgeAfterLatency float64  `json:"net_edge_after_latency"`
	StalePenalty        float64  `json:"stale_penalty"`
	ReversalPenalty     float64  `json:"reversal_penalty"`
	Accepted            bool     `json:"accepted"`
	Reasons             []string `json:"reasons,omitempty"`
}

func Simulate(input SimulationInput) SimulationResult {
	if !validInput(input) {
		return SimulationResult{Reasons: []string{"invalid_input"}}
	}

	latencyRatio := clamp01(input.LatencyMS / input.ResolutionWindowMS)
	windowPressure := 0.5 + 0.5*latencyRatio
	staleExposure := input.StaleBookProbability * windowPressure * latencyRatio
	reversalExposure := input.ReversalProbability * latencyRatio * latencyRatio

	stalePenalty := input.Stake * staleExposure
	reversalPenalty := input.Stake * reversalExposure
	expectedLoss := stalePenalty + reversalPenalty
	netEdge := input.Stake*input.EdgeBeforeLatency - expectedLoss

	result := SimulationResult{
		ExpectedLoss:        expectedLoss,
		NetEdgeAfterLatency: netEdge,
		StalePenalty:        stalePenalty,
		ReversalPenalty:     reversalPenalty,
	}

	if !finiteResult(result) {
		return SimulationResult{Reasons: []string{"invalid_input"}}
	}

	reasons := make([]string, 0, 4)
	if input.LatencyMS > input.MaxLatencyMS || latencyRatio >= 0.85 {
		reasons = append(reasons, "high_latency")
	}
	if stalePenalty > 0 && (stalePenalty >= 0.02*input.Stake || input.StaleBookProbability >= 0.35) {
		reasons = append(reasons, "stale_book_penalty")
	}
	if reversalPenalty > 0 && (reversalPenalty >= 0.02*input.Stake || input.ReversalProbability >= 0.2) {
		reasons = append(reasons, "reversal_tail_risk")
	}
	if result.NetEdgeAfterLatency <= 0 {
		reasons = append(reasons, "negative_net_edge")
	}

	if lossCap := input.MaxExpectedLoss; lossCap > 0 && isFinite(lossCap) && result.ExpectedLoss > lossCap {
		if stalePenalty >= reversalPenalty {
			reasons = append(reasons, "stale_book_penalty")
		} else {
			reasons = append(reasons, "reversal_tail_risk")
		}
	}

	result.Reasons = uniqueReasons(reasons)
	result.Accepted = len(result.Reasons) == 0
	return result
}

func validInput(input SimulationInput) bool {
	if !isFinite(input.LatencyMS) || input.LatencyMS < 0 {
		return false
	}
	if !isFinite(input.ResolutionWindowMS) || input.ResolutionWindowMS <= 0 {
		return false
	}
	if !unitInterval(input.StaleBookProbability) || !unitInterval(input.ReversalProbability) {
		return false
	}
	if !isFinite(input.Stake) || input.Stake < 0 {
		return false
	}
	if !isFinite(input.EdgeBeforeLatency) {
		return false
	}
	if !isFinite(input.MaxLatencyMS) || input.MaxLatencyMS <= 0 {
		return false
	}
	if !isFinite(input.MaxExpectedLoss) || input.MaxExpectedLoss < 0 {
		return false
	}
	return true
}

func finiteResult(result SimulationResult) bool {
	return isFinite(result.ExpectedLoss) && isFinite(result.NetEdgeAfterLatency) && isFinite(result.StalePenalty) && isFinite(result.ReversalPenalty)
}

func uniqueReasons(reasons []string) []string {
	if len(reasons) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(reasons))
	out := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if reason == "" {
			continue
		}
		if _, ok := seen[reason]; ok {
			continue
		}
		seen[reason] = struct{}{}
		out = append(out, reason)
	}
	return out
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func unitInterval(v float64) bool {
	return isFinite(v) && v >= 0 && v <= 1
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
