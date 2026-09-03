package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/generativestrategy"
	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

const generatedResearchMaximumFrames = 100000

type generatedPreparationRow struct {
	specID                  uuid.UUID
	versionID               uuid.UUID
	manifestID              uuid.UUID
	qualityResultID         uuid.UUID
	capitalBindingID        uuid.UUID
	simulationPolicyVersion string
	capitalPolicyVersion    string
}

func (r *GenerativeStrategyRepo) ListEligibleGeneratedResearchPreparations(
	ctx context.Context,
	accountID, scopeID uuid.UUID,
	limit int,
) ([]generativestrategy.EligiblePreparation, error) {
	if r == nil || r.pool == nil || accountID == uuid.Nil || scopeID == uuid.Nil || limit <= 0 || limit > generativestrategy.MaximumResearchBatchSize {
		return nil, fmt.Errorf("postgres: exact generated preparation account, scope, and bounded limit are required")
	}
	report, err := NewReportArtifactRepo(r.pool).DiscoveryDeploymentReadinessForScope(ctx, scopeID, accountID)
	if err != nil {
		return nil, fmt.Errorf("postgres: generated preparation scope: %w", err)
	}
	if !report.Stock.Ready {
		return nil, fmt.Errorf("postgres: generated preparation stock evidence: %s", report.Stock.Reason)
	}
	family, err := generativestrategy.ReviewedDailyStockFamily()
	if err != nil {
		return nil, fmt.Errorf("postgres: generated preparation family: %w", err)
	}
	expectedSpecKey := "daily_stock_" + strings.ReplaceAll(scopeID.String(), "-", "")
	rows, err := r.pool.Query(ctx, `
		SELECT spec.id,receipt.version_id,manifest.id,quality.id,binding.id,simulation.policy_version,capital.policy_version
		FROM paper_evaluation_scopes scope
		JOIN dataset_manifests manifest ON manifest.sha256=scope.manifest_sha256
		JOIN dataset_quality_results quality ON quality.manifest_id=manifest.id AND quality.sha256=scope.quality_sha256 AND NOT quality.quarantined
		JOIN simulation_policy_artifacts simulation ON simulation.sha256=scope.simulation_policy_sha256
		JOIN account_capital_policy_bindings binding ON binding.id=scope.capital_binding_id AND binding.account_id=scope.account_id
		JOIN capital_margin_policy_artifacts capital ON capital.id=binding.policy_artifact_id AND capital.sha256=scope.capital_policy_sha256
		JOIN generated_strategy_specs spec ON spec.spec_key=$3 AND spec.family_id=$4
		JOIN generated_strategy_compilation_receipts receipt ON receipt.spec_id=spec.id
		WHERE scope.id=$1 AND scope.account_id=$2
		  AND NOT EXISTS(
			SELECT 1 FROM research_experiments experiment
			WHERE experiment.version_id=receipt.version_id AND experiment.account_id=scope.account_id
			  AND experiment.capital_binding_id=binding.id AND experiment.manifest_id=manifest.id
			  AND experiment.quality_result_id=quality.id AND experiment.simulation_policy_version=simulation.policy_version
			  AND experiment.capital_policy_version=capital.policy_version AND experiment.mode='paper_scored'
			  AND experiment.evaluation_start=scope.evaluation_start AND experiment.evaluation_end=scope.evaluation_end
		  )
		ORDER BY spec.created_at,spec.id
		LIMIT $5`, scopeID, accountID, expectedSpecKey, family.ID(), limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: list exact generated research preparations: %w", err)
	}
	defer rows.Close()
	candidates := make([]generatedPreparationRow, 0, limit)
	for rows.Next() {
		var candidate generatedPreparationRow
		if err := rows.Scan(
			&candidate.specID, &candidate.versionID, &candidate.manifestID, &candidate.qualityResultID, &candidate.capitalBindingID,
			&candidate.simulationPolicyVersion, &candidate.capitalPolicyVersion,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan exact generated research preparation: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list exact generated research preparations: %w", err)
	}
	items := make([]generativestrategy.EligiblePreparation, 0, len(candidates))
	datasets := NewDatasetRepo(r.pool)
	for _, candidate := range candidates {
		spec, version, _, err := r.GetCompilation(ctx, candidate.specID)
		if err != nil {
			return nil, fmt.Errorf("postgres: load generated preparation compilation: %w", err)
		}
		if spec.SpecKey() != expectedSpecKey || spec.FamilyID() != family.ID() || version.ID() != candidate.versionID || candidate.manifestID != report.ManifestID ||
			candidate.qualityResultID != report.QualityResultID || candidate.capitalBindingID == uuid.Nil {
			return nil, fmt.Errorf("postgres: generated preparation graph does not match the exact scope")
		}
		bound, err := datasets.LoadBoundMarketDataset(ctx, candidate.manifestID)
		if err != nil {
			return nil, fmt.Errorf("postgres: load generated preparation dataset: %w", err)
		}
		executionInput, err := spec.PreferredExecutionInput()
		if err != nil {
			return nil, fmt.Errorf("postgres: select generated preparation execution input: %w", err)
		}
		contracts, err := r.generatedPreparationContracts(ctx, spec, bound, report.EvaluationStart, report.EvaluationEnd)
		if err != nil {
			return nil, err
		}
		seedBytes := sha256.Sum256([]byte(candidate.specID.String() + "\x00" + scopeID.String()))
		seed := int64(binary.BigEndian.Uint64(seedBytes[:8]) & math.MaxInt64)
		items = append(items, generativestrategy.EligiblePreparation{
			Key: candidate.specID.String(),
			Request: generativestrategy.ResearchRequest{
				SpecID: candidate.specID, ExpectedVersionID: candidate.versionID, Dataset: bound,
				QualityResultID: candidate.qualityResultID, DatasetQuarantined: false,
				AccountID: accountID, CapitalBindingID: candidate.capitalBindingID,
				SimulationPolicyVersion: candidate.simulationPolicyVersion, CapitalPolicyVersion: candidate.capitalPolicyVersion,
				Mode: strategycatalog.ExperimentPaperScored, EvaluationStart: report.EvaluationStart, EvaluationEnd: report.EvaluationEnd,
				Seed: seed, ExecutionInput: executionInput, VenueContractIDs: contracts, MaximumFrames: generatedResearchMaximumFrames,
			},
		})
	}
	return items, nil
}

func (r *GenerativeStrategyRepo) generatedPreparationContracts(
	ctx context.Context,
	spec *generativestrategy.Spec,
	bound *dataset.BoundMarketDataset,
	evaluationStart, evaluationEnd time.Time,
) (map[uuid.UUID]uuid.UUID, error) {
	providers := make(map[uuid.UUID]string)
	for _, payload := range bound.Payloads() {
		if payload.Kind() != dataset.MarketPayloadStockBar {
			continue
		}
		instrumentID, provider := payload.InstrumentID(), payload.Metadata().Provider
		if prior := providers[instrumentID]; prior != "" && prior != provider {
			return nil, fmt.Errorf("postgres: generated preparation instrument %s has mixed providers", instrumentID)
		}
		providers[instrumentID] = provider
	}
	result := make(map[uuid.UUID]uuid.UUID)
	for _, instrumentID := range spec.Universe().Instruments {
		provider := providers[instrumentID]
		if provider == "" {
			return nil, fmt.Errorf("postgres: generated preparation instrument %s has no immutable stock bars", instrumentID)
		}
		var count int
		var contractID uuid.UUID
		if err := r.pool.QueryRow(ctx, `SELECT count(*),COALESCE(min(id::text),'00000000-0000-0000-0000-000000000000')::uuid
			FROM venue_contracts WHERE instrument_id=$1 AND venue=$2 AND valid_from<=$3 AND (valid_to IS NULL OR valid_to>=$4)`,
			instrumentID, provider, evaluationStart, evaluationEnd).Scan(&count, &contractID); err != nil {
			return nil, fmt.Errorf("postgres: load generated preparation venue contract: %w", err)
		}
		if count != 1 || contractID == uuid.Nil {
			return nil, fmt.Errorf("postgres: generated preparation instrument %s resolves to %d executable venue contracts", instrumentID, count)
		}
		result[instrumentID] = contractID
	}
	return result, nil
}

var _ generativestrategy.EligiblePreparationSource = (*GenerativeStrategyRepo)(nil)
