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

func NewProjectionReader(pool *pgxpool.Pool) *ProjectionReader { return &ProjectionReader{pool: pool} }

func (reader *ProjectionReader) GetLatestPortfolioProjection(ctx context.Context, accountID uuid.UUID, generatedAt time.Time) (*repository.ProjectionSnapshot, error) {
	if reader == nil || reader.pool == nil || accountID == uuid.Nil {
		return nil, fmt.Errorf("postgres: projection reader and account ID are required")
	}
	var checkpoint ledger.ProjectionCheckpoint
	var maxMarkAgeMicroseconds int64
	var reconciliation pgtype.Bool
	err := reader.pool.QueryRow(ctx, `SELECT
		c.id,c.account_id,c.projection_type,c.through_transaction_id,c.projection_version,c.as_of,c.fifo_method,c.base_currency,
		c.mark_source,c.mark_namespace,c.max_mark_age_microseconds,c.transaction_count,c.mark_count,c.lot_count,c.match_count,c.position_count,
		c.input_checksum,c.checksum,c.payload_bytes,c.created_at,c.attestation_key_id,c.attestation_hmac,
		(SELECT r.clean AND r.incident_count=0 AND ls.issue_count=0
		 FROM accounts a
		 JOIN venue_local_snapshots ls ON ls.account_id=a.id AND ls.provider=a.venue
		 JOIN venue_reconciliation_runs r ON r.local_snapshot_id=ls.id
		 WHERE a.id=c.account_id AND ls.checkpoint_id=c.id AND ls.horizon_end=c.as_of
		 ORDER BY r.created_at DESC,r.id DESC LIMIT 1)
	FROM projection_checkpoints c
	WHERE c.account_id=$1 AND c.projection_type=$2 AND c.projection_version IS NOT NULL AND c.as_of <= $3
	ORDER BY c.as_of DESC,c.created_at DESC,c.id DESC LIMIT 1`, accountID, ledger.PortfolioProjectionType, generatedAt.UTC()).Scan(
		&checkpoint.ID, &checkpoint.AccountID, &checkpoint.ProjectionType, &checkpoint.ThroughTransactionID,
		&checkpoint.ProjectionVersion, &checkpoint.AsOf, &checkpoint.FIFO, &checkpoint.BaseCurrency,
		&checkpoint.MarkSource, &checkpoint.MarkNamespace, &maxMarkAgeMicroseconds,
		&checkpoint.TransactionCount, &checkpoint.MarkCount, &checkpoint.LotCount, &checkpoint.MatchCount, &checkpoint.PositionCount,
		&checkpoint.InputChecksum, &checkpoint.OutputChecksum, &checkpoint.PayloadBytes, &checkpoint.CreatedAt,
		&checkpoint.AttestationKeyID, &checkpoint.AttestationHMAC, &reconciliation,
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
		ReconciliationAvailable: reconciliation.Valid,
		ReconciliationPassed:    reconciliation.Valid && reconciliation.Bool,
	}, nil
}
