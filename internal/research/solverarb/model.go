package solverarb

import (
	"math"
	"strings"
)

// OutcomeQuote describes an offline quote for one outcome in a complete set.
type OutcomeQuote struct {
	Outcome  string  `json:"outcome"`
	AskPrice float64 `json:"ask_price"`
	Size     float64 `json:"size"`
}

// OpportunityInput configures a deterministic offline complete-set evaluation.
type OpportunityInput struct {
	MarketID           string         `json:"market_id"`
	Outcomes           []OutcomeQuote `json:"outcomes"`
	FeeRate            float64        `json:"fee_rate"`
	PartialFillHaircut float64        `json:"partial_fill_haircut"`
	MinNetEdge         float64        `json:"min_net_edge"`
}

// Observation is the research-only result of evaluating a complete-set opportunity.
type Observation struct {
	MarketID        string   `json:"market_id"`
	CompleteSetCost float64  `json:"complete_set_cost"`
	GrossEdge       float64  `json:"gross_edge"`
	FeeCost         float64  `json:"fee_cost"`
	HaircutCost     float64  `json:"haircut_cost"`
	NetEdge         float64  `json:"net_edge"`
	Accepted        bool     `json:"accepted"`
	Reasons         []string `json:"reasons"`
}

// EvaluateCompleteSet deterministically evaluates a paper/research-only complete-set opportunity.
func EvaluateCompleteSet(input OpportunityInput) Observation {
	obs := Observation{MarketID: strings.TrimSpace(input.MarketID)}
	reasons := make([]string, 0, 8)

	if obs.MarketID == "" {
		reasons = append(reasons, "invalid_market_id")
	}
	if len(input.Outcomes) == 0 {
		reasons = append(reasons, "invalid_outcomes")
	}
	if !isFiniteNonNegative(input.FeeRate) || input.FeeRate > 1 {
		reasons = append(reasons, "invalid_fee_rate")
	}
	if !isFiniteNonNegative(input.PartialFillHaircut) || input.PartialFillHaircut > 1 {
		reasons = append(reasons, "invalid_partial_fill_haircut")
	}
	if !isFiniteNonNegative(input.MinNetEdge) {
		reasons = append(reasons, "invalid_min_net_edge")
	}

	completeSetCost := 0.0
	for i, q := range input.Outcomes {
		if strings.TrimSpace(q.Outcome) == "" {
			reasons = append(reasons, outcomeReason(i, "missing_outcome"))
		}
		if !isFinitePositive(q.AskPrice) {
			reasons = append(reasons, outcomeReason(i, "invalid_ask_price"))
		}
		if !isFinitePositive(q.Size) {
			reasons = append(reasons, outcomeReason(i, "invalid_size"))
		}
		completeSetCost += q.AskPrice
	}

	if len(reasons) == 0 {
		obs.CompleteSetCost = completeSetCost
		obs.GrossEdge = 1 - completeSetCost
		obs.FeeCost = completeSetCost * input.FeeRate
		haircutBase := obs.GrossEdge
		if haircutBase < 0 {
			haircutBase = 0
		}
		obs.HaircutCost = haircutBase * input.PartialFillHaircut
		obs.NetEdge = obs.GrossEdge - obs.FeeCost - obs.HaircutCost
		if !isFinite(obs.CompleteSetCost) || !isFinite(obs.GrossEdge) || !isFinite(obs.FeeCost) || !isFinite(obs.HaircutCost) || !isFinite(obs.NetEdge) {
			reasons = append(reasons, "non_finite_result")
			obs = Observation{MarketID: obs.MarketID}
		}
	}

	if len(reasons) == 0 && obs.NetEdge < input.MinNetEdge {
		reasons = append(reasons, "insufficient_edge")
	}

	if len(reasons) > 0 && !containsReason(reasons, "insufficient_edge") {
		obs = Observation{MarketID: obs.MarketID}
		obs.Reasons = append([]string(nil), reasons...)
		return obs
	}

	if len(reasons) > 0 {
		obs.Reasons = append([]string(nil), reasons...)
		return obs
	}

	obs.Accepted = true
	return obs
}

func outcomeReason(index int, suffix string) string {
	return "outcome_" + itoa(index) + "_" + suffix
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func isFinitePositive(v float64) bool {
	return isFinite(v) && v > 0
}

func isFiniteNonNegative(v float64) bool {
	return isFinite(v) && v >= 0
}

func containsReason(reasons []string, want string) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
