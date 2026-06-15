package polymarket

import (
	"errors"
	"fmt"
)

const minLivePaperBurnInDays = 60

// LiveReadinessInput captures the static gate inputs for live activation.
// Zero values are deny-by-default.
type LiveReadinessInput struct {
	EnableLiveTrading   bool
	StrategyAllowlisted bool
	BrokerAllowlisted   bool
	HasCredentials      bool
	PaperBurnInDays     int
	ValidationFailures  int
}

// CheckLiveReadiness returns the first explicit failure blocking live trading.
func CheckLiveReadiness(in LiveReadinessInput) error {
	if !in.EnableLiveTrading {
		return errors.New("polymarket live readiness: live trading disabled")
	}
	if !in.StrategyAllowlisted {
		return errors.New("polymarket live readiness: strategy not allowlisted")
	}
	if !in.BrokerAllowlisted {
		return errors.New("polymarket live readiness: broker not allowlisted")
	}
	if !in.HasCredentials {
		return errors.New("polymarket live readiness: missing polymarket credentials")
	}
	if in.PaperBurnInDays < minLivePaperBurnInDays {
		return fmt.Errorf("polymarket live readiness: paper burn-in %d days below minimum %d", in.PaperBurnInDays, minLivePaperBurnInDays)
	}
	if in.ValidationFailures > 0 {
		return fmt.Errorf("polymarket live readiness: %d validation failures present", in.ValidationFailures)
	}
	return nil
}
