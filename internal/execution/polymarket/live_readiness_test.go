package polymarket

import (
	"strings"
	"testing"
)

func TestCheckLiveReadiness_DeniesByDefault(t *testing.T) {
	err := CheckLiveReadiness(LiveReadinessInput{})
	if err == nil {
		t.Fatal("expected default readiness check to deny")
	}
	if !strings.Contains(err.Error(), "live trading disabled") {
		t.Fatalf("error = %q, want live trading disabled", err)
	}
}

func TestCheckLiveReadiness_DeniesWithoutCredentials(t *testing.T) {
	in := LiveReadinessInput{
		EnableLiveTrading:   true,
		StrategyAllowlisted: true,
		BrokerAllowlisted:   true,
		PaperBurnInDays:     minLivePaperBurnInDays,
	}
	err := CheckLiveReadiness(in)
	if err == nil {
		t.Fatal("expected missing credentials to deny")
	}
	if !strings.Contains(err.Error(), "missing polymarket credentials") {
		t.Fatalf("error = %q, want missing polymarket credentials", err)
	}
}

func TestCheckLiveReadiness_DeniesWithoutStrategyAllowlist(t *testing.T) {
	in := LiveReadinessInput{
		EnableLiveTrading: true,
		BrokerAllowlisted: true,
		HasCredentials:    true,
		PaperBurnInDays:   minLivePaperBurnInDays,
	}
	err := CheckLiveReadiness(in)
	if err == nil {
		t.Fatal("expected missing strategy allowlist to deny")
	}
	if !strings.Contains(err.Error(), "strategy not allowlisted") {
		t.Fatalf("error = %q, want strategy not allowlisted", err)
	}
}

func TestCheckLiveReadiness_DeniesWithoutBrokerAllowlist(t *testing.T) {
	in := LiveReadinessInput{
		EnableLiveTrading:   true,
		StrategyAllowlisted: true,
		HasCredentials:      true,
		PaperBurnInDays:     minLivePaperBurnInDays,
	}
	err := CheckLiveReadiness(in)
	if err == nil {
		t.Fatal("expected missing broker allowlist to deny")
	}
	if !strings.Contains(err.Error(), "broker not allowlisted") {
		t.Fatalf("error = %q, want broker not allowlisted", err)
	}
}

func TestCheckLiveReadiness_DeniesBurnInAndValidationFailures(t *testing.T) {
	t.Run("burn-in", func(t *testing.T) {
		in := LiveReadinessInput{
			EnableLiveTrading:   true,
			StrategyAllowlisted: true,
			BrokerAllowlisted:   true,
			HasCredentials:      true,
			PaperBurnInDays:     minLivePaperBurnInDays - 1,
		}
		err := CheckLiveReadiness(in)
		if err == nil {
			t.Fatal("expected insufficient burn-in to deny")
		}
		if !strings.Contains(err.Error(), "below minimum") {
			t.Fatalf("error = %q, want burn-in failure", err)
		}
	})

	t.Run("validation failures", func(t *testing.T) {
		in := LiveReadinessInput{
			EnableLiveTrading:   true,
			StrategyAllowlisted: true,
			BrokerAllowlisted:   true,
			HasCredentials:      true,
			PaperBurnInDays:     minLivePaperBurnInDays,
			ValidationFailures:  1,
		}
		err := CheckLiveReadiness(in)
		if err == nil {
			t.Fatal("expected validation failures to deny")
		}
		if !strings.Contains(err.Error(), "validation failures present") {
			t.Fatalf("error = %q, want validation failures failure", err)
		}
	})
}

func TestCheckLiveReadiness_AllGatesPass(t *testing.T) {
	in := LiveReadinessInput{
		EnableLiveTrading:   true,
		StrategyAllowlisted: true,
		BrokerAllowlisted:   true,
		HasCredentials:      true,
		PaperBurnInDays:     minLivePaperBurnInDays,
	}
	if err := CheckLiveReadiness(in); err != nil {
		t.Fatalf("expected readiness to pass, got %v", err)
	}
}
