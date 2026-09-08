package postgres

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type projectionOutboxQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func TestProjectionOutboxMarkBatchConvergesAndKeepsLaterGeneration(t *testing.T) {
	ctx := context.Background()
	pools := newProjectionIntegrationPool(t, ctx)
	applyRepositoryMigrationRange(t, ctx, pools.owner, "000069", "000108")
	fixture := newEconomicLedgerFixture(t, ctx, pools.owner, "projection-outbox-generations")
	ledgerRepo := NewLedgerRepo(pools.owner)
	if _, err := ledgerRepo.RecordEconomicSourceEvent(ctx, fixture.source); err != nil {
		t.Fatal(err)
	}
	if _, err := ledgerRepo.ApplyEconomicNormalization(ctx, fixture.normalization); err != nil {
		t.Fatal(err)
	}
	repo := NewProjectionOutboxRepository(pools.owner)
	markAt := fixture.normalization.Transaction.EffectiveAt.Add(time.Second).UTC().Truncate(time.Microsecond)
	first := projectionOutboxMark(t, fixture.instrument.ID, "generation-1", markAt)
	request := repository.ProjectionMarkBatch{
		AccountID: fixture.account.ID, ThroughTransactionID: fixture.normalization.Transaction.ID,
		AsOf: markAt.Add(time.Minute), MarkAsOf: markAt, MaxMarkAge: time.Hour, Marks: []*ledger.MarkObservation{first},
	}

	const writers = 2
	ids := make(chan uuid.UUID, writers)
	errs := make(chan error, writers)
	var wait sync.WaitGroup
	for range writers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			id, err := repo.RecordMarksAndEnqueueRebuild(ctx, request)
			if err != nil {
				errs <- err
				return
			}
			ids <- id
		}()
	}
	wait.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Errorf("identical mark batch: %v", err)
	}
	var firstID uuid.UUID
	for id := range ids {
		if firstID == uuid.Nil {
			firstID = id
		} else if id != firstID {
			t.Fatalf("identical batch IDs differ: %s != %s", id, firstID)
		}
	}
	assertProjectionOutboxCounts(t, ctx, pools.owner, fixture.account.ID, 1, 1)

	secondAt := markAt.Add(time.Second)
	second := projectionOutboxMark(t, fixture.instrument.ID, "generation-2", secondAt)
	request.MarkAsOf, request.Marks = secondAt, []*ledger.MarkObservation{second}
	secondID, err := repo.RecordMarksAndEnqueueRebuild(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if secondID == firstID {
		t.Fatal("later mark generation reused the first outbox identity")
	}
	assertProjectionOutboxCounts(t, ctx, pools.owner, fixture.account.ID, 2, 2)
}

func TestProjectionOutboxMarkBatchRollsBackMarksWhenEnqueueFails(t *testing.T) {
	ctx := context.Background()
	pools := newProjectionIntegrationPool(t, ctx)
	applyRepositoryMigrationRange(t, ctx, pools.owner, "000069", "000108")
	fixture := newEconomicLedgerFixture(t, ctx, pools.owner, "projection-outbox-rollback")
	ledgerRepo := NewLedgerRepo(pools.owner)
	if _, err := ledgerRepo.RecordEconomicSourceEvent(ctx, fixture.source); err != nil {
		t.Fatal(err)
	}
	if _, err := ledgerRepo.ApplyEconomicNormalization(ctx, fixture.normalization); err != nil {
		t.Fatal(err)
	}
	markAt := fixture.normalization.Transaction.EffectiveAt.Add(time.Second).UTC().Truncate(time.Microsecond)
	mark := projectionOutboxMark(t, fixture.instrument.ID, "rollback", markAt)
	_, err := NewProjectionOutboxRepository(pools.owner).RecordMarksAndEnqueueRebuild(ctx, repository.ProjectionMarkBatch{
		AccountID: uuid.New(), ThroughTransactionID: fixture.normalization.Transaction.ID,
		AsOf: markAt.Add(time.Minute), MarkAsOf: markAt, MaxMarkAge: time.Hour, Marks: []*ledger.MarkObservation{mark},
	})
	if err == nil {
		t.Fatal("mismatched account unexpectedly enqueued a rebuild")
	}
	var markCount int
	if err := pools.owner.QueryRow(ctx, `SELECT count(*) FROM mark_observations WHERE id=$1`, mark.ID).Scan(&markCount); err != nil {
		t.Fatal(err)
	}
	if markCount != 0 {
		t.Fatalf("orphan mark count = %d, want 0", markCount)
	}
}

func TestProjectionOutboxExpiredClaimIsRecoveredAndOldWorkerLosesLease(t *testing.T) {
	ctx := context.Background()
	pools := newProjectionIntegrationPool(t, ctx)
	applyRepositoryMigrationRange(t, ctx, pools.owner, "000069", "000108")
	fixture := newEconomicLedgerFixture(t, ctx, pools.owner, "projection-outbox-lease")
	ledgerRepo := NewLedgerRepo(pools.owner)
	if _, err := ledgerRepo.RecordEconomicSourceEvent(ctx, fixture.source); err != nil {
		t.Fatal(err)
	}
	if _, err := ledgerRepo.ApplyEconomicNormalization(ctx, fixture.normalization); err != nil {
		t.Fatal(err)
	}
	repo := NewProjectionOutboxRepository(pools.owner)
	now := fixture.normalization.Transaction.ObservedAt.Add(time.Minute).UTC().Truncate(time.Microsecond)
	tx, err := pools.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enqueueEconomicProjectionTx(ctx, tx, fixture.account.ID, fixture.normalization.Transaction.ID, now); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := repo.Claim(ctx, "worker-one", now, time.Second)
	if err != nil || first == nil {
		t.Fatalf("first Claim() = %+v, %v", first, err)
	}
	if second, err := repo.Claim(ctx, "worker-two", now.Add(500*time.Millisecond), time.Second); err != nil || second != nil {
		t.Fatalf("unexpired Claim() = %+v, %v", second, err)
	}
	recovered, err := repo.Claim(ctx, "worker-two", now.Add(2*time.Second), time.Second)
	if err != nil || recovered == nil || recovered.ID != first.ID {
		t.Fatalf("expired Claim() = %+v, %v", recovered, err)
	}
	if err := repo.Complete(ctx, first.ID, "worker-one", now.Add(2500*time.Millisecond)); err == nil {
		t.Fatal("old worker completed a claim after losing its lease")
	}
	if err := repo.Complete(ctx, recovered.ID, "worker-two", now.Add(2500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var status string
	var claimedBy *string
	if err := pools.owner.QueryRow(ctx, `SELECT status,claimed_by FROM account_projection_outbox WHERE id=$1`, recovered.ID).Scan(&status, &claimedBy); err != nil {
		t.Fatal(err)
	}
	if status != "completed" || claimedBy != nil {
		t.Fatalf("completed claim = status:%s claimed_by:%v", status, claimedBy)
	}
}

func projectionOutboxMark(t *testing.T, instrumentID uuid.UUID, sourceID string, at time.Time) *ledger.MarkObservation {
	t.Helper()
	mark, err := ledger.NewMarkObservation(ledger.MarkObservationInput{
		InstrumentID: instrumentID, Price: decimal.RequireFromString("12.25"), PriceCurrency: "USD",
		Source: "test-source", SourceNamespace: "marks/outbox", SourceObservationID: sourceID,
		SourceRevision: "v1", EffectiveAt: at, ObservedAt: at, Metadata: json.RawMessage(`{"test":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return mark
}

func assertProjectionOutboxCounts(t *testing.T, ctx context.Context, pool projectionOutboxQuerier, accountID uuid.UUID, marks, outbox int) {
	t.Helper()
	var markCount, outboxCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM mark_observations WHERE source_namespace='marks/outbox'`).Scan(&markCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM account_projection_outbox WHERE account_id=$1 AND request_kind='mark_rebuild'`, accountID).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if markCount != marks || outboxCount != outbox {
		t.Fatalf("marks/outbox = %d/%d, want %d/%d", markCount, outboxCount, marks, outbox)
	}
}
