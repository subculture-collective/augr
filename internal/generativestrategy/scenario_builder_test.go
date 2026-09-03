package generativestrategy

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
)

func TestBuildScenarioFromExactBoundDataset(t *testing.T) {
	t.Parallel()
	spec, input, payloadMap := scenarioFixture(t)
	payloads := make([]*dataset.MarketPayload, 0, len(payloadMap))
	for _, payload := range payloadMap {
		payloads = append(payloads, payload)
	}
	bound, err := dataset.NewBoundMarketDataset(input.Manifest, payloads)
	if err != nil {
		t.Fatal(err)
	}
	contractID := input.Frames[0].VenueContractID
	scenario, err := BuildScenarioFromDataset(ScenarioBuildRequest{
		Spec: spec, Dataset: bound, Mode: input.Mode, EvaluationStart: input.EvaluationStart, EvaluationEnd: input.EvaluationEnd,
		ExecutionInput: "price", VenueContractIDs: map[uuid.UUID]uuid.UUID{input.Frames[0].InstrumentID: contractID}, MaximumFrames: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(scenario.canonical.Frames) != 2 || scenario.canonical.Frames[0].Action != ScenarioBuy || scenario.canonical.Frames[1].Action != ScenarioSell || scenario.ManifestID() != input.Manifest.ID() {
		t.Fatalf("scenario = %s", scenario.CanonicalBytes())
	}
}

func TestBuildScenarioUsesImmutablePublicationTimeForHistoricalReplay(t *testing.T) {
	t.Parallel()
	spec, fixture, payloadMap := scenarioFixture(t)
	var original *dataset.MarketPayload
	for _, payload := range payloadMap {
		original = payload
		break
	}
	publication := original.AvailableAt()
	importedAt := fixture.EvaluationEnd.Add(time.Hour)
	metadata := original.Metadata()
	payload, err := dataset.NewMarketPayload(dataset.MarketPayloadInput{
		Kind: metadata.Kind, InstrumentID: metadata.InstrumentID, Provider: metadata.Provider, Feed: metadata.Feed,
		Symbol: metadata.Symbol, Timeframe: metadata.Timeframe, AdjustmentPolicy: metadata.AdjustmentPolicy,
		EffectiveAt: metadata.EffectiveAt, PublishedAt: &publication, ObservedAt: importedAt, AvailableAt: importedAt,
		Revision: metadata.Revision, Bar: original.Bar(),
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := dataset.NewManifest(dataset.ManifestInput{DecisionCutoff: importedAt, Partitions: []dataset.PartitionInput{{
		Kind: dataset.KindBars, Provider: metadata.Provider, Source: "historical-bars", Namespace: "alpaca/sip",
		RequestSHA256: strings.Repeat("a", 64), MediaType: "application/json", SymbologyVersion: "alpaca-v1",
		AdjustmentPolicy: metadata.AdjustmentPolicy, Timezone: "UTC", Calendar: "XNYS", Revision: "original", License: "licensed", RetentionPolicy: "immutable",
		Observations: []dataset.ObservationInput{
			{
				SourceKey: "historical", InstrumentID: metadata.InstrumentID, EffectiveAt: metadata.EffectiveAt,
				PublishedAt: &publication, ObservedAt: importedAt, AvailableAt: importedAt,
				Revision: metadata.Revision, ContentSHA256: payload.Digest(),
			},
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := dataset.NewBoundMarketDataset(manifest, []*dataset.MarketPayload{payload})
	if err != nil {
		t.Fatal(err)
	}
	scenario, err := BuildScenarioFromDataset(ScenarioBuildRequest{
		Spec: spec, Dataset: bound, Mode: fixture.Mode, EvaluationStart: fixture.EvaluationStart, EvaluationEnd: fixture.EvaluationEnd,
		ExecutionInput: "price", VenueContractIDs: map[uuid.UUID]uuid.UUID{metadata.InstrumentID: fixture.Frames[0].VenueContractID}, MaximumFrames: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := scenario.ExecutionEvidence()[0].AvailableAt; got != publication {
		t.Fatalf("scenario replay availability = %s, want %s", got, publication)
	}
}

func TestBuildScenarioFailsClosedOnMissingContractAndFrameLimit(t *testing.T) {
	t.Parallel()
	spec, input, payloadMap := scenarioFixture(t)
	payloads := make([]*dataset.MarketPayload, 0, len(payloadMap))
	for _, payload := range payloadMap {
		payloads = append(payloads, payload)
	}
	bound, err := dataset.NewBoundMarketDataset(input.Manifest, payloads)
	if err != nil {
		t.Fatal(err)
	}
	base := ScenarioBuildRequest{Spec: spec, Dataset: bound, Mode: input.Mode, EvaluationStart: input.EvaluationStart, EvaluationEnd: input.EvaluationEnd, ExecutionInput: "price", MaximumFrames: 10}
	if _, err = BuildScenarioFromDataset(base); err == nil || !strings.Contains(err.Error(), "contracts") {
		t.Fatalf("missing contracts error = %v", err)
	}
	base.VenueContractIDs = map[uuid.UUID]uuid.UUID{input.Frames[0].InstrumentID: input.Frames[0].VenueContractID}
	base.MaximumFrames = 1
	if _, err = BuildScenarioFromDataset(base); err == nil || !strings.Contains(err.Error(), "maximum frame") {
		t.Fatalf("frame limit error = %v", err)
	}
}

func TestSelectScenarioInputRejectsEquallyCurrentRevisions(t *testing.T) {
	t.Parallel()
	spec, input, payloadMap := scenarioFixture(t)
	var original *dataset.MarketPayload
	for _, payload := range payloadMap {
		original = payload
		break
	}
	metadata := original.Metadata()
	changed, err := dataset.NewMarketPayload(dataset.MarketPayloadInput{
		Kind: dataset.MarketPayloadStockBar, InstrumentID: original.InstrumentID(), Provider: metadata.Provider, Feed: metadata.Feed,
		Symbol: metadata.Symbol, Timeframe: metadata.Timeframe, AdjustmentPolicy: metadata.AdjustmentPolicy, EffectiveAt: metadata.EffectiveAt, ObservedAt: metadata.ObservedAt,
		AvailableAt: metadata.AvailableAt, Revision: "correction", CorrectionOfSHA256: original.Digest(), Bar: &dataset.BarPayload{Open: "100", High: "103", Low: "98", Close: "102", Volume: "1000", TradeCount: "100", VWAP: "100"},
	})
	if err != nil {
		t.Fatal(err)
	}
	declaration, _ := scenarioInputDeclaration(spec, "price")
	available := []boundScenarioPayload{{payload: original, available: original.AvailableAt()}, {payload: changed, available: changed.AvailableAt()}}
	if _, err = selectScenarioInput(available, original.InstrumentID(), original.AvailableAt().Add(time.Second), declaration); err == nil || !strings.Contains(err.Error(), "revisions") {
		t.Fatalf("revision ambiguity error = %v (scope %s)", err, input.Manifest.ID())
	}
}
