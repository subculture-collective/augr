package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProjectionReader is the HTTP runtime's read-only canonical valuation seam.
type ProjectionReader struct{ pool *pgxpool.Pool }

var _ repository.ProjectionReader = (*ProjectionReader)(nil)
var _ repository.CutoverEvidenceReader = (*ProjectionReader)(nil)

func NewProjectionReader(pool *pgxpool.Pool) *ProjectionReader { return &ProjectionReader{pool: pool} }

func (reader *ProjectionReader) GetLatestPortfolioProjection(ctx context.Context, accountID uuid.UUID, generatedAt time.Time) (*repository.ProjectionSnapshot, error) {
	if reader == nil || reader.pool == nil || accountID == uuid.Nil {
		return nil, fmt.Errorf("postgres: projection reader and account ID are required")
	}
	var checkpoint ledger.ProjectionCheckpoint
	var maxMarkAgeMicroseconds int64
	var reconciliation pgtype.Bool
	var reconciliationAccountID pgtype.UUID
	var reconciliationProvider, reconciliationExternalAccountID pgtype.Text
	var reconciliationGeneratedAt pgtype.Timestamptz
	var freshMarks, staleMarks, unavailableMarks int
	err := reader.pool.QueryRow(ctx, `SELECT
		c.id,c.account_id,c.projection_type,c.through_transaction_id,c.projection_version,c.as_of,c.fifo_method,c.base_currency,
		c.mark_source,c.mark_namespace,c.max_mark_age_microseconds,c.transaction_count,c.mark_count,c.lot_count,c.match_count,c.position_count,
		c.input_checksum,c.checksum,c.payload_bytes,c.created_at,c.attestation_key_id,c.attestation_hmac,
		(SELECT r.clean AND r.incident_count=0 AND ls.issue_count=0
		 FROM accounts a
		 JOIN venue_local_snapshots ls ON ls.account_id=a.id AND ls.provider=a.venue
		 JOIN venue_reconciliation_runs r ON r.local_snapshot_id=ls.id
		 JOIN venue_provider_snapshots ps ON ps.id=r.provider_snapshot_id
		 WHERE a.id=c.account_id AND ls.checkpoint_id=c.id AND ls.horizon_end=c.as_of
		 ORDER BY r.created_at DESC,r.id DESC LIMIT 1),
		(SELECT ls.account_id FROM venue_local_snapshots ls JOIN venue_reconciliation_runs r ON r.local_snapshot_id=ls.id WHERE ls.checkpoint_id=c.id AND ls.horizon_end=c.as_of ORDER BY r.created_at DESC,r.id DESC LIMIT 1),
		(SELECT ps.provider FROM venue_local_snapshots ls JOIN venue_reconciliation_runs r ON r.local_snapshot_id=ls.id JOIN venue_provider_snapshots ps ON ps.id=r.provider_snapshot_id WHERE ls.checkpoint_id=c.id AND ls.horizon_end=c.as_of ORDER BY r.created_at DESC,r.id DESC LIMIT 1),
		(SELECT ps.account_external_id FROM venue_local_snapshots ls JOIN venue_reconciliation_runs r ON r.local_snapshot_id=ls.id JOIN venue_provider_snapshots ps ON ps.id=r.provider_snapshot_id WHERE ls.checkpoint_id=c.id AND ls.horizon_end=c.as_of ORDER BY r.created_at DESC,r.id DESC LIMIT 1),
		(SELECT r.created_at FROM venue_local_snapshots ls JOIN venue_reconciliation_runs r ON r.local_snapshot_id=ls.id WHERE ls.checkpoint_id=c.id AND ls.horizon_end=c.as_of ORDER BY r.created_at DESC,r.id DESC LIMIT 1),
		(SELECT count(*) FROM jsonb_array_elements(c.payload->'positions') p
		 WHERE (p->>'open')::boolean AND p->>'mark_observation_id'<>''),
		(SELECT count(*) FROM jsonb_array_elements(c.payload->'positions') p
		 WHERE (p->>'open')::boolean AND p->>'mark_observation_id'='' AND
		   (SELECT max(m.effective_at) FROM mark_observations m WHERE m.instrument_id=(p->>'instrument_id')::uuid
		     AND m.source=c.mark_source AND m.source_namespace=c.mark_namespace AND m.price_currency=c.base_currency
		     AND m.effective_at<=c.as_of AND m.observed_at<=c.as_of)
		   < c.as_of-c.max_mark_age_microseconds*interval '1 microsecond'),
		(SELECT count(*) FROM jsonb_array_elements(c.payload->'positions') p
		 WHERE (p->>'open')::boolean AND p->>'mark_observation_id'='' AND COALESCE(
		   (SELECT max(m.effective_at) FROM mark_observations m WHERE m.instrument_id=(p->>'instrument_id')::uuid
		     AND m.source=c.mark_source AND m.source_namespace=c.mark_namespace AND m.price_currency=c.base_currency
		     AND m.effective_at<=c.as_of AND m.observed_at<=c.as_of)
		   >= c.as_of-c.max_mark_age_microseconds*interval '1 microsecond',true))
	FROM projection_checkpoints c
	WHERE c.account_id=$1 AND c.projection_type=$2 AND c.projection_version IS NOT NULL AND c.as_of <= $3
	ORDER BY c.as_of DESC,c.created_at DESC,c.id DESC LIMIT 1`, accountID, ledger.PortfolioProjectionType, generatedAt.UTC()).Scan(
		&checkpoint.ID, &checkpoint.AccountID, &checkpoint.ProjectionType, &checkpoint.ThroughTransactionID,
		&checkpoint.ProjectionVersion, &checkpoint.AsOf, &checkpoint.FIFO, &checkpoint.BaseCurrency,
		&checkpoint.MarkSource, &checkpoint.MarkNamespace, &maxMarkAgeMicroseconds,
		&checkpoint.TransactionCount, &checkpoint.MarkCount, &checkpoint.LotCount, &checkpoint.MatchCount, &checkpoint.PositionCount,
		&checkpoint.InputChecksum, &checkpoint.OutputChecksum, &checkpoint.PayloadBytes, &checkpoint.CreatedAt,
		&checkpoint.AttestationKeyID, &checkpoint.AttestationHMAC, &reconciliation, &reconciliationAccountID, &reconciliationProvider, &reconciliationExternalAccountID, &reconciliationGeneratedAt, &freshMarks, &staleMarks, &unavailableMarks,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: get latest portfolio projection for account %s: %w", accountID, err)
	}
	checkpoint.AsOf = checkpoint.AsOf.UTC()
	checkpoint.CreatedAt = checkpoint.CreatedAt.UTC()
	checkpoint.MaxMarkAge = time.Duration(maxMarkAgeMicroseconds) * time.Microsecond
	valuation, err := ledger.DecodeProjectionValuation(&checkpoint)
	if err != nil {
		return nil, fmt.Errorf("postgres: validate latest portfolio projection: %w", err)
	}
	return &repository.ProjectionSnapshot{
		Checkpoint: &checkpoint, Valuation: valuation,
		ReconciliationAvailable:         reconciliation.Valid,
		ReconciliationPassed:            reconciliation.Valid && reconciliation.Bool,
		ReconciliationAccountID:         uuid.UUID(reconciliationAccountID.Bytes),
		ReconciliationProvider:          reconciliationProvider.String,
		ReconciliationExternalAccountID: reconciliationExternalAccountID.String,
		ReconciliationGeneratedAt:       reconciliationGeneratedAt.Time.UTC(),
		FreshMarks:                      freshMarks, StaleMarks: staleMarks, UnavailableMarks: unavailableMarks,
	}, nil
}

func (reader *ProjectionReader) GetCutoverEvidenceInventory(ctx context.Context, accountID uuid.UUID) (*repository.CutoverEvidenceInventory, error) {
	if reader == nil || reader.pool == nil || accountID == uuid.Nil {
		return nil, fmt.Errorf("postgres: cutover evidence reader and account ID are required")
	}
	var inventory repository.CutoverEvidenceInventory
	var scopeID, scopeAccountID pgtype.UUID
	err := reader.pool.QueryRow(ctx, `SELECT
		selected.id,selected.account_id,
		(SELECT count(*) FROM report_artifacts r WHERE r.scope_id=selected.id AND r.status='completed'),
		(SELECT count(*) FROM report_artifacts WHERE scope_id IS NULL),
		(SELECT count(*) FROM report_artifacts r JOIN backtest_runs br ON br.id=r.backtest_run_id WHERE r.scope_id=selected.id AND br.scope_id IS DISTINCT FROM r.scope_id),
		(SELECT count(*) FROM report_artifacts r WHERE r.scope_id=selected.id AND r.status='completed' AND (r.backtest_run_id IS NULL OR r.report_sha256 IS NULL OR r.report_bytes IS NULL))
	FROM (SELECT s.id,s.account_id FROM paper_evaluation_scopes s JOIN report_artifacts r ON r.scope_id=s.id
		 WHERE s.account_id=$1 AND r.status='completed' ORDER BY r.completed_at DESC NULLS LAST,r.id DESC LIMIT 1) selected`, accountID).Scan(
		&scopeID, &scopeAccountID, &inventory.ScopedArtifacts, &inventory.LegacyArtifacts, &inventory.ScopeMismatchCount, &inventory.MissingCanonicalLinks,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &inventory, nil
		}
		return nil, fmt.Errorf("postgres: read cutover evidence inventory: %w", err)
	}
	if scopeID.Valid {
		inventory.ScopeID = uuid.UUID(scopeID.Bytes)
		inventory.ScopeAccountID = uuid.UUID(scopeAccountID.Bytes)
	}
	return &inventory, nil
}
