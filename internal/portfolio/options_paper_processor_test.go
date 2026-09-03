package portfolio

import (
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestDefinedRiskSpreadFromOpportunitySupportsOnlyFourVerticals(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 3, 15, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		optionType  string
		longStrike  float64
		shortStrike float64
		want        domain.OptionStrategyType
	}{
		{"bull call", "call", 500, 505, domain.StrategyBullCallSpread},
		{"bear call", "call", 505, 500, domain.StrategyBearCallSpread},
		{"bear put", "put", 505, 500, domain.StrategyBearPutSpread},
		{"bull put", "put", 500, 505, domain.StrategyBullPutSpread},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opportunity := strongOptionOpportunity(now, now)
			opportunity.OptionLegs[0].OptionType = test.optionType
			opportunity.OptionLegs[1].OptionType = test.optionType
			opportunity.OptionLegs[0].Strike = test.longStrike
			opportunity.OptionLegs[1].Strike = test.shortStrike
			spread, err := DefinedRiskSpreadFromOpportunity(opportunity)
			if err != nil || spread.StrategyType != test.want || len(spread.Legs) != 2 || spread.MaxRisk != opportunity.MaxLossPerUnit {
				t.Fatalf("spread=%+v err=%v want=%s", spread, err, test.want)
			}
		})
	}
}

func TestDefinedRiskSpreadFromOpportunityRejectsNakedOrChangedPackage(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 3, 15, 0, 0, 0, time.UTC)
	opportunity := strongOptionOpportunity(now, now)
	opportunity.OptionLegs = opportunity.OptionLegs[:1]
	if _, err := DefinedRiskSpreadFromOpportunity(opportunity); err == nil {
		t.Fatal("naked option package accepted")
	}
	opportunity = strongOptionOpportunity(now, now)
	opportunity.OptionLegs[1].Expiry = opportunity.OptionLegs[1].Expiry.AddDate(0, 1, 0)
	if _, err := DefinedRiskSpreadFromOpportunity(opportunity); err == nil {
		t.Fatal("mixed-expiry option package accepted")
	}
}
