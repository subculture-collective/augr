package optionsstrategy

import (
	"strings"
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/agent/rules"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestCompileIsStableAcrossLegMapOrderAndDecodes(t *testing.T) {
	first := compilerFixture()
	second := compilerFixture()
	second.LegSelection = map[string]rules.LegSelector{"short": first.LegSelection["short"], "long": first.LegSelection["long"]}
	familyA, versionA, err := Compile(first, strings.Repeat("a", 40), strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	familyB, versionB, err := Compile(second, strings.Repeat("a", 40), strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	if familyA.ID() != familyB.ID() || versionA.ID() != versionB.ID() || versionA.Digest() != versionB.Digest() {
		t.Fatal("compiler identity changed with map insertion order")
	}
	decoded, err := Decode(familyA, versionA)
	if err != nil || decoded.StrategyType != first.StrategyType || len(decoded.LegSelection) != 2 {
		t.Fatalf("decoded = %+v, %v", decoded, err)
	}
	runtimeConfig, err := RuntimeConfig(*decoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRuntimeConfig(runtimeConfig, familyA, versionA); err != nil {
		t.Fatal(err)
	}
}

func TestCompileRejectsNonCanonicalOrUndefinedRiskRules(t *testing.T) {
	config := compilerFixture()
	config.Underlying = "aapl"
	if _, _, err := Compile(config, strings.Repeat("a", 40), strings.Repeat("b", 64)); err == nil {
		t.Fatal("noncanonical underlying compiled")
	}
	config = compilerFixture()
	config.StrategyType = domain.StrategyShortStraddle
	if _, _, err := Compile(config, strings.Repeat("a", 40), strings.Repeat("b", 64)); err == nil {
		t.Fatal("undefined-risk strategy compiled")
	}
}

func TestCompileChangesVersionWhenRulesChange(t *testing.T) {
	first := compilerFixture()
	second := compilerFixture()
	selector := second.LegSelection["long"]
	selector.DeltaTarget = 0.55
	second.LegSelection["long"] = selector
	_, versionA, err := Compile(first, strings.Repeat("a", 40), strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	_, versionB, err := Compile(second, strings.Repeat("a", 40), strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	if versionA.ID() == versionB.ID() {
		t.Fatal("changed rules retained version identity")
	}
}

func compilerFixture() rules.OptionsRulesConfig {
	zero := 0.0
	return rules.OptionsRulesConfig{
		Version: 1, StrategyType: domain.StrategyBullCallSpread, Underlying: "AAPL",
		Entry: rules.ConditionGroup{Operator: "AND", Conditions: []rules.Condition{{Field: "close", Op: "gt", Value: &zero}}},
		Exit:  rules.ConditionGroup{Operator: "AND", Conditions: []rules.Condition{{Field: "pnl_pct", Op: "gt", Value: &zero}}},
		LegSelection: map[string]rules.LegSelector{
			"long":  {OptionType: domain.OptionTypeCall, DeltaTarget: 0.6, DTEMin: 20, DTEMax: 60, Side: domain.OrderSideBuy, Intent: domain.PositionIntentBuyToOpen, Ratio: 1},
			"short": {OptionType: domain.OptionTypeCall, DeltaTarget: 0.3, DTEMin: 20, DTEMax: 60, Side: domain.OrderSideSell, Intent: domain.PositionIntentSellToOpen, Ratio: 1},
		},
		PositionSizing: rules.OptionsSizingConfig{Method: "max_risk", MaxRiskUSD: 500},
	}
}
