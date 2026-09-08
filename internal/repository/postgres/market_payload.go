package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type MarketPayloadBinding struct {
	ManifestID          uuid.UUID
	PartitionSequence   int
	ObservationSequence int
	PayloadID           uuid.UUID
	ContentSHA256       string
	CreatedAt           time.Time
}

// RecordBoundMarketDataset atomically persists the immutable payloads, their
// canonical manifest graph, and the one-to-one observation bindings. No
// partially imported dataset becomes visible if any row fails validation.
func (repo *DatasetRepo) RecordBoundMarketDataset(ctx context.Context, value *dataset.BoundMarketDataset, createdAt time.Time) (*dataset.Manifest, error) {
	if repo == nil || repo.pool == nil || value == nil || !validDatasetCreatedAt(createdAt) {
		return nil, fmt.Errorf("postgres: bound market dataset and UTC microsecond creation time are required")
	}
	manifest := value.Manifest()
	validated, err := dataset.ManifestFromCanonical(manifest.ID(), manifest.Digest(), manifest.CanonicalBytes())
	if err != nil {
		return nil, fmt.Errorf("postgres: validate bound market dataset manifest: %w", err)
	}
	tx, err := repo.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, payload := range value.Payloads() {
		if err := insertMarketPayloadTx(ctx, tx, payload, createdAt); err != nil {
			return nil, err
		}
	}
	if err := repo.stage("bound_payloads"); err != nil {
		return nil, err
	}
	if err := insertDatasetManifestTx(ctx, tx, validated, createdAt); err != nil {
		return nil, err
	}
	if err := repo.stage("bound_manifest"); err != nil {
		return nil, err
	}
	for _, binding := range value.Bindings() {
		command, err := tx.Exec(ctx, `INSERT INTO dataset_manifest_payload_bindings(
			manifest_id,partition_sequence,observation_sequence,payload_id,content_sha256,created_at
		) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(manifest_id,partition_sequence,observation_sequence) DO NOTHING`,
			validated.ID(), binding.PartitionSequence, binding.ObservationSequence, binding.PayloadID, binding.ContentSHA256, createdAt)
		if err != nil {
			return nil, fmt.Errorf("postgres: bind imported market payload: %w", err)
		}
		if command.RowsAffected() == 0 {
			var payloadID uuid.UUID
			var digest string
			if err := tx.QueryRow(ctx, `SELECT payload_id,content_sha256 FROM dataset_manifest_payload_bindings
				WHERE manifest_id=$1 AND partition_sequence=$2 AND observation_sequence=$3`,
				validated.ID(), binding.PartitionSequence, binding.ObservationSequence).Scan(&payloadID, &digest); err != nil {
				return nil, fmt.Errorf("postgres: reload imported payload binding: %w", err)
			}
			if payloadID != binding.PayloadID || digest != binding.ContentSHA256 {
				return nil, fmt.Errorf("postgres: imported payload binding changed on retry: %w", repository.ErrIdempotencyConflict)
			}
		}
	}
	if err := repo.stage("bound_bindings"); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres: commit bound market dataset: %w", err)
	}
	return repo.GetDatasetManifest(ctx, validated.ID())
}

func insertMarketPayloadTx(ctx context.Context, tx pgx.Tx, payload *dataset.MarketPayload, createdAt time.Time) error {
	metadata := payload.Metadata()
	command, err := tx.Exec(ctx, `INSERT INTO dataset_market_payloads(
		id,schema_name,payload_kind,instrument_id,underlying_instrument_id,provider,feed,symbol,underlying_symbol,timeframe,
		adjustment_policy,effective_at,published_at,observed_at,available_at,revision,correction_of_sha256,
		content_sha256,canonical_bytes,canonical_json,created_at
	) VALUES($1,$2,$3,$4,NULLIF($5,'')::UUID,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,convert_from($19,'UTF8')::JSONB,$20)
	ON CONFLICT(content_sha256) DO NOTHING`,
		payload.ID(), dataset.MarketPayloadSchemaV1, metadata.Kind, metadata.InstrumentID, optionalUUIDText(metadata.UnderlyingInstrumentID),
		metadata.Provider, metadata.Feed, metadata.Symbol, metadata.UnderlyingSymbol, metadata.Timeframe,
		metadata.AdjustmentPolicy, metadata.EffectiveAt, metadata.PublishedAt, metadata.ObservedAt, metadata.AvailableAt,
		metadata.Revision, metadata.CorrectionOfSHA256, payload.Digest(), payload.CanonicalBytes(), createdAt)
	if err != nil {
		return fmt.Errorf("postgres: insert imported market payload: %w", err)
	}
	if command.RowsAffected() == 0 {
		var id uuid.UUID
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT id,canonical_bytes FROM dataset_market_payloads WHERE content_sha256=$1`, payload.Digest()).Scan(&id, &raw); err != nil {
			return fmt.Errorf("postgres: reload imported market payload: %w", err)
		}
		if id != payload.ID() || !bytes.Equal(raw, payload.CanonicalBytes()) {
			return fmt.Errorf("postgres: imported market payload changed on retry: %w", repository.ErrIdempotencyConflict)
		}
	}
	return nil
}

func insertDatasetManifestTx(ctx context.Context, tx pgx.Tx, manifest *dataset.Manifest, createdAt time.Time) error {
	partitions := manifest.Partitions()
	observationCount := 0
	for _, partition := range partitions {
		observationCount += len(partition.Observations)
	}
	command, err := tx.Exec(ctx, `INSERT INTO dataset_manifests(
		id,schema_name,decision_cutoff,partition_count,observation_count,sha256,canonical_bytes,canonical_json,created_at
	) VALUES($1,$2,$3,$4,$5,$6,$7,convert_from($7,'UTF8')::JSONB,$8) ON CONFLICT(id) DO NOTHING`,
		manifest.ID(), dataset.ManifestSchemaV1, manifest.DecisionCutoff(), len(partitions), observationCount,
		manifest.Digest(), []byte(manifest.CanonicalBytes()), createdAt)
	if err != nil {
		return fmt.Errorf("postgres: insert imported dataset manifest: %w", err)
	}
	if command.RowsAffected() == 0 {
		return verifyDatasetEnvelope(ctx, tx, "dataset_manifests", manifest.ID(), manifest.Digest(), manifest.CanonicalBytes())
	}
	for _, partition := range partitions {
		partitionBytes, err := json.Marshal(partition)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO dataset_manifest_partitions(
			manifest_id,manifest_decision_cutoff,sequence,kind,provider,source_name,namespace,request_sha256,content_sha256,media_type,
			effective_start,effective_end,observed_start,observed_end,available_start,available_end,symbology_version,adjustment_policy,
			timezone_name,calendar_name,revision,supersedes_content_sha256,row_count,license_name,retention_policy,canonical_bytes,canonical_json
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,convert_from($26,'UTF8')::JSONB)`,
			manifest.ID(), manifest.DecisionCutoff(), partition.Sequence, partition.Kind, partition.Provider, partition.Source,
			partition.Namespace, partition.RequestSHA256, partition.ContentSHA256, partition.MediaType, partition.EffectiveStart,
			partition.EffectiveEnd, partition.ObservedStart, partition.ObservedEnd, partition.AvailableStart, partition.AvailableEnd,
			partition.SymbologyVersion, partition.AdjustmentPolicy, partition.Timezone, partition.Calendar, partition.Revision,
			partition.SupersedesContentSHA256, partition.RowCount, partition.License, partition.RetentionPolicy, partitionBytes); err != nil {
			return fmt.Errorf("postgres: insert imported dataset partition: %w", err)
		}
		for _, observation := range partition.Observations {
			observationBytes, err := json.Marshal(observation)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO dataset_manifest_observations(
				manifest_id,manifest_decision_cutoff,partition_sequence,partition_content_sha256,sequence,source_key,instrument_id,
				effective_at,published_at,observed_at,available_at,revision,correction_of,content_sha256,bid,ask,volume,depth,canonical_bytes,canonical_json
			) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,convert_from($19,'UTF8')::JSONB)`,
				manifest.ID(), manifest.DecisionCutoff(), partition.Sequence, partition.ContentSHA256, observation.Sequence,
				observation.SourceKey, nullableDatasetString(observation.InstrumentID), observation.EffectiveAt,
				nullableDatasetString(observation.PublishedAt), observation.ObservedAt, observation.AvailableAt, observation.Revision,
				observation.CorrectionOf, observation.ContentSHA256, observation.Bid, observation.Ask, observation.Volume,
				observation.Depth, observationBytes); err != nil {
				return fmt.Errorf("postgres: insert imported dataset observation: %w", err)
			}
		}
	}
	return nil
}

func (repo *DatasetRepo) RecordMarketPayload(ctx context.Context, payload *dataset.MarketPayload, createdAt time.Time) (*dataset.MarketPayload, error) {
	if repo == nil || repo.pool == nil || payload == nil || createdAt.Location() != time.UTC || !createdAt.Equal(createdAt.Truncate(time.Microsecond)) {
		return nil, fmt.Errorf("postgres: market payload repository, payload, and UTC microsecond creation time are required")
	}
	metadata := payload.Metadata()
	command, err := repo.pool.Exec(ctx, `INSERT INTO dataset_market_payloads(
		id,schema_name,payload_kind,instrument_id,underlying_instrument_id,provider,feed,symbol,underlying_symbol,timeframe,
		adjustment_policy,effective_at,published_at,observed_at,available_at,revision,correction_of_sha256,
		content_sha256,canonical_bytes,canonical_json,created_at
	) VALUES($1,$2,$3,$4,NULLIF($5,'')::UUID,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,convert_from($19,'UTF8')::JSONB,$20)
	ON CONFLICT(content_sha256) DO NOTHING`,
		payload.ID(), dataset.MarketPayloadSchemaV1, metadata.Kind, metadata.InstrumentID, optionalUUIDText(metadata.UnderlyingInstrumentID),
		metadata.Provider, metadata.Feed, metadata.Symbol, metadata.UnderlyingSymbol, metadata.Timeframe,
		metadata.AdjustmentPolicy, metadata.EffectiveAt, metadata.PublishedAt, metadata.ObservedAt, metadata.AvailableAt,
		metadata.Revision, metadata.CorrectionOfSHA256, payload.Digest(), payload.CanonicalBytes(), createdAt)
	if err != nil {
		return nil, fmt.Errorf("postgres: insert market payload: %w", err)
	}
	stored, err := repo.GetMarketPayload(ctx, payload.ID())
	if err != nil {
		return nil, err
	}
	if command.RowsAffected() == 0 && (stored.Digest() != payload.Digest() || !bytes.Equal(stored.CanonicalBytes(), payload.CanonicalBytes())) {
		return nil, fmt.Errorf("postgres: market payload conflict: %w", repository.ErrIdempotencyConflict)
	}
	return stored, nil
}

func (repo *DatasetRepo) GetMarketPayload(ctx context.Context, id uuid.UUID) (*dataset.MarketPayload, error) {
	if repo == nil || repo.pool == nil || id == uuid.Nil {
		return nil, fmt.Errorf("postgres: market payload repository, pool, and identity are required")
	}
	var digest string
	var raw []byte
	err := repo.pool.QueryRow(ctx, `SELECT content_sha256,canonical_bytes FROM dataset_market_payloads WHERE id=$1`, id).Scan(&digest, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("postgres: market payload: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: get market payload: %w", err)
	}
	payload, err := dataset.MarketPayloadFromCanonical(id, digest, raw)
	if err != nil {
		return nil, fmt.Errorf("postgres: reconstruct market payload: %w", err)
	}
	return payload, nil
}

func (repo *DatasetRepo) BindMarketPayload(ctx context.Context, binding MarketPayloadBinding) (*MarketPayloadBinding, error) {
	if repo == nil || repo.pool == nil || binding.ManifestID == uuid.Nil || binding.PayloadID == uuid.Nil ||
		binding.PartitionSequence < 0 || binding.ObservationSequence < 0 || len(binding.ContentSHA256) != 64 ||
		binding.CreatedAt.Location() != time.UTC || !binding.CreatedAt.Equal(binding.CreatedAt.Truncate(time.Microsecond)) {
		return nil, fmt.Errorf("postgres: complete market payload binding is required")
	}
	command, err := repo.pool.Exec(ctx, `INSERT INTO dataset_manifest_payload_bindings(
		manifest_id,partition_sequence,observation_sequence,payload_id,content_sha256,created_at
	) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(manifest_id,partition_sequence,observation_sequence) DO NOTHING`,
		binding.ManifestID, binding.PartitionSequence, binding.ObservationSequence, binding.PayloadID, binding.ContentSHA256, binding.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("postgres: bind market payload: %w", err)
	}
	var stored MarketPayloadBinding
	err = repo.pool.QueryRow(ctx, `SELECT manifest_id,partition_sequence,observation_sequence,payload_id,content_sha256,created_at
		FROM dataset_manifest_payload_bindings WHERE manifest_id=$1 AND partition_sequence=$2 AND observation_sequence=$3`,
		binding.ManifestID, binding.PartitionSequence, binding.ObservationSequence).Scan(
		&stored.ManifestID, &stored.PartitionSequence, &stored.ObservationSequence, &stored.PayloadID, &stored.ContentSHA256, &stored.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: reload market payload binding: %w", err)
	}
	if command.RowsAffected() == 0 && (stored.PayloadID != binding.PayloadID || stored.ContentSHA256 != binding.ContentSHA256) {
		return nil, fmt.Errorf("postgres: market payload binding conflict: %w", repository.ErrIdempotencyConflict)
	}
	return &stored, nil
}

func (repo *DatasetRepo) ListBoundMarketPayloads(ctx context.Context, manifestID uuid.UUID, kind dataset.MarketPayloadKind) ([]*dataset.MarketPayload, error) {
	if repo == nil || repo.pool == nil || manifestID == uuid.Nil {
		return nil, fmt.Errorf("postgres: market payload repository, pool, and manifest are required")
	}
	rows, err := repo.pool.Query(ctx, `SELECT p.id,p.content_sha256,p.canonical_bytes
		FROM dataset_manifest_payload_bindings b
		JOIN dataset_market_payloads p ON p.id=b.payload_id AND p.content_sha256=b.content_sha256
		WHERE b.manifest_id=$1 AND ($2='' OR p.payload_kind=$2)
		ORDER BY b.partition_sequence,b.observation_sequence`, manifestID, kind)
	if err != nil {
		return nil, fmt.Errorf("postgres: list bound market payloads: %w", err)
	}
	defer rows.Close()
	values := make([]*dataset.MarketPayload, 0)
	for rows.Next() {
		var id uuid.UUID
		var digest string
		var raw []byte
		if err := rows.Scan(&id, &digest, &raw); err != nil {
			return nil, fmt.Errorf("postgres: scan bound market payload: %w", err)
		}
		payload, err := dataset.MarketPayloadFromCanonical(id, digest, raw)
		if err != nil {
			return nil, fmt.Errorf("postgres: reconstruct bound market payload: %w", err)
		}
		values = append(values, payload)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list bound market payload rows: %w", err)
	}
	return values, nil
}

// LoadBoundMarketDataset reconstructs an exact persisted manifest and every
// payload reachable through it. It never resolves a latest/current manifest.
func (repo *DatasetRepo) LoadBoundMarketDataset(ctx context.Context, manifestID uuid.UUID) (*dataset.BoundMarketDataset, error) {
	manifest, err := repo.GetDatasetManifest(ctx, manifestID)
	if err != nil {
		return nil, err
	}
	payloads, err := repo.ListBoundMarketPayloads(ctx, manifestID, "")
	if err != nil {
		return nil, err
	}
	bound, err := dataset.NewBoundMarketDataset(manifest, payloads)
	if err != nil {
		return nil, fmt.Errorf("postgres: reconstruct bound market dataset: %w", err)
	}
	return bound, nil
}

func optionalUUIDText(value uuid.UUID) string {
	if value == uuid.Nil {
		return ""
	}
	return value.String()
}
