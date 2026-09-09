package capital

import (
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// LongBuyingPower derives a bounded equity/ETF long capacity from the same
// reviewed profile used by Assess. It is not options margin or an admission:
// each proposed order must still pass the instrument-specific assessment.
func LongBuyingPower(account domain.Account, binding Binding, policy *Policy, state *State) (decimal.Decimal, error) {
	if policy == nil || state == nil {
		return decimal.Zero, fmt.Errorf("long buying power requires policy and capital state")
	}
	if err := binding.Validate(account, policy); err != nil {
		return decimal.Zero, err
	}
	if err := state.validate(account, binding, policy); err != nil {
		return decimal.Zero, err
	}
	profile, ok := policy.Profile(binding.Profile)
	if !ok || profile.Unlimited || !profile.InitialLong.IsPositive() {
		return decimal.Zero, fmt.Errorf("long buying power requires a finite reviewed profile")
	}
	if state.maintenanceRequirement.GreaterThan(state.equity) {
		return decimal.Zero, nil
	}
	initial := roundCapitalUp(state.longExposure.Mul(profile.InitialLong).Add(state.shortExposure.Mul(profile.InitialShort)), policy.Scale())
	marginCapacity := nonnegative(state.equity.Sub(initial)).Div(profile.InitialLong).RoundFloor(policy.Scale())
	grossCapacity := nonnegative(state.equity.Mul(profile.MaximumGross).Sub(state.grossExposure))
	capacity := decimal.Min(marginCapacity, grossCapacity)
	if binding.Profile == domain.MarginProfileCash {
		capacity = decimal.Min(capacity, nonnegative(state.cash.Sub(binding.Tier.Mul(profile.CashReserve))))
	}
	return capacity, nil
}
