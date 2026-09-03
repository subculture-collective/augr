package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/economicid"
	"github.com/PatrickFanella/get-rich-quick/internal/experimentrun"
	"github.com/PatrickFanella/get-rich-quick/internal/generativestrategy"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

type GenerativeStrategyRepo struct {
	pool       *pgxpool.Pool
	afterStage func(string) error
}

var (
	_ generativestrategy.Store                  = (*GenerativeStrategyRepo)(nil)
	_ generativestrategy.ResearchStore          = (*GenerativeStrategyRepo)(nil)
	_ generativestrategy.EligibleResearchSource = (*GenerativeStrategyRepo)(nil)
)

func NewGenerativeStrategyRepo(pool *pgxpool.Pool) *GenerativeStrategyRepo {
	return &GenerativeStrategyRepo{pool: pool}
}

func (r *GenerativeStrategyRepo) RegisterStrategyFamily(ctx context.Context, family *strategycatalog.Family) (*strategycatalog.Family, error) {
	if r == nil || r.pool == nil {
		return nil, fmt.Errorf("postgres: generated strategy family repository is not configured")
	}
	return NewStrategyCatalogRepo(r.pool).RegisterStrategyFamily(ctx, family)
}

func (r *GenerativeStrategyRepo) RegisterStrategyVersion(ctx context.Context, version *strategycatalog.Version) (*strategycatalog.Version, error) {
	if r == nil || r.pool == nil {
		return nil, fmt.Errorf("postgres: generated strategy version repository is not configured")
	}
	return NewStrategyCatalogRepo(r.pool).RegisterStrategyVersion(ctx, version)
}

func (r *GenerativeStrategyRepo) ListEligibleGeneratedResearch(
	ctx context.Context,
	accountID, scopeID uuid.UUID,
	limit int,
	now time.Time,
) ([]generativestrategy.EligibleResearch, error) {
	if r == nil || r.pool == nil || accountID == uuid.Nil || scopeID == uuid.Nil || limit <= 0 ||
		limit > generativestrategy.MaximumResearchBatchSize || now.IsZero() || now.Location() != time.UTC || !now.Equal(now.Truncate(time.Microsecond)) {
		return nil, fmt.Errorf("postgres: exact generated research account, scope, limit, and time are required")
	}
	rows, err := r.pool.Query(ctx, `
		SELECT experiment.id,scenario.id
		FROM paper_evaluation_scopes scope
		JOIN dataset_manifests manifest ON manifest.sha256=scope.manifest_sha256
		JOIN dataset_quality_results quality ON quality.manifest_id=manifest.id AND quality.sha256=scope.quality_sha256 AND NOT quality.quarantined
		JOIN simulation_policy_artifacts simulation ON simulation.sha256=scope.simulation_policy_sha256
		JOIN account_capital_policy_bindings binding ON binding.id=scope.capital_binding_id AND binding.account_id=scope.account_id
		JOIN capital_margin_policy_artifacts capital ON capital.id=binding.policy_artifact_id AND capital.sha256=scope.capital_policy_sha256
		JOIN research_experiments experiment ON experiment.account_id=scope.account_id AND experiment.capital_binding_id=binding.id
			AND experiment.manifest_id=manifest.id AND experiment.quality_result_id=quality.id
			AND experiment.simulation_policy_version=simulation.policy_version AND experiment.capital_policy_version=capital.policy_version
			AND experiment.evaluation_start=scope.evaluation_start AND experiment.evaluation_end=scope.evaluation_end AND NOT experiment.dataset_quarantined
		JOIN generated_strategy_compilation_receipts receipt ON receipt.version_id=experiment.version_id
		JOIN generated_strategy_scenarios scenario ON scenario.spec_id=receipt.spec_id AND scenario.manifest_id=manifest.id
			AND scenario.mode=experiment.mode AND scenario.evaluation_start=experiment.evaluation_start AND scenario.evaluation_end=experiment.evaluation_end
		WHERE scope.id=$1 AND scope.account_id=$2
			AND NOT EXISTS(SELECT 1 FROM experiment_run_results result WHERE result.experiment_id=experiment.id)
		ORDER BY experiment.created_at,experiment.id
		LIMIT $3`, scopeID, accountID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: list exact generated research: %w", err)
	}
	defer rows.Close()
	items := make([]generativestrategy.EligibleResearch, 0, limit)
	for rows.Next() {
		var experimentID, scenarioID uuid.UUID
		if err := rows.Scan(&experimentID, &scenarioID); err != nil {
			return nil, fmt.Errorf("postgres: scan exact generated research: %w", err)
		}
		prepared, err := r.restorePreparedResearch(ctx, experimentID, scenarioID)
		if err != nil {
			return nil, err
		}
		attemptID := economicid.DeterministicUUID("generated-research-attempt", experimentID.String(), now.Format("2006-01-02T15:04:05.000000Z"))
		items = append(items, generativestrategy.EligibleResearch{
			ScopeID: scopeID, Prepared: prepared, AttemptID: attemptID, StartedAt: now, FinishedAt: now,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list exact generated research: %w", err)
	}
	return items, nil
}

func (r *GenerativeStrategyRepo) restorePreparedResearch(ctx context.Context, experimentID, scenarioID uuid.UUID) (*generativestrategy.PreparedResearch, error) {
	scenario, err := r.GetScenario(ctx, scenarioID)
	if err != nil {
		return nil, fmt.Errorf("postgres: restore generated research scenario: %w", err)
	}
	spec, version, receipt, err := r.GetCompilation(ctx, scenario.SpecID())
	if err != nil {
		return nil, fmt.Errorf("postgres: restore generated research compilation: %w", err)
	}
	experiment, err := NewStrategyCatalogRepo(r.pool).GetResearchExperiment(ctx, experimentID)
	if err != nil {
		return nil, fmt.Errorf("postgres: restore generated research experiment: %w", err)
	}
	identity, err := experimentrun.NewProgramIdentity(experimentrun.ProgramIdentityInput{
		VersionID: version.ID(), VersionSHA256: version.Digest(), CompilerKind: version.CompilerKind(), CompilerVersion: version.CompilerVersion(),
		SourceCommit: version.SourceCommit(), SourceTreeSHA256: version.SourceTreeSHA256(), DecisionContract: version.DecisionContract(),
		AdapterKind: generativestrategy.ScenarioAdapterKindV1, AdapterVersion: generativestrategy.ScenarioAdapterVersionV1,
		AdapterSHA256: generativestrategy.ScenarioAdapterSHA256(spec, scenario), RunnerContract: experimentrun.RunnerContractV1,
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: bind generated research program: %w", err)
	}
	program, err := generativestrategy.NewProgram(identity, spec, version, scenario)
	if err != nil {
		return nil, fmt.Errorf("postgres: restore generated research program: %w", err)
	}
	if experiment.VersionID() != version.ID() || experiment.ManifestID() != scenario.ManifestID() ||
		experiment.Mode() != scenario.Mode() || !experiment.EvaluationStart().Equal(scenario.EvaluationStart()) || !experiment.EvaluationEnd().Equal(scenario.EvaluationEnd()) {
		return nil, fmt.Errorf("postgres: generated research experiment and scenario do not reconstruct")
	}
	return &generativestrategy.PreparedResearch{
		Spec: spec, Version: version, Receipt: receipt, Scenario: scenario, Experiment: experiment, Program: program,
	}, nil
}

func (r *GenerativeStrategyRepo) DeclareResearchExperiment(ctx context.Context, value *strategycatalog.Experiment) (*strategycatalog.Experiment, error) {
	if r == nil || r.pool == nil {
		return nil, fmt.Errorf("postgres: generated strategy research repository is not configured")
	}
	return NewStrategyCatalogRepo(r.pool).DeclareResearchExperiment(ctx, value)
}

type generatedSpecEnvelope struct {
	Schema       string            `json:"schema"`
	FamilyID     string            `json:"family_id"`
	FamilySHA256 string            `json:"family_sha256"`
	SpecKey      string            `json:"spec_key"`
	Inputs       []json.RawMessage `json:"inputs"`
	Universe     struct {
		Instruments []string `json:"instruments"`
	} `json:"universe"`
	ProhibitedBehaviors []string          `json:"prohibited_behaviors"`
	PropertyTests       []string          `json:"property_tests"`
	ExampleTests        []json.RawMessage `json:"example_tests"`
}

type generatedReceiptEnvelope struct {
	Schema           string `json:"schema"`
	State            string `json:"state"`
	FamilyID         string `json:"family_id"`
	FamilySHA256     string `json:"family_sha256"`
	SpecID           string `json:"spec_id"`
	SpecSHA256       string `json:"spec_sha256"`
	VersionID        string `json:"version_id"`
	VersionSHA256    string `json:"version_sha256"`
	CompilerKind     string `json:"compiler_kind"`
	CompilerVersion  string `json:"compiler_version"`
	SourceCommit     string `json:"source_commit"`
	SourceTreeSHA256 string `json:"source_tree_sha256"`
	ConfigSchema     string `json:"config_schema"`
	DecisionContract string `json:"decision_contract"`
	ConfigSHA256     string `json:"config_sha256"`
}

type generatedScenarioEnvelope struct {
	Schema          string                           `json:"schema"`
	State           string                           `json:"state"`
	SpecID          string                           `json:"spec_id"`
	SpecSHA256      string                           `json:"spec_sha256"`
	ManifestID      string                           `json:"manifest_id"`
	ManifestSHA256  string                           `json:"manifest_sha256"`
	Mode            string                           `json:"mode"`
	EvaluationStart string                           `json:"evaluation_start"`
	EvaluationEnd   string                           `json:"evaluation_end"`
	Frames          []generatedScenarioFrameEnvelope `json:"frames"`
}

type generatedScenarioFrameEnvelope struct {
	Sequence        int                                `json:"sequence"`
	InstrumentID    string                             `json:"instrument_id"`
	VenueContractID string                             `json:"venue_contract_id"`
	DecisionAt      string                             `json:"decision_at"`
	RouteAt         string                             `json:"route_at"`
	Action          string                             `json:"action"`
	Bindings        []generatedScenarioBindingEnvelope `json:"bindings"`
}

type generatedScenarioBindingEnvelope struct {
	Name                   string `json:"name"`
	DatasetKind            string `json:"dataset_kind"`
	Field                  string `json:"field"`
	PayloadID              string `json:"payload_id"`
	PayloadSHA256          string `json:"payload_sha256"`
	PartitionContentSHA256 string `json:"partition_content_sha256"`
	SourceKey              string `json:"source_key"`
	AvailableAt            string `json:"available_at"`
	Value                  string `json:"value"`
}

type generatedNormalizedRow struct {
	kind     string
	sequence int
	raw      json.RawMessage
}

func generatedRows(envelope generatedSpecEnvelope) ([]generatedNormalizedRow, error) {
	rows := make([]generatedNormalizedRow, 0, len(envelope.Inputs)+len(envelope.Universe.Instruments)+len(envelope.ProhibitedBehaviors)+len(envelope.PropertyTests)+len(envelope.ExampleTests))
	for sequence, raw := range envelope.Inputs {
		rows = append(rows, generatedNormalizedRow{"input", sequence, raw})
	}
	for sequence, value := range envelope.Universe.Instruments {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		rows = append(rows, generatedNormalizedRow{"instrument", sequence, raw})
	}
	for sequence, value := range envelope.ProhibitedBehaviors {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		rows = append(rows, generatedNormalizedRow{"prohibition", sequence, raw})
	}
	for sequence, value := range envelope.PropertyTests {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		rows = append(rows, generatedNormalizedRow{"property", sequence, raw})
	}
	for sequence, raw := range envelope.ExampleTests {
		rows = append(rows, generatedNormalizedRow{"example", sequence, raw})
	}
	return rows, nil
}

func (r *GenerativeStrategyRepo) RegisterCompilation(ctx context.Context, spec *generativestrategy.Spec, version *strategycatalog.Version, receipt *generativestrategy.Receipt) (*generativestrategy.Spec, *strategycatalog.Version, *generativestrategy.Receipt, error) {
	if r == nil || r.pool == nil || spec == nil || version == nil || receipt == nil || receipt.SpecID() != spec.ID() || receipt.VersionID() != version.ID() {
		return nil, nil, nil, fmt.Errorf("postgres: generated strategy compilation is required")
	}
	var specEnvelope generatedSpecEnvelope
	var receiptEnvelope generatedReceiptEnvelope
	if err := json.Unmarshal(spec.CanonicalBytes(), &specEnvelope); err != nil {
		return nil, nil, nil, err
	}
	if err := json.Unmarshal(receipt.CanonicalBytes(), &receiptEnvelope); err != nil {
		return nil, nil, nil, err
	}
	rows, err := generatedRows(specEnvelope)
	if err != nil {
		return nil, nil, nil, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var familySHA, versionSHA string
	if err = tx.QueryRow(ctx, `SELECT sha256 FROM strategy_families WHERE id=$1`, spec.FamilyID()).Scan(&familySHA); err != nil || familySHA != specEnvelope.FamilySHA256 {
		return nil, nil, nil, fmt.Errorf("postgres: generated strategy family is missing or changed")
	}
	if err = tx.QueryRow(ctx, `SELECT sha256 FROM strategy_versions WHERE id=$1`, version.ID()).Scan(&versionSHA); err != nil || versionSHA != version.Digest() {
		return nil, nil, nil, fmt.Errorf("postgres: generated strategy version is missing or changed")
	}
	_, err = tx.Exec(ctx, `INSERT INTO generated_strategy_specs(id,schema_name,family_id,family_sha256,spec_key,input_count,instrument_count,prohibition_count,property_count,example_count,normalized_row_count,sha256,canonical_bytes,canonical_json) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,convert_from($13,'UTF8')::jsonb) ON CONFLICT(id) DO NOTHING`, spec.ID(), specEnvelope.Schema, specEnvelope.FamilyID, specEnvelope.FamilySHA256, specEnvelope.SpecKey, len(specEnvelope.Inputs), len(specEnvelope.Universe.Instruments), len(specEnvelope.ProhibitedBehaviors), len(specEnvelope.PropertyTests), len(specEnvelope.ExampleTests), len(rows), spec.Digest(), spec.CanonicalBytes())
	if err != nil {
		return nil, nil, nil, generatedStrategyWriteError("insert spec", err)
	}
	if err = r.stage("parent"); err != nil {
		return nil, nil, nil, err
	}
	for _, row := range rows {
		_, err = tx.Exec(ctx, `INSERT INTO generated_strategy_spec_rows(spec_id,kind,sequence,canonical_row) VALUES($1,$2,$3,$4::jsonb) ON CONFLICT(spec_id,kind,sequence) DO NOTHING`, spec.ID(), row.kind, row.sequence, string(row.raw))
		if err != nil {
			return nil, nil, nil, generatedStrategyWriteError("insert normalized row", err)
		}
		if err = r.stage("row"); err != nil {
			return nil, nil, nil, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO generated_strategy_compilation_receipts(id,schema_name,state,family_id,family_sha256,spec_id,spec_sha256,version_id,version_sha256,compiler_kind,compiler_version,source_commit,source_tree_sha256,config_schema,decision_contract,config_sha256,sha256,canonical_bytes,canonical_json) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,convert_from($18,'UTF8')::jsonb) ON CONFLICT(id) DO NOTHING`, receipt.ID(), receiptEnvelope.Schema, receiptEnvelope.State, receiptEnvelope.FamilyID, receiptEnvelope.FamilySHA256, receiptEnvelope.SpecID, receiptEnvelope.SpecSHA256, receiptEnvelope.VersionID, receiptEnvelope.VersionSHA256, receiptEnvelope.CompilerKind, receiptEnvelope.CompilerVersion, receiptEnvelope.SourceCommit, receiptEnvelope.SourceTreeSHA256, receiptEnvelope.ConfigSchema, receiptEnvelope.DecisionContract, receiptEnvelope.ConfigSHA256, receipt.Digest(), receipt.CanonicalBytes())
	if err != nil {
		return nil, nil, nil, generatedStrategyWriteError("insert receipt", err)
	}
	if err = r.stage("receipt"); err != nil {
		return nil, nil, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, nil, nil, generatedStrategyWriteError("commit", err)
	}
	loadedSpec, loadedVersion, loadedReceipt, err := r.GetCompilation(ctx, spec.ID())
	if err != nil {
		return nil, nil, nil, err
	}
	if !bytes.Equal(loadedSpec.CanonicalBytes(), spec.CanonicalBytes()) || loadedVersion.Digest() != version.Digest() || !bytes.Equal(loadedReceipt.CanonicalBytes(), receipt.CanonicalBytes()) {
		return nil, nil, nil, fmt.Errorf("postgres: generated strategy conflict: %w", repository.ErrIdempotencyConflict)
	}
	return loadedSpec, loadedVersion, loadedReceipt, nil
}

func (r *GenerativeStrategyRepo) GetCompilation(ctx context.Context, specID uuid.UUID) (*generativestrategy.Spec, *strategycatalog.Version, *generativestrategy.Receipt, error) {
	if r == nil || r.pool == nil || specID == uuid.Nil {
		return nil, nil, nil, fmt.Errorf("postgres: generated strategy identity is required")
	}
	var specDigest string
	var specRaw []byte
	var familyID uuid.UUID
	if err := r.pool.QueryRow(ctx, `SELECT sha256,canonical_bytes,family_id FROM generated_strategy_specs WHERE id=$1`, specID).Scan(&specDigest, &specRaw, &familyID); errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil, repository.ErrNotFound
	} else if err != nil {
		return nil, nil, nil, err
	}
	catalog := NewStrategyCatalogRepo(r.pool)
	family, err := catalog.GetStrategyFamily(ctx, familyID)
	if err != nil {
		return nil, nil, nil, err
	}
	spec, err := generativestrategy.SpecFromCanonical(specID, specDigest, specRaw, family)
	if err != nil {
		return nil, nil, nil, err
	}
	var envelope generatedSpecEnvelope
	if err = json.Unmarshal(specRaw, &envelope); err != nil {
		return nil, nil, nil, err
	}
	expectedRows, err := generatedRows(envelope)
	if err != nil {
		return nil, nil, nil, err
	}
	dbRows, err := r.pool.Query(ctx, `SELECT kind,sequence,canonical_row FROM generated_strategy_spec_rows WHERE spec_id=$1 ORDER BY kind,sequence`, specID)
	if err != nil {
		return nil, nil, nil, err
	}
	defer dbRows.Close()
	stored := map[string]json.RawMessage{}
	for dbRows.Next() {
		var kind string
		var sequence int
		var raw []byte
		if err = dbRows.Scan(&kind, &sequence, &raw); err != nil {
			return nil, nil, nil, err
		}
		stored[fmt.Sprintf("%s:%d", kind, sequence)] = raw
	}
	if len(stored) != len(expectedRows) {
		return nil, nil, nil, fmt.Errorf("postgres: generated strategy rows do not reconstruct")
	}
	for _, row := range expectedRows {
		if !jsonEqual(stored[fmt.Sprintf("%s:%d", row.kind, row.sequence)], row.raw) {
			return nil, nil, nil, fmt.Errorf("postgres: generated strategy row does not reconstruct")
		}
	}
	var receiptID, versionID uuid.UUID
	var receiptDigest string
	var receiptRaw []byte
	if err = r.pool.QueryRow(ctx, `SELECT id,version_id,sha256,canonical_bytes FROM generated_strategy_compilation_receipts WHERE spec_id=$1`, specID).Scan(&receiptID, &versionID, &receiptDigest, &receiptRaw); err != nil {
		return nil, nil, nil, err
	}
	version, err := catalog.GetStrategyVersion(ctx, versionID)
	if err != nil {
		return nil, nil, nil, err
	}
	receipt, err := generativestrategy.ReceiptFromCanonical(receiptID, receiptDigest, receiptRaw, spec, version)
	if err != nil {
		return nil, nil, nil, err
	}
	return spec, version, receipt, nil
}

func (r *GenerativeStrategyRepo) RegisterScenario(ctx context.Context, scenario *generativestrategy.Scenario) (*generativestrategy.Scenario, error) {
	if r == nil || r.pool == nil || scenario == nil || scenario.SpecID() == uuid.Nil {
		return nil, fmt.Errorf("postgres: generated strategy scenario is required")
	}
	var envelope generatedScenarioEnvelope
	if err := json.Unmarshal(scenario.CanonicalBytes(), &envelope); err != nil {
		return nil, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var specSHA string
	if err = tx.QueryRow(ctx, `SELECT sha256 FROM generated_strategy_specs WHERE id=$1`, scenario.SpecID()).Scan(&specSHA); err != nil || specSHA != envelope.SpecSHA256 {
		return nil, fmt.Errorf("postgres: generated strategy scenario spec is missing or changed")
	}
	_, err = tx.Exec(ctx, `INSERT INTO generated_strategy_scenarios(id,schema_name,state,spec_id,spec_sha256,manifest_id,manifest_sha256,mode,evaluation_start,evaluation_end,frame_count,sha256,canonical_bytes,canonical_json,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,convert_from($13,'UTF8')::jsonb,$14) ON CONFLICT(id) DO NOTHING`,
		scenario.ID(), envelope.Schema, envelope.State, envelope.SpecID, envelope.SpecSHA256, envelope.ManifestID, envelope.ManifestSHA256, envelope.Mode, envelope.EvaluationStart, envelope.EvaluationEnd, len(envelope.Frames), scenario.Digest(), scenario.CanonicalBytes(), databaseNow())
	if err != nil {
		return nil, generatedStrategyWriteError("insert scenario", err)
	}
	if err = r.stage("scenario"); err != nil {
		return nil, err
	}
	for _, frame := range envelope.Frames {
		frameRaw, marshalErr := json.Marshal(frame)
		if marshalErr != nil {
			return nil, marshalErr
		}
		_, err = tx.Exec(ctx, `INSERT INTO generated_strategy_scenario_frames(scenario_id,sequence,instrument_id,venue_contract_id,decision_at,route_at,action,input_count,canonical_frame)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb) ON CONFLICT(scenario_id,sequence) DO NOTHING`, scenario.ID(), frame.Sequence, frame.InstrumentID, frame.VenueContractID, frame.DecisionAt, frame.RouteAt, frame.Action, len(frame.Bindings), string(frameRaw))
		if err != nil {
			return nil, generatedStrategyWriteError("insert scenario frame", err)
		}
		if err = r.stage("scenario_frame"); err != nil {
			return nil, err
		}
		for inputSequence, binding := range frame.Bindings {
			bindingRaw, marshalErr := json.Marshal(binding)
			if marshalErr != nil {
				return nil, marshalErr
			}
			_, err = tx.Exec(ctx, `INSERT INTO generated_strategy_scenario_bindings(scenario_id,frame_sequence,input_sequence,input_name,dataset_kind,field_name,payload_id,payload_sha256,partition_content_sha256,source_key,available_at,canonical_value,canonical_binding)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13::jsonb) ON CONFLICT(scenario_id,frame_sequence,input_sequence) DO NOTHING`, scenario.ID(), frame.Sequence, inputSequence, binding.Name, binding.DatasetKind, binding.Field, binding.PayloadID, binding.PayloadSHA256, binding.PartitionContentSHA256, binding.SourceKey, binding.AvailableAt, binding.Value, string(bindingRaw))
			if err != nil {
				return nil, generatedStrategyWriteError("insert scenario binding", err)
			}
			if err = r.stage("scenario_binding"); err != nil {
				return nil, err
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, generatedStrategyWriteError("commit scenario", err)
	}
	loaded, err := r.GetScenario(ctx, scenario.ID())
	if err != nil {
		return nil, err
	}
	if loaded.Digest() != scenario.Digest() || !bytes.Equal(loaded.CanonicalBytes(), scenario.CanonicalBytes()) {
		return nil, fmt.Errorf("postgres: generated strategy scenario conflict: %w", repository.ErrIdempotencyConflict)
	}
	return loaded, nil
}

func (r *GenerativeStrategyRepo) GetScenario(ctx context.Context, id uuid.UUID) (*generativestrategy.Scenario, error) {
	if r == nil || r.pool == nil || id == uuid.Nil {
		return nil, fmt.Errorf("postgres: generated strategy scenario identity is required")
	}
	var digest string
	var raw []byte
	var specID uuid.UUID
	var manifestID uuid.UUID
	if err := r.pool.QueryRow(ctx, `SELECT sha256,canonical_bytes,spec_id,manifest_id FROM generated_strategy_scenarios WHERE id=$1`, id).Scan(&digest, &raw, &specID, &manifestID); errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	spec, _, _, err := r.GetCompilation(ctx, specID)
	if err != nil {
		return nil, err
	}
	manifest, err := NewDatasetRepo(r.pool).GetDatasetManifest(ctx, manifestID)
	if err != nil {
		return nil, err
	}
	var envelope generatedScenarioEnvelope
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	payloads := map[uuid.UUID]*dataset.MarketPayload{}
	datasets := NewDatasetRepo(r.pool)
	for _, frame := range envelope.Frames {
		for _, binding := range frame.Bindings {
			payloadID, parseErr := uuid.Parse(binding.PayloadID)
			if parseErr != nil {
				return nil, parseErr
			}
			if _, exists := payloads[payloadID]; exists {
				continue
			}
			payloads[payloadID], err = datasets.GetMarketPayload(ctx, payloadID)
			if err != nil {
				return nil, err
			}
		}
	}
	return generativestrategy.ScenarioFromCanonical(id, digest, raw, spec, manifest, payloads)
}

func (r *GenerativeStrategyRepo) stage(value string) error {
	if r.afterStage != nil {
		return r.afterStage(value)
	}
	return nil
}

func generatedStrategyWriteError(action string, err error) error {
	if err != nil && (strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "does not reconstruct") || strings.Contains(err.Error(), "foreign key")) {
		return fmt.Errorf("postgres: generated strategy %s conflict: %w", action, repository.ErrIdempotencyConflict)
	}
	return fmt.Errorf("postgres: generated strategy %s: %w", action, err)
}
