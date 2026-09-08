package generativestrategy

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/simulation"
	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

type researchStoreFixture struct {
	spec       *Spec
	version    *strategycatalog.Version
	receipt    *Receipt
	scenario   *Scenario
	experiment *strategycatalog.Experiment
	policy     *simulation.PolicyArtifact
}

func (store *researchStoreFixture) RegisterSimulationPolicy(_ context.Context, value *simulation.PolicyArtifact) (*simulation.PolicyArtifact, error) {
	store.policy = value
	return value, nil
}

type preparationSourceFixture struct {
	items []EligiblePreparation
	err   error
}

func (source *preparationSourceFixture) ListEligibleGeneratedResearchPreparations(context.Context, uuid.UUID, uuid.UUID, int) ([]EligiblePreparation, error) {
	return source.items, source.err
}

func (store *researchStoreFixture) GetCompilation(context.Context, uuid.UUID) (*Spec, *strategycatalog.Version, *Receipt, error) {
	return store.spec, store.version, store.receipt, nil
}

func (store *researchStoreFixture) RegisterScenario(_ context.Context, value *Scenario) (*Scenario, error) {
	store.scenario = value
	return value, nil
}

func (store *researchStoreFixture) DeclareResearchExperiment(_ context.Context, value *strategycatalog.Experiment) (*strategycatalog.Experiment, error) {
	store.experiment = value
	return value, nil
}

func researchFixture(t *testing.T) (*ResearchPreparer, ResearchRequest, *researchStoreFixture) {
	t.Helper()
	spec, scenarioInput, payloadMap := scenarioFixture(t)
	version, receipt, err := Compile(spec, strings.Repeat("b", 40), strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	payloads := make([]*dataset.MarketPayload, 0, len(payloadMap))
	for _, payload := range payloadMap {
		payloads = append(payloads, payload)
	}
	bound, err := dataset.NewBoundMarketDataset(scenarioInput.Manifest, payloads)
	if err != nil {
		t.Fatal(err)
	}
	store := &researchStoreFixture{spec: spec, version: version, receipt: receipt}
	preparer, err := NewResearchPreparer(store)
	if err != nil {
		t.Fatal(err)
	}
	request := ResearchRequest{
		SpecID: spec.ID(), ExpectedVersionID: version.ID(), Dataset: bound, QualityResultID: uuid.New(), AccountID: uuid.New(), CapitalBindingID: uuid.New(),
		SimulationPolicyVersion: "simulation-policy-v1@sha256:" + strings.Repeat("d", 64), CapitalPolicyVersion: "capital-margin-policy-v1@sha256:" + strings.Repeat("e", 64),
		Mode: scenarioInput.Mode, EvaluationStart: scenarioInput.EvaluationStart, EvaluationEnd: scenarioInput.EvaluationEnd, Seed: 42, ExecutionInput: "price",
		VenueContractIDs: map[uuid.UUID]uuid.UUID{scenarioInput.Frames[0].InstrumentID: scenarioInput.Frames[0].VenueContractID}, MaximumFrames: 10,
	}
	return preparer, request, store
}

func TestResearchPreparerPersistsExactScenarioAndExperiment(t *testing.T) {
	t.Parallel()
	preparer, request, store := researchFixture(t)
	prepared, err := preparer.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Scenario == nil || prepared.Experiment == nil || prepared.Program == nil || store.scenario != prepared.Scenario || store.experiment != prepared.Experiment ||
		prepared.Experiment.VersionID() != request.ExpectedVersionID || prepared.Experiment.ManifestID() != request.Dataset.Manifest().ID() || prepared.Experiment.AccountID() != request.AccountID ||
		prepared.Program.Identity().VersionID() != request.ExpectedVersionID {
		t.Fatalf("prepared research = %+v", prepared)
	}
}

func TestResearchPreparerRejectsVersionAndQuarantineBeforeProgram(t *testing.T) {
	t.Parallel()
	preparer, request, store := researchFixture(t)
	request.ExpectedVersionID = uuid.New()
	if prepared, err := preparer.Prepare(context.Background(), request); err == nil || prepared != nil || store.scenario != nil {
		t.Fatalf("version mismatch = %+v, %v", prepared, err)
	}
	preparer, request, store = researchFixture(t)
	request.DatasetQuarantined = true
	if prepared, err := preparer.Prepare(context.Background(), request); err == nil || prepared != nil || store.experiment != nil {
		t.Fatalf("quarantined scored research = %+v, %v", prepared, err)
	}
}

func TestPreparationBatchPersistsEachExactEligibleRequest(t *testing.T) {
	t.Parallel()
	preparer, request, store := researchFixture(t)
	source := &preparationSourceFixture{items: []EligiblePreparation{{Key: request.SpecID.String(), Request: request}}}
	service, err := NewPreparationBatchService(source, preparer)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := service.RunEligible(context.Background(), request.AccountID, uuid.New(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Eligible != 1 || summary.Completed != 1 || summary.Failed != 0 || store.scenario == nil || store.experiment == nil {
		t.Fatalf("summary=%+v store=%+v", summary, store)
	}
}

func TestPreparationBatchRejectsDuplicateAndOversizedWork(t *testing.T) {
	t.Parallel()
	preparer, request, _ := researchFixture(t)
	item := EligiblePreparation{Key: request.SpecID.String(), Request: request}
	service, err := NewPreparationBatchService(&preparationSourceFixture{items: []EligiblePreparation{item, item}}, preparer)
	if err != nil {
		t.Fatal(err)
	}
	if summary, runErr := service.RunEligible(context.Background(), request.AccountID, uuid.New(), 2); runErr == nil || summary.Failed != 1 || summary.Completed != 0 {
		t.Fatalf("duplicate summary=%+v error=%v", summary, runErr)
	}
	if summary, runErr := service.RunEligible(context.Background(), request.AccountID, uuid.New(), MaximumResearchBatchSize+1); runErr == nil || summary.Completed != 0 {
		t.Fatalf("oversized summary=%+v error=%v", summary, runErr)
	}
}
