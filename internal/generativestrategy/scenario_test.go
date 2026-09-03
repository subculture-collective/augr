package generativestrategy

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

func scenarioFixture(t *testing.T) (*Spec, ScenarioInput, map[uuid.UUID]*dataset.MarketPayload) {
	t.Helper()
	_, specInput := specFixture(t)
	specInput.Inputs = []InputField{
		{Name: "average", Type: "decimal", DatasetKind: dataset.KindBars, Field: "vwap", FreshnessSeconds: 60, MissingPolicy: "abstain"},
		{Name: "price", Type: "decimal", DatasetKind: dataset.KindBars, Field: "close", FreshnessSeconds: 60, MissingPolicy: "abstain"},
	}
	specInput.Entry = Expr{Op: "gt", Args: []Expr{{Op: "ref", Ref: "price"}, {Op: "ref", Ref: "average"}}}
	specInput.Exit = Expr{Op: "lt", Args: []Expr{{Op: "ref", Ref: "price"}, {Op: "ref", Ref: "average"}}}
	specInput.ExampleTests = []ExampleTest{{Key: "entry", Values: map[string]string{"price": "101", "average": "100"}, ExpectedEntry: true}}
	spec, err := NewSpec(specInput)
	if err != nil {
		t.Fatal(err)
	}
	instrumentID := specInput.Universe.Instruments[0]
	start := time.Date(2026, 1, 2, 14, 30, 0, 0, time.UTC)
	bar := func(at time.Time, close string) *dataset.MarketPayload {
		payload, payloadErr := dataset.NewMarketPayload(dataset.MarketPayloadInput{
			Kind: dataset.MarketPayloadStockBar, InstrumentID: instrumentID, Provider: "alpaca", Feed: "sip", Symbol: "AAPL", Timeframe: "1Min", AdjustmentPolicy: "all",
			EffectiveAt: at.Add(-time.Minute), ObservedAt: at, AvailableAt: at, Revision: "original",
			Bar: &dataset.BarPayload{Open: "100", High: "102", Low: "98", Close: close, Volume: "1000", TradeCount: "100", VWAP: "100"},
		})
		if payloadErr != nil {
			t.Fatal(payloadErr)
		}
		return payload
	}
	first, second := bar(start.Add(time.Minute), "101"), bar(start.Add(2*time.Minute), "99")
	contractID := uuid.New()
	evidence := func(payload *dataset.MarketPayload, source string) map[string]ScenarioEvidenceInput {
		bound := ScenarioEvidenceInput{Payload: payload, PartitionContentSHA256: strings.Repeat("d", 64), SourceKey: source}
		return map[string]ScenarioEvidenceInput{"price": bound, "average": bound}
	}
	input := ScenarioInput{Spec: spec, Mode: strategycatalog.ExperimentPaperScored, EvaluationStart: start, EvaluationEnd: start.Add(time.Hour), Frames: []DecisionFrameInput{
		{InstrumentID: instrumentID, VenueContractID: contractID, DecisionAt: first.AvailableAt(), RouteAt: first.AvailableAt(), ExecutionInput: "price", EvidenceByInput: evidence(first, "aapl-first")},
		{InstrumentID: instrumentID, VenueContractID: contractID, DecisionAt: second.AvailableAt(), RouteAt: second.AvailableAt(), ExecutionInput: "price", EvidenceByInput: evidence(second, "aapl-second")},
	}}
	return spec, input, map[uuid.UUID]*dataset.MarketPayload{first.ID(): first, second.ID(): second}
}

func TestScenarioDerivesActionsOnlyFromImmutablePayloads(t *testing.T) {
	t.Parallel()
	spec, input, payloads := scenarioFixture(t)
	scenario, err := NewScenario(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenario.canonical.Frames) != 2 || scenario.canonical.Frames[0].Action != ScenarioBuy || scenario.canonical.Frames[1].Action != ScenarioSell ||
		scenario.canonical.Frames[0].ExecutionPrice != "101" || scenario.canonical.Frames[1].ExecutionPrice != "99" {
		t.Fatalf("scenario frames = %+v", scenario.canonical.Frames)
	}
	restored, err := ScenarioFromCanonical(scenario.ID(), scenario.Digest(), scenario.CanonicalBytes(), spec, payloads)
	if err != nil || restored.ID() != scenario.ID() || !bytes.Equal(restored.CanonicalBytes(), scenario.CanonicalBytes()) {
		t.Fatalf("restored = %v, %v", restored, err)
	}
}

func TestScenarioRejectsStaleCrossInstrumentAndTamperedEvidence(t *testing.T) {
	t.Parallel()
	spec, input, payloads := scenarioFixture(t)
	input.Frames = input.Frames[:1]
	input.Frames[0].DecisionAt = input.Frames[0].DecisionAt.Add(2 * time.Minute)
	input.Frames[0].RouteAt = input.Frames[0].DecisionAt
	if _, err := NewScenario(input); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale NewScenario() error = %v", err)
	}
	_, validInput, _ := scenarioFixture(t)
	validInput.Frames[0].InstrumentID = uuid.New()
	if _, err := NewScenario(validInput); err == nil {
		t.Fatal("cross-universe scenario accepted")
	}
	_, validInput, _ = scenarioFixture(t)
	scenario, err := NewScenario(validInput)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(scenario.CanonicalBytes(), []byte(`"value":"101"`), []byte(`"value":"102"`), 1)
	if _, err = ScenarioFromCanonical(scenario.ID(), scenario.Digest(), tampered, spec, payloads); err == nil {
		t.Fatal("tampered scenario restored")
	}
}
