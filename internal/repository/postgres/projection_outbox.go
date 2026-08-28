package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/economicid"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

const (
	ProjectionRequestEconomicFill = "economic_fill"
	ProjectionRequestMarkRebuild  = "mark_rebuild"
)

type ProjectionOutboxItem struct {
	ID                   uuid.UUID
	AccountID            uuid.UUID
	RequestKind          string
	ThroughTransactionID uuid.UUID
	AsOf                 time.Time
	MarkAsOf             *time.Time
	MarkGeneration       uuid.UUID
	MarkSource           string
	MarkNamespace        string
	MaxMarkAge           time.Duration
	Status               string
	AttemptCount         int
	NextAttemptAt        time.Time
	ClaimedBy            string
	ClaimExpiresAt       *time.Time
}

type ProjectionOutboxRepository struct{ pool *pgxpool.Pool }

func NewProjectionOutboxRepository(pool *pgxpool.Pool) *ProjectionOutboxRepository {
	return &ProjectionOutboxRepository{pool: pool}
}

func enqueueEconomicProjectionTx(ctx context.Context, tx pgx.Tx, accountID, throughTransactionID uuid.UUID, asOf time.Time) (uuid.UUID, error) {
	asOf = asOf.UTC().Truncate(time.Microsecond)
	if accountID == uuid.Nil || throughTransactionID == uuid.Nil || asOf.IsZero() {
		return uuid.Nil, fmt.Errorf("postgres: enqueue economic projection: account, frontier, and as-of are required")
	}
	id := economicid.DeterministicUUID("account-projection-outbox-v1", accountID.String(), ProjectionRequestEconomicFill, throughTransactionID.String(), uuid.Nil.String())
	var persistedID uuid.UUID
	var status string
	err := tx.QueryRow(ctx, `INSERT INTO account_projection_outbox (
		id, account_id, request_kind, through_transaction_id, as_of,
		mark_as_of, mark_generation, status, attempt_count, next_attempt_at,
		created_at, updated_at
	) VALUES ($1,$2,$3,$4,$5,NULL,$6,'pending',0,$5,$5,$5)
	ON CONFLICT (account_id,request_kind,through_transaction_id,mark_generation)
	DO UPDATE SET next_attempt_at=LEAST(account_projection_outbox.next_attempt_at,EXCLUDED.next_attempt_at), updated_at=EXCLUDED.updated_at
	WHERE account_projection_outbox.status IN ('pending','retry')
	RETURNING id,status`, id, accountID, ProjectionRequestEconomicFill, throughTransactionID, asOf, uuid.Nil).Scan(&persistedID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return id, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("postgres: enqueue economic projection: %w", err)
	}
	return persistedID, nil
}

var _ repository.ProjectionOutboxRepository = (*ProjectionOutboxRepository)(nil)

func (repo *ProjectionOutboxRepository) LatestProjectionFrontier(ctx context.Context, accountID uuid.UUID, asOf time.Time) (uuid.UUID, error) {
	if repo == nil || repo.pool == nil || accountID == uuid.Nil || asOf.IsZero() {
		return uuid.Nil, fmt.Errorf("postgres: latest projection frontier requires runtime pool, account, and as-of")
	}
	var id uuid.UUID
	err := repo.pool.QueryRow(ctx, `SELECT id FROM ledger_transactions WHERE account_id=$1 AND effective_at<=$2 AND observed_at<=$2 ORDER BY effective_at DESC,observed_at DESC,id DESC LIMIT 1`, accountID, asOf.UTC().Truncate(time.Microsecond)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, repository.ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("postgres: latest projection frontier: %w", err)
	}
	return id, nil
}

func (repo *ProjectionOutboxRepository) RecordMarksAndEnqueueRebuild(ctx context.Context, request repository.ProjectionMarkBatch) (uuid.UUID, error) {
	if repo == nil || repo.pool == nil {
		return uuid.Nil, fmt.Errorf("postgres: projection outbox runtime pool is required")
	}
	request.AsOf = request.AsOf.UTC().Truncate(time.Microsecond)
	request.MarkAsOf = request.MarkAsOf.UTC().Truncate(time.Microsecond)
	request.MaxMarkAge = request.MaxMarkAge.Truncate(time.Microsecond)
	if request.AccountID == uuid.Nil || request.ThroughTransactionID == uuid.Nil || request.AsOf.IsZero() || request.MarkAsOf.IsZero() || request.MarkAsOf.After(request.AsOf) || request.MaxMarkAge <= 0 || len(request.Marks) == 0 {
		return uuid.Nil, fmt.Errorf("postgres: mark rebuild requires account, frontier, times, age, and marks")
	}
	marksByID := make(map[uuid.UUID]*ledger.MarkObservation, len(request.Marks))
	var source, namespace string
	for _, mark := range request.Marks {
		if mark == nil {
			return uuid.Nil, fmt.Errorf("postgres: mark rebuild contains nil mark")
		}
		if err := mark.Validate(); err != nil {
			return uuid.Nil, fmt.Errorf("postgres: mark rebuild: %w", err)
		}
		if source == "" {
			source, namespace = mark.Source, mark.SourceNamespace
		}
		if mark.Source != source || mark.SourceNamespace != namespace {
			return uuid.Nil, fmt.Errorf("postgres: mark rebuild marks must share source and namespace")
		}
		if existing, ok := marksByID[mark.ID]; ok && !ledger.SameMarkObservation(existing, mark) {
			return uuid.Nil, fmt.Errorf("postgres: mark rebuild duplicate ID has changed evidence")
		}
		marksByID[mark.ID] = mark
	}
	ids := make([]string, 0, len(marksByID))
	for id := range marksByID {
		ids = append(ids, id.String())
	}
	sort.Strings(ids)
	generation := economicid.DeterministicUUID("projection-mark-generation-v1", request.AccountID.String(), request.ThroughTransactionID.String(), request.MarkAsOf.Format(time.RFC3339Nano), strings.Join(ids, ","))
	outboxID := economicid.DeterministicUUID("account-projection-outbox-v1", request.AccountID.String(), ProjectionRequestMarkRebuild, request.ThroughTransactionID.String(), generation.String())

	tx, err := repo.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, fmt.Errorf("postgres: begin mark rebuild transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, id := range ids {
		mark := marksByID[uuid.MustParse(id)]
		if err := insertMarkObservationTx(ctx, tx, mark); err != nil {
			return uuid.Nil, err
		}
	}
	var persistedID uuid.UUID
	var status string
	err = tx.QueryRow(ctx, `INSERT INTO account_projection_outbox (
		id, account_id, request_kind, through_transaction_id, as_of,
		mark_as_of, mark_generation, mark_source, mark_namespace, max_mark_age_microseconds,
		status, attempt_count, next_attempt_at, created_at, updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'pending',0,$5,$5,$5)
	ON CONFLICT (account_id,request_kind,through_transaction_id,mark_generation)
	DO UPDATE SET next_attempt_at=LEAST(account_projection_outbox.next_attempt_at,EXCLUDED.next_attempt_at), updated_at=EXCLUDED.updated_at
	WHERE account_projection_outbox.status IN ('pending','retry')
	RETURNING id,status`, outboxID, request.AccountID, ProjectionRequestMarkRebuild, request.ThroughTransactionID, request.AsOf, request.MarkAsOf, generation, source, namespace, request.MaxMarkAge.Microseconds()).Scan(&persistedID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		persistedID = outboxID
	} else if err != nil {
		return uuid.Nil, fmt.Errorf("postgres: enqueue mark rebuild: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("postgres: commit marks and rebuild request: %w", err)
	}
	return persistedID, nil
}

func insertMarkObservationTx(ctx context.Context, tx pgx.Tx, mark *ledger.MarkObservation) error {
	var persistedID uuid.UUID
	err := tx.QueryRow(ctx, `INSERT INTO mark_observations (
		id,unit_kind,unit,price,price_currency,source,source_observation_id,effective_at,observed_at,metadata,created_at,instrument_id,source_namespace,source_revision
	) VALUES ($1,'instrument',$2,$3,$4,$5,$6,$7,$8,$9::JSONB,$10,$11,$12,$13)
	ON CONFLICT DO NOTHING RETURNING id`, mark.ID, mark.InstrumentID.String(), mark.Price.String(), mark.PriceCurrency, mark.Source, mark.SourceObservationID, mark.EffectiveAt, mark.ObservedAt, jsonForStorage(mark.Metadata), mark.CreatedAt, mark.InstrumentID, mark.SourceNamespace, mark.SourceRevision).Scan(&persistedID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("postgres: insert mark in rebuild transaction: %w", err)
	}
	existing, loadErr := scanProjectionMark(tx.QueryRow(ctx, projectionMarkSelectSQL+` WHERE instrument_id=$1 AND price_currency=$2 AND source=$3 AND source_namespace=$4 AND source_observation_id=$5`, mark.InstrumentID, mark.PriceCurrency, mark.Source, mark.SourceNamespace, mark.SourceObservationID))
	if loadErr != nil {
		return fmt.Errorf("postgres: load replayed rebuild mark: %w", loadErr)
	}
	if !ledger.SameMarkObservation(existing, mark) {
		return fmt.Errorf("postgres: mark source identity reused with changed evidence: %w", repository.ErrIdempotencyConflict)
	}
	return nil
}

func (repo *ProjectionOutboxRepository) Claim(ctx context.Context, workerID string, now time.Time, lease time.Duration) (*ProjectionOutboxItem, error) {
	workerID = strings.TrimSpace(workerID)
	now = now.UTC().Truncate(time.Microsecond)
	lease = lease.Truncate(time.Microsecond)
	if repo == nil || repo.pool == nil || workerID == "" || len(workerID) > 256 || now.IsZero() || lease <= 0 {
		return nil, fmt.Errorf("postgres: claim projection request: valid worker, time, and lease are required")
	}
	tx, err := repo.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("postgres: begin projection claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, err := scanProjectionOutbox(tx.QueryRow(ctx, `WITH candidate AS (
		SELECT id FROM account_projection_outbox
		WHERE (status IN ('pending','retry') AND next_attempt_at <= $1)
		   OR (status='processing' AND claim_expires_at <= $1)
		ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1
	) UPDATE account_projection_outbox o SET status='processing',claimed_at=$1,claimed_by=$2,claim_expires_at=$3,updated_at=$1
	FROM candidate WHERE o.id=candidate.id
	RETURNING o.id,o.account_id,o.request_kind,o.through_transaction_id,o.as_of,o.mark_as_of,o.mark_generation,
		o.mark_source,o.mark_namespace,o.max_mark_age_microseconds,o.status,o.attempt_count,o.next_attempt_at,o.claimed_by,o.claim_expires_at`, now, workerID, now.Add(lease)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: claim projection request: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres: commit projection claim: %w", err)
	}
	return item, nil
}

func (repo *ProjectionOutboxRepository) Heartbeat(ctx context.Context, id uuid.UUID, workerID string, now time.Time, lease time.Duration) error {
	command, err := repo.pool.Exec(ctx, `UPDATE account_projection_outbox SET claim_expires_at=$4,updated_at=$3
		WHERE id=$1 AND status='processing' AND claimed_by=$2 AND claim_expires_at>$3`, id, workerID, now.UTC().Truncate(time.Microsecond), now.UTC().Truncate(time.Microsecond).Add(lease.Truncate(time.Microsecond)))
	if err != nil {
		return fmt.Errorf("postgres: heartbeat projection claim: %w", err)
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("postgres: projection claim lease lost")
	}
	return nil
}

func (repo *ProjectionOutboxRepository) Complete(ctx context.Context, id uuid.UUID, workerID string, now time.Time) error {
	return repo.finish(ctx, id, workerID, now, "completed", "", time.Time{})
}

func (repo *ProjectionOutboxRepository) Release(ctx context.Context, id uuid.UUID, workerID string, now time.Time, errorCode string) error {
	now = now.UTC().Truncate(time.Microsecond)
	command, err := repo.pool.Exec(ctx, `UPDATE account_projection_outbox
		SET status='retry',next_attempt_at=$3,last_error_code=$4,completed_at=NULL,
			claimed_at=NULL,claimed_by=NULL,claim_expires_at=NULL,updated_at=$3
		WHERE id=$1 AND status='processing' AND claimed_by=$2`,
		id, strings.TrimSpace(workerID), now, sanitizeProjectionErrorCode(errorCode))
	if err != nil {
		return fmt.Errorf("postgres: release projection claim: %w", err)
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("postgres: projection claim lease lost")
	}
	return nil
}

func (repo *ProjectionOutboxRepository) RetryOrDegrade(ctx context.Context, id uuid.UUID, workerID string, now time.Time, retryLimit int, errorCode string) (string, error) {
	if retryLimit <= 0 {
		return "", fmt.Errorf("postgres: retry limit must be positive")
	}
	var attempts int
	if err := repo.pool.QueryRow(ctx, `SELECT attempt_count FROM account_projection_outbox WHERE id=$1 AND status='processing' AND claimed_by=$2 AND claim_expires_at>$3`, id, workerID, now.UTC().Truncate(time.Microsecond)).Scan(&attempts); err != nil {
		return "", fmt.Errorf("postgres: load projection attempt: %w", err)
	}
	status := "retry"
	next := now.UTC().Truncate(time.Microsecond).Add(projectionRetryDelay(attempts + 1))
	if attempts+1 >= retryLimit {
		status, next = "degraded", time.Time{}
	}
	if err := repo.finish(ctx, id, workerID, now, status, sanitizeProjectionErrorCode(errorCode), next); err != nil {
		return "", err
	}
	return status, nil
}

func (repo *ProjectionOutboxRepository) finish(ctx context.Context, id uuid.UUID, workerID string, now time.Time, status, errorCode string, next time.Time) error {
	now = now.UTC().Truncate(time.Microsecond)
	var command pgconnCommandTag
	var err error
	if status == "completed" {
		command, err = repo.pool.Exec(ctx, `UPDATE account_projection_outbox SET status='completed',completed_at=$3,last_error_code=NULL,claimed_at=NULL,claimed_by=NULL,claim_expires_at=NULL,updated_at=$3 WHERE id=$1 AND status='processing' AND claimed_by=$2 AND claim_expires_at>$3`, id, workerID, now)
	} else {
		command, err = repo.pool.Exec(ctx, `UPDATE account_projection_outbox SET status=$4,attempt_count=attempt_count+1,next_attempt_at=CASE WHEN $4='retry' THEN $6 ELSE next_attempt_at END,last_error_code=$5,completed_at=NULL,claimed_at=NULL,claimed_by=NULL,claim_expires_at=NULL,updated_at=$3 WHERE id=$1 AND status='processing' AND claimed_by=$2 AND claim_expires_at>$3`, id, workerID, now, status, errorCode, next)
	}
	if err != nil {
		return fmt.Errorf("postgres: finish projection request: %w", err)
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("postgres: projection claim lease lost")
	}
	return nil
}

type pgconnCommandTag interface{ RowsAffected() int64 }

func projectionRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return time.Second * time.Duration(1<<(attempt-1))
}

func sanitizeProjectionErrorCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' || character == '-' || character == '.' {
			builder.WriteRune(character)
		}
		if builder.Len() == 128 {
			break
		}
	}
	if builder.Len() == 0 {
		return "projection_rebuild_failed"
	}
	return builder.String()
}

func scanProjectionOutbox(row accountRow) (*ProjectionOutboxItem, error) {
	var item ProjectionOutboxItem
	var markAsOf, claimExpiresAt *time.Time
	var markSource, markNamespace *string
	var maxMarkAgeMicroseconds *int64
	if err := row.Scan(&item.ID, &item.AccountID, &item.RequestKind, &item.ThroughTransactionID, &item.AsOf, &markAsOf, &item.MarkGeneration, &markSource, &markNamespace, &maxMarkAgeMicroseconds, &item.Status, &item.AttemptCount, &item.NextAttemptAt, &item.ClaimedBy, &claimExpiresAt); err != nil {
		return nil, err
	}
	item.MarkAsOf, item.ClaimExpiresAt = markAsOf, claimExpiresAt
	if markSource != nil {
		item.MarkSource = *markSource
	}
	if markNamespace != nil {
		item.MarkNamespace = *markNamespace
	}
	if maxMarkAgeMicroseconds != nil {
		item.MaxMarkAge = time.Duration(*maxMarkAgeMicroseconds) * time.Microsecond
	}
	return &item, nil
}
