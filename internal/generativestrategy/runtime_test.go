package generativestrategy

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
)

func runtimeSpecFixture(t *testing.T) *Spec {
	t.Helper()
	family, err := ReviewedDailyStockFamily()
	if err != nil {
		t.Fatal(err)
	}
	instrumentID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	scopeID := uuid.MustParse("20000000-0000-4000-8000-000000000002")
	key, _ := ReviewedDailyStockSpecKey(scopeID, instrumentID)
	spec, err := NewSpec(SpecInput{
		Family: family, SpecKey: key,
		Inputs:   []InputField{{Name: "price", Type: "decimal", DatasetKind: dataset.KindBars, Field: "close", FreshnessSeconds: 86400, MissingPolicy: "abstain"}},
		Universe: Universe{AssetClass: instrument.AssetClassEquity, Instruments: []uuid.UUID{instrumentID}, Benchmark: instrumentID},
		Entry:    Expr{Op: "gt", Args: []Expr{{Op: "ref", Ref: "price"}, {Op: "decimal", Value: "100"}}},
		Exit:     Expr{Op: "lt", Args: []Expr{{Op: "ref", Ref: "price"}, {Op: "decimal", Value: "90"}}},
		Sizing:   Sizing{Mode: "fixed_fraction", Value: "0.1", MaxPosition: "0.2"}, MaximumHoldingSeconds: 604800,
		Costs: Costs{SpreadBPS: "10", FeeBPS: "1", SlippageBPS: "2"}, Capacity: Capacity{MaximumDailyTurnover: "100000", MaximumParticipation: "0.05"},
		ProhibitedBehaviors: append([]string(nil), requiredProhibitions...), PropertyTests: append([]string(nil), requiredProperties...),
		ExampleTests: []ExampleTest{{Key: "entry_true", Values: map[string]string{"price": "101"}, ExpectedEntry: true}},
		Retirement:   Retirement{MaximumDrawdown: "0.2", MinimumSamples: 100, MaximumFailedChecks: 3},
		Authoring:    Authoring{Provider: "openai", Model: "test", PromptSHA256: strings.Repeat("a", 64), Currency: "USD", Cost: "0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestRuntimeBindingReconstructsAndEvaluatesExactSpec(t *testing.T) {
	spec := runtimeSpecFixture(t)
	var err error
	version, _, err := Compile(spec, strings.Repeat("a", 40), strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := NewRuntimeBinding(spec, version, "0.025", "0.91")
	if err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]any{"generated_strategy": json.RawMessage(binding.CanonicalBytes()), "research_lifecycle": map[string]any{"stage": "shadow"}})
	rebuilt, err := ParseRuntimeBinding(config)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 3, 15, 0, 0, 0, time.UTC)
	decision, err := rebuilt.EvaluateBar(now, RuntimeBar{Timestamp: now.Add(-time.Hour), Open: 99, High: 105, Low: 98, Close: 101, Volume: 10000}, false, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != "buy" || decision.ExecutionPrice != 101 || decision.ProposedNotional != 2000 || decision.LiquidityUSD != 1010000 || decision.SpreadPct != 0.001 {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestRuntimeBindingFailsClosedOnTamperingAndStaleInput(t *testing.T) {
	spec := runtimeSpecFixture(t)
	version, _, _ := Compile(spec, strings.Repeat("a", 40), strings.Repeat("b", 64))
	binding, _ := NewRuntimeBinding(spec, version, "0.025", "0.91")
	config, _ := json.Marshal(map[string]any{"generated_strategy": json.RawMessage(binding.CanonicalBytes())})
	var outer map[string]any
	_ = json.Unmarshal(config, &outer)
	outer["generated_strategy"].(map[string]any)["source_version_sha256"] = strings.Repeat("f", 64)
	tampered, _ := json.Marshal(outer)
	if _, err := ParseRuntimeBinding(tampered); err == nil {
		t.Fatal("tampered generated runtime binding was accepted")
	}
	now := time.Date(2026, 9, 3, 15, 0, 0, 0, time.UTC)
	if _, err := binding.EvaluateBar(now, RuntimeBar{Timestamp: now.Add(-8 * 24 * time.Hour), Open: 99, High: 105, Low: 98, Close: 101, Volume: 10000}, false, 2000); err == nil {
		t.Fatal("stale generated runtime input was accepted")
	}
}
