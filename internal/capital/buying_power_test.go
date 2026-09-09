package capital

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
)

func TestLongBuyingPowerMatchesReviewedAdmissionCapacity(t *testing.T) {
	for _, test := range []struct {
		profile  domain.MarginProfile
		multiple int64
	}{
		{domain.MarginProfileCash, 1},
		{domain.MarginProfileRegT, 2},
		{domain.MarginProfilePortfolio, 6},
	} {
		t.Run(string(test.profile), func(t *testing.T) {
			fixture := newCapitalStateFixture(t, test.profile, decimal.NewFromInt(test.multiple), nil)
			state, err := StateFromProjection(fixture.account, fixture.binding, fixture.policy, fixture.projection, fixture.instruments)
			if err != nil {
				t.Fatal(err)
			}
			capacity, err := LongBuyingPower(fixture.account, fixture.binding, fixture.policy, state)
			if err != nil {
				t.Fatal(err)
			}
			assessment, err := Assess(AssessmentInput{
				Account: fixture.account, Binding: fixture.binding, Policy: fixture.policy, State: state,
				Instrument: *capitalTestInstrument(t, instrument.AssetClassEquity), Currency: "USD",
				ScenarioID: "buying-power-boundary", Direction: ExposureIncreaseLong, ProposedNotional: capacity,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !capacity.Equal(assessment.AvailableBuyingPower) || assessment.Decision != DecisionAdmitted {
				t.Fatalf("capacity %s differs from assessment %+v", capacity, assessment)
			}
			foreign := fixture.account
			foreign.ID[0] ^= 1
			if _, err := LongBuyingPower(foreign, fixture.binding, fixture.policy, state); err == nil {
				t.Fatal("foreign account accepted")
			}
		})
	}
}

func TestLongBuyingPowerRejectsMissingInputs(t *testing.T) {
	fixture := newCapitalStateFixture(t, domain.MarginProfileRegT, decimal.NewFromInt(2), nil)
	if _, err := LongBuyingPower(fixture.account, fixture.binding, fixture.policy, nil); err == nil {
		t.Fatal("missing state accepted")
	}
	if _, err := LongBuyingPower(fixture.account, fixture.binding, nil, &State{}); err == nil {
		t.Fatal("missing policy accepted")
	}
}

func TestLongBuyingPowerAccountsForHeldExposureAndMaintenance(t *testing.T) {
	for _, test := range []struct {
		name  string
		cash  int64
		value string
		want  string
	}{
		{"held long", 50000, "50000", "150000"},
		{"maintenance breach", -400000, "500000", "0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCapitalStateFixtureForMode(t, domain.AccountEnvironmentPaperScored,
				decimal.NewFromInt(100000), domain.MarginProfileRegT, decimal.NewFromInt(2), decimal.NewFromInt(test.cash),
				[]capitalPosition{{assetClass: instrument.AssetClassEquity, quantity: "1000", marketValue: test.value}})
			state, err := StateFromProjection(fixture.account, fixture.binding, fixture.policy, fixture.projection, fixture.instruments)
			if err != nil {
				t.Fatal(err)
			}
			capacity, err := LongBuyingPower(fixture.account, fixture.binding, fixture.policy, state)
			if err != nil {
				t.Fatal(err)
			}
			if !capacity.Equal(decimal.RequireFromString(test.want)) {
				t.Fatalf("capacity=%s, want %s", capacity, test.want)
			}
		})
	}
}
