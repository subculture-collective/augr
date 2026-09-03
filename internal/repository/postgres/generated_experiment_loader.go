package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/capital"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/experimentrun"
	"github.com/PatrickFanella/get-rich-quick/internal/generativestrategy"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/simulation"
	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

type ExperimentCapitalStateSource interface {
	LoadExperimentCapitalState(context.Context, *strategycatalog.Experiment, *domain.Account, *capital.Binding, *capital.Policy) (*capital.State, error)
}

type GeneratedExperimentEvidenceLoader struct {
	pool         *pgxpool.Pool
	capitalState ExperimentCapitalStateSource
}

var _ experimentrun.EvidenceLoader = (*GeneratedExperimentEvidenceLoader)(nil)

func NewGeneratedExperimentEvidenceLoader(pool *pgxpool.Pool, capitalState ExperimentCapitalStateSource) (*GeneratedExperimentEvidenceLoader, error) {
	if pool == nil || capitalState == nil {
		return nil, fmt.Errorf("postgres: generated experiment evidence requires database and capital-state source")
	}
	return &GeneratedExperimentEvidenceLoader{pool: pool, capitalState: capitalState}, nil
}

func (loader *GeneratedExperimentEvidenceLoader) LoadExperimentEvidence(ctx context.Context, experimentID uuid.UUID) (*experimentrun.EvidenceGraph, error) {
	if loader == nil || loader.pool == nil || loader.capitalState == nil || experimentID == uuid.Nil {
		return nil, fmt.Errorf("postgres: generated experiment identity and dependencies are required")
	}
	generated := NewGenerativeStrategyRepo(loader.pool)
	scenarioID, err := loader.scenarioID(ctx, experimentID)
	if err != nil {
		return nil, err
	}
	prepared, err := generated.restorePreparedResearch(ctx, experimentID, scenarioID)
	if err != nil {
		return nil, err
	}
	experiment := prepared.Experiment
	account, err := NewAccountRepo(loader.pool).GetByID(ctx, experiment.AccountID())
	if err != nil {
		return nil, fmt.Errorf("postgres: load generated experiment account: %w", err)
	}
	capitalRepo := NewCapitalPolicyRepo(loader.pool)
	binding, err := capitalRepo.GetCapitalBinding(ctx, account.ID)
	if err != nil {
		return nil, fmt.Errorf("postgres: load generated experiment capital binding: %w", err)
	}
	if binding.ID != experiment.CapitalBindingID() {
		return nil, fmt.Errorf("postgres: generated experiment capital binding changed")
	}
	capitalArtifact, err := capitalRepo.GetCapitalPolicyByVersion(ctx, experiment.CapitalPolicyVersion())
	if err != nil {
		return nil, fmt.Errorf("postgres: load generated experiment capital policy: %w", err)
	}
	capitalPolicy, err := capital.PolicyFromArtifact(*capitalArtifact)
	if err != nil {
		return nil, fmt.Errorf("postgres: reconstruct generated experiment capital policy: %w", err)
	}
	simulationArtifact, err := NewSimulationPolicyRepo(loader.pool).GetSimulationPolicyByVersion(ctx, experiment.SimulationPolicyVersion())
	if err != nil {
		return nil, fmt.Errorf("postgres: load generated experiment simulation policy: %w", err)
	}
	simulationPolicy, err := simulationPolicyFromArtifact(simulationArtifact)
	if err != nil {
		return nil, err
	}
	datasets := NewDatasetRepo(loader.pool)
	manifest, err := datasets.GetDatasetManifest(ctx, experiment.ManifestID())
	if err != nil {
		return nil, fmt.Errorf("postgres: load generated experiment manifest: %w", err)
	}
	quality, err := datasets.GetDatasetQualityResult(ctx, experiment.QualityResultID())
	if err != nil {
		return nil, fmt.Errorf("postgres: load generated experiment quality: %w", err)
	}
	state, err := loader.capitalState.LoadExperimentCapitalState(ctx, experiment, account, binding, capitalPolicy)
	if err != nil {
		return nil, fmt.Errorf("postgres: load generated experiment capital state: %w", err)
	}
	if state == nil {
		return nil, fmt.Errorf("postgres: generated experiment capital state is unavailable")
	}
	graph := &experimentrun.EvidenceGraph{
		Experiment: experiment, Version: prepared.Version, Manifest: manifest, Quality: quality, Account: account,
		CapitalBinding: binding, CapitalPolicy: capitalPolicy, CapitalState: state, SimulationPolicy: simulationPolicy,
		Instruments: map[uuid.UUID]*instrument.Instrument{}, VenueContracts: map[uuid.UUID]*instrument.VenueContract{},
	}
	instruments := NewInstrumentRepo(loader.pool)
	for _, evidence := range prepared.Scenario.ExecutionEvidence() {
		payload, err := datasets.GetMarketPayload(ctx, evidence.PayloadID)
		if err != nil {
			return nil, fmt.Errorf("postgres: load generated experiment payload: %w", err)
		}
		if graph.Instruments[evidence.InstrumentID] == nil {
			graph.Instruments[evidence.InstrumentID], err = instruments.GetInstrumentByID(ctx, evidence.InstrumentID)
			if err != nil {
				return nil, fmt.Errorf("postgres: load generated experiment instrument: %w", err)
			}
		}
		contract := graph.VenueContracts[evidence.VenueContractID]
		if contract == nil {
			contract, err = instruments.GetVenueContractByID(ctx, evidence.VenueContractID)
			if err != nil {
				return nil, fmt.Errorf("postgres: load generated experiment venue contract: %w", err)
			}
			graph.VenueContracts[evidence.VenueContractID] = contract
		}
		material, err := generativestrategy.BuildObservationMaterial(evidence, payload, contract)
		if err != nil {
			return nil, fmt.Errorf("postgres: reconstruct generated experiment material: %w", err)
		}
		graph.Observations = append(graph.Observations, material)
	}
	return graph, nil
}

func (loader *GeneratedExperimentEvidenceLoader) scenarioID(ctx context.Context, experimentID uuid.UUID) (uuid.UUID, error) {
	rows, err := loader.pool.Query(ctx, `SELECT scenario.id
		FROM research_experiments experiment
		JOIN generated_strategy_compilation_receipts receipt ON receipt.version_id=experiment.version_id
		JOIN generated_strategy_scenarios scenario ON scenario.spec_id=receipt.spec_id AND scenario.manifest_id=experiment.manifest_id
			AND scenario.mode=experiment.mode AND scenario.evaluation_start=experiment.evaluation_start AND scenario.evaluation_end=experiment.evaluation_end
		WHERE experiment.id=$1 ORDER BY scenario.id LIMIT 2`, experimentID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("postgres: locate generated experiment scenario: %w", err)
	}
	defer rows.Close()
	ids := make([]uuid.UUID, 0, 2)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return uuid.Nil, fmt.Errorf("postgres: scan generated experiment scenario: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return uuid.Nil, fmt.Errorf("postgres: locate generated experiment scenario: %w", err)
	}
	if len(ids) == 0 {
		return uuid.Nil, repository.ErrNotFound
	}
	if len(ids) != 1 {
		return uuid.Nil, fmt.Errorf("postgres: generated experiment scenario is ambiguous")
	}
	return ids[0], nil
}

func simulationPolicyFromArtifact(artifact *simulation.PolicyArtifact) (*simulation.Policy, error) {
	if artifact == nil {
		return nil, errors.New("postgres: generated experiment simulation policy is unavailable")
	}
	policy, err := simulation.PolicyFromArtifact(*artifact)
	if err != nil {
		return nil, fmt.Errorf("postgres: reconstruct generated experiment simulation policy: %w", err)
	}
	return policy, nil
}
