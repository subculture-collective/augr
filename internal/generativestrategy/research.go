package generativestrategy

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/experimentrun"
	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

type ResearchStore interface {
	GetCompilation(context.Context, uuid.UUID) (*Spec, *strategycatalog.Version, *Receipt, error)
	RegisterScenario(context.Context, *Scenario) (*Scenario, error)
	DeclareResearchExperiment(context.Context, *strategycatalog.Experiment) (*strategycatalog.Experiment, error)
}

type ResearchPreparer struct{ store ResearchStore }

func NewResearchPreparer(store ResearchStore) (*ResearchPreparer, error) {
	if store == nil {
		return nil, fmt.Errorf("generated strategy research store is required")
	}
	return &ResearchPreparer{store: store}, nil
}

type ResearchRequest struct {
	SpecID                  uuid.UUID
	ExpectedVersionID       uuid.UUID
	Dataset                 *dataset.BoundMarketDataset
	QualityResultID         uuid.UUID
	DatasetQuarantined      bool
	AccountID               uuid.UUID
	CapitalBindingID        uuid.UUID
	SimulationPolicyVersion string
	CapitalPolicyVersion    string
	Mode                    strategycatalog.ExperimentMode
	EvaluationStart         time.Time
	EvaluationEnd           time.Time
	Seed                    int64
	ExecutionInput          string
	VenueContractIDs        map[uuid.UUID]uuid.UUID
	MaximumFrames           int
}

type PreparedResearch struct {
	Spec       *Spec
	Version    *strategycatalog.Version
	Receipt    *Receipt
	Scenario   *Scenario
	Experiment *strategycatalog.Experiment
	Program    *Program
}

type EligiblePreparation struct {
	Key     string
	Request ResearchRequest
}

type EligiblePreparationSource interface {
	ListEligibleGeneratedResearchPreparations(context.Context, uuid.UUID, uuid.UUID, int) ([]EligiblePreparation, error)
}

type PreparationBatchSummary struct {
	Eligible  int
	Completed int
	Failed    int
}

type PreparationBatchService struct {
	source   EligiblePreparationSource
	preparer *ResearchPreparer
}

func NewPreparationBatchService(source EligiblePreparationSource, preparer *ResearchPreparer) (*PreparationBatchService, error) {
	if source == nil || preparer == nil {
		return nil, fmt.Errorf("generated research preparation source and preparer are required")
	}
	return &PreparationBatchService{source: source, preparer: preparer}, nil
}

func (service *PreparationBatchService) RunEligible(ctx context.Context, accountID, scopeID uuid.UUID, limit int) (PreparationBatchSummary, error) {
	summary := PreparationBatchSummary{}
	if service == nil || service.source == nil || service.preparer == nil || accountID == uuid.Nil || scopeID == uuid.Nil || limit <= 0 || limit > MaximumResearchBatchSize {
		return summary, fmt.Errorf("generated research preparation requires exact account, scope, and bounded limit")
	}
	items, err := service.source.ListEligibleGeneratedResearchPreparations(ctx, accountID, scopeID, limit)
	if err != nil {
		return summary, fmt.Errorf("list eligible generated research preparations: %w", err)
	}
	if len(items) > limit {
		return summary, fmt.Errorf("generated research preparation source exceeded requested batch limit")
	}
	summary.Eligible = len(items)
	seen := make(map[string]struct{}, len(items))
	for index, item := range items {
		if item.Key == "" || item.Request.SpecID == uuid.Nil || item.Request.ExpectedVersionID == uuid.Nil || item.Request.Dataset == nil {
			summary.Failed++
			return summary, fmt.Errorf("generated research preparation work item %d is incomplete", index)
		}
		if _, duplicate := seen[item.Key]; duplicate {
			summary.Failed++
			return summary, fmt.Errorf("generated research preparation key %q is duplicated", item.Key)
		}
		seen[item.Key] = struct{}{}
	}
	for index, item := range items {
		if _, err := service.preparer.Prepare(ctx, item.Request); err != nil {
			summary.Failed++
			return summary, fmt.Errorf("generated research preparation work item %d: %w", index, err)
		}
		summary.Completed++
	}
	return summary, nil
}

// Prepare reloads the exact compilation, derives and records its immutable
// manifest-bound scenario, and declares one exact experiment. It creates no
// deployment, promotion decision, schedule, allocation, or broker effect.
func (preparer *ResearchPreparer) Prepare(ctx context.Context, request ResearchRequest) (*PreparedResearch, error) {
	if preparer == nil || preparer.store == nil || request.SpecID == uuid.Nil || request.ExpectedVersionID == uuid.Nil || request.Dataset == nil ||
		request.QualityResultID == uuid.Nil || request.AccountID == uuid.Nil || request.CapitalBindingID == uuid.Nil {
		return nil, fmt.Errorf("generated strategy research request requires exact compilation, dataset, quality, account, and capital identities")
	}
	spec, version, receipt, err := preparer.store.GetCompilation(ctx, request.SpecID)
	if err != nil {
		return nil, fmt.Errorf("load generated strategy compilation: %w", err)
	}
	if spec == nil || version == nil || receipt == nil || spec.ID() != request.SpecID || version.ID() != request.ExpectedVersionID || receipt.SpecID() != spec.ID() || receipt.VersionID() != version.ID() {
		return nil, fmt.Errorf("generated strategy compilation does not match the requested identities")
	}
	scenario, err := BuildScenarioFromDataset(ScenarioBuildRequest{
		Spec: spec, Dataset: request.Dataset, Mode: request.Mode,
		EvaluationStart: request.EvaluationStart, EvaluationEnd: request.EvaluationEnd, ExecutionInput: request.ExecutionInput,
		VenueContractIDs: request.VenueContractIDs, MaximumFrames: request.MaximumFrames,
	})
	if err != nil {
		return nil, fmt.Errorf("build generated strategy scenario: %w", err)
	}
	manifest := request.Dataset.Manifest()
	experiment, err := strategycatalog.NewExperiment(strategycatalog.ExperimentInput{
		VersionID: version.ID(), AccountID: request.AccountID,
		CapitalBindingID: request.CapitalBindingID, ManifestID: manifest.ID(), QualityResultID: request.QualityResultID,
		SimulationPolicyVersion: request.SimulationPolicyVersion, CapitalPolicyVersion: request.CapitalPolicyVersion, Mode: request.Mode,
		EvaluationStart: request.EvaluationStart, EvaluationEnd: request.EvaluationEnd, Seed: request.Seed, DatasetQuarantined: request.DatasetQuarantined,
	})
	if err != nil {
		return nil, fmt.Errorf("declare generated strategy experiment: %w", err)
	}
	persistedScenario, err := preparer.store.RegisterScenario(ctx, scenario)
	if err != nil {
		return nil, fmt.Errorf("record generated strategy scenario: %w", err)
	}
	if persistedScenario == nil || persistedScenario.ID() != scenario.ID() || persistedScenario.Digest() != scenario.Digest() {
		return nil, fmt.Errorf("recorded generated strategy scenario diverged")
	}
	persistedExperiment, err := preparer.store.DeclareResearchExperiment(ctx, experiment)
	if err != nil {
		return nil, fmt.Errorf("record generated strategy experiment: %w", err)
	}
	if persistedExperiment == nil || persistedExperiment.ID() != experiment.ID() || persistedExperiment.Digest() != experiment.Digest() {
		return nil, fmt.Errorf("recorded generated strategy experiment diverged")
	}
	identity, err := experimentrun.NewProgramIdentity(experimentrun.ProgramIdentityInput{
		VersionID: version.ID(), VersionSHA256: version.Digest(),
		CompilerKind: version.CompilerKind(), CompilerVersion: version.CompilerVersion(), SourceCommit: version.SourceCommit(), SourceTreeSHA256: version.SourceTreeSHA256(),
		DecisionContract: version.DecisionContract(), AdapterKind: ScenarioAdapterKindV1, AdapterVersion: ScenarioAdapterVersionV1,
		AdapterSHA256: ScenarioAdapterSHA256(spec, persistedScenario), RunnerContract: experimentrun.RunnerContractV1,
	})
	if err != nil {
		return nil, fmt.Errorf("bind generated strategy experiment program: %w", err)
	}
	program, err := NewProgram(identity, spec, version, persistedScenario)
	if err != nil {
		return nil, err
	}
	return &PreparedResearch{Spec: spec, Version: version, Receipt: receipt, Scenario: persistedScenario, Experiment: persistedExperiment, Program: program}, nil
}
