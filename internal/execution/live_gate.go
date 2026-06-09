package execution

import (
	"strings"

	"github.com/google/uuid"
)

const (
	LiveGateReasonLiveTradingDisabled = "live_trading_disabled"
	LiveGateReasonStrategyDenied      = "strategy_not_allowlisted"
	LiveGateReasonStrategyMissing     = "strategy_id_required"
	LiveGateReasonBrokerDenied        = "broker_not_allowlisted"
)

// LiveGateConfig controls whether a live execution path may proceed.
// Zero values are intentionally deny-all.
type LiveGateConfig struct {
	EnableLiveTrading bool
	AllowedStrategies map[uuid.UUID]bool
	AllowedBrokers    map[string]bool
}

// LiveGateDenial captures the structured reason an execution was blocked.
type LiveGateDenial struct {
	Code       string     `json:"code"`
	Message    string     `json:"message"`
	StrategyID *uuid.UUID `json:"strategy_id,omitempty"`
	Broker     string     `json:"broker,omitempty"`
}

// Allows returns true only when live trading is explicitly enabled and both the
// strategy and broker are allowlisted.
func (c LiveGateConfig) Allows(strategyID *uuid.UUID, broker string) (bool, LiveGateDenial) {
	normalizedBroker := strings.ToLower(strings.TrimSpace(broker))
	if !c.EnableLiveTrading {
		return false, LiveGateDenial{Code: LiveGateReasonLiveTradingDisabled, Message: "live trading disabled", Broker: normalizedBroker}
	}
	if strategyID == nil {
		return false, LiveGateDenial{Code: LiveGateReasonStrategyMissing, Message: "strategy id required for live trading", Broker: normalizedBroker}
	}
	if !c.AllowedStrategies[*strategyID] {
		return false, LiveGateDenial{Code: LiveGateReasonStrategyDenied, Message: "strategy not live-allowlisted", StrategyID: strategyID, Broker: normalizedBroker}
	}
	if !c.AllowedBrokers[normalizedBroker] {
		return false, LiveGateDenial{Code: LiveGateReasonBrokerDenied, Message: "broker not live-allowlisted", StrategyID: strategyID, Broker: normalizedBroker}
	}
	return true, LiveGateDenial{}
}
