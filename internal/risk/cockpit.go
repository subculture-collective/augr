package risk

import (
	"math"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

var cockpitMarketOrder = []domain.MarketType{
	domain.MarketTypeStock,
	domain.MarketTypeCrypto,
	domain.MarketTypeOptions,
	domain.MarketTypePolymarket,
}

// CockpitExposure summarizes risk activity for a single market type.
type CockpitExposure struct {
	MarketType        domain.MarketType `json:"market_type"`
	OpenPositions     int               `json:"open_positions"`
	ApprovedDecisions int               `json:"approved_decisions"`
	RejectedDecisions int               `json:"rejected_decisions"`
	GrossExposure     float64           `json:"gross_exposure"`
	NetExpectedValue  float64           `json:"net_expected_value"`
}

// CockpitSummary is the aggregated backend view for the risk cockpit.
type CockpitSummary struct {
	GeneratedAt      time.Time         `json:"generated_at"`
	KillSwitchActive bool              `json:"kill_switch_active"`
	CircuitBreaker   bool              `json:"circuit_breaker"`
	Exposures        []CockpitExposure `json:"exposures"`
	Warnings         []string          `json:"warnings"`
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func isOpenExposureStatus(status domain.TradeDecisionStatus) bool {
	switch status {
	case domain.TradeDecisionStatusPaper, domain.TradeDecisionStatusLive:
		return true
	default:
		return false
	}
}

func cockpitWarningForCircuitBreaker(status CircuitBreakerStatus) string {
	switch status.State {
	case CircuitBreakerPhaseTripped:
		if status.Reason != "" {
			return "circuit breaker tripped: " + status.Reason
		}
		return "circuit breaker tripped"
	case CircuitBreakerPhaseCooldown:
		return "circuit breaker cooling down"
	case CircuitBreakerPhaseOpen, "":
		return ""
	default:
		return "circuit breaker active: " + status.State.String()
	}
}

func cockpitExposureByMarket(decisions []domain.TradeDecision) map[domain.MarketType]*CockpitExposure {
	byMarket := make(map[domain.MarketType]*CockpitExposure, len(cockpitMarketOrder))
	for _, marketType := range cockpitMarketOrder {
		byMarket[marketType] = &CockpitExposure{MarketType: marketType}
	}
	for _, decision := range decisions {
		bucket, ok := byMarket[decision.MarketType]
		if !ok {
			continue
		}
		switch decision.RiskStatus {
		case domain.RiskDecisionApproved:
			bucket.ApprovedDecisions++
			approvedSize := math.Abs(decision.ApprovedSize)
			if isOpenExposureStatus(decision.Status) && isFinite(decision.ApprovedSize) && approvedSize > 0 {
				bucket.OpenPositions++
				bucket.GrossExposure += approvedSize
				if isFinite(decision.NetEV) {
					bucket.NetExpectedValue += decision.NetEV
				}
			}
		case domain.RiskDecisionRejected:
			bucket.RejectedDecisions++
		}
	}
	return byMarket
}

// BuildCockpitSummary aggregates trade decisions and risk status into a
// deterministic cockpit snapshot suitable for API responses and tests.
func BuildCockpitSummary(decisions []domain.TradeDecision, status *EngineStatus, generatedAt time.Time) CockpitSummary {
	byMarket := cockpitExposureByMarket(decisions)
	result := CockpitSummary{
		GeneratedAt: generatedAt,
		Exposures:   make([]CockpitExposure, 0, len(cockpitMarketOrder)),
		Warnings:    make([]string, 0, 8),
	}

	if len(decisions) == 0 {
		result.Warnings = append(result.Warnings, "no trade decisions available")
	}

	if status != nil {
		result.KillSwitchActive = status.KillSwitch.Active
		result.CircuitBreaker = status.CircuitBreaker.State != "" && status.CircuitBreaker.State != CircuitBreakerPhaseOpen
		if status.KillSwitch.Active {
			result.Warnings = append(result.Warnings, "kill switch active")
		}
		if warning := cockpitWarningForCircuitBreaker(status.CircuitBreaker); warning != "" {
			result.Warnings = append(result.Warnings, warning)
		}
	}

	for _, marketType := range cockpitMarketOrder {
		exposure := *byMarket[marketType]
		result.Exposures = append(result.Exposures, exposure)
		if exposure.ApprovedDecisions == 0 && exposure.RejectedDecisions > 0 {
			result.Warnings = append(result.Warnings, "market "+string(marketType)+" has rejected decisions but no approved exposure")
		}
	}

	return result
}
