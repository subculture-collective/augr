package execution

import (
	"testing"

	"github.com/google/uuid"
)

func TestLiveGate_DeniesByDefault(t *testing.T) {
	gate := LiveGateConfig{}
	allowed, denial := gate.Allows(nil, "alpaca")
	if allowed {
		t.Fatal("expected live gate to deny by default")
	}
	if denial.Code != LiveGateReasonLiveTradingDisabled {
		t.Fatalf("denial code = %q, want %q", denial.Code, LiveGateReasonLiveTradingDisabled)
	}
}

func TestLiveGate_DeniesMissingStrategyAllowlist(t *testing.T) {
	strategyID := uuid.New()
	gate := LiveGateConfig{
		EnableLiveTrading: true,
		AllowedBrokers:    map[string]bool{"alpaca": true},
	}
	allowed, denial := gate.Allows(&strategyID, "alpaca")
	if allowed {
		t.Fatal("expected missing strategy allowlist to deny")
	}
	if denial.Code != LiveGateReasonStrategyDenied {
		t.Fatalf("denial code = %q, want %q", denial.Code, LiveGateReasonStrategyDenied)
	}
}

func TestLiveGate_DeniesMissingBrokerAllowlist(t *testing.T) {
	strategyID := uuid.New()
	gate := LiveGateConfig{
		EnableLiveTrading: true,
		AllowedStrategies: map[uuid.UUID]bool{strategyID: true},
	}
	allowed, denial := gate.Allows(&strategyID, "alpaca")
	if allowed {
		t.Fatal("expected missing broker allowlist to deny")
	}
	if denial.Code != LiveGateReasonBrokerDenied {
		t.Fatalf("denial code = %q, want %q", denial.Code, LiveGateReasonBrokerDenied)
	}
}

func TestLiveGate_AllowsExplicitlyAllowlistedStrategyAndBroker(t *testing.T) {
	strategyID := uuid.New()
	gate := LiveGateConfig{
		EnableLiveTrading: true,
		AllowedStrategies: map[uuid.UUID]bool{strategyID: true},
		AllowedBrokers:    map[string]bool{"alpaca": true},
	}
	allowed, denial := gate.Allows(&strategyID, "  ALPACA  ")
	if !allowed {
		t.Fatalf("expected live gate to allow, got denial: %+v", denial)
	}
	if denial.Code != "" || denial.Message != "" {
		t.Fatalf("expected zero denial, got %+v", denial)
	}
}
