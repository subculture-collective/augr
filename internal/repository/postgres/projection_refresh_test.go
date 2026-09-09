package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	"github.com/google/uuid"
)

func TestCashOnlyProjectionBootstrapRefreshAndReplay(t *testing.T) {
	ctx := context.Background()
	pools := newProjectionIntegrationPool(t, ctx)
	accountID := uuid.MustParse("00000000-0000-4000-8000-000000000064")
	account, err := NewAccountRepo(pools.owner).GetByID(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	asOf := time.Now().UTC().Add(time.Minute).Truncate(time.Minute)
	frontier, err := NewProjectionOutboxRepository(pools.owner).LatestProjectionFrontier(ctx, accountID, asOf)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewProjectionRepo(pools.writer, pools.attestor)
	request := ledger.ProjectionRequest{AccountID: accountID, ThroughTransactionID: frontier, AsOf: asOf, MarkSource: "kalshi", MarkNamespace: "cash-refresh-test", MaxMarkAge: time.Minute}
	first, err := repo.RebuildPortfolioProjection(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Marks) != 0 || len(first.Positions) != 0 || !first.Totals.Cash.Equal(account.StartingCapital) {
		t.Fatalf("cash-only projection=%+v", first)
	}
	replay, err := repo.RebuildPortfolioProjection(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if replay.CheckpointID != first.CheckpointID {
		t.Fatal("same occurrence failed to converge")
	}
	request.AsOf = asOf.Add(time.Minute)
	second, err := repo.RebuildPortfolioProjection(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if second.CheckpointID == first.CheckpointID || !second.Totals.Cash.Equal(first.Totals.Cash) {
		t.Fatal("refresh must advance identity without changing cash")
	}
	var transactions, marks, reconciliations int
	if err := pools.owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM ledger_transactions WHERE account_id=$1),(SELECT count(*) FROM mark_observations),(SELECT count(*) FROM venue_reconciliation_runs)`, accountID).Scan(&transactions, &marks, &reconciliations); err != nil {
		t.Fatal(err)
	}
	if transactions != 1 || marks != 0 || reconciliations != 0 {
		t.Fatalf("refresh fabricated evidence: transactions=%d marks=%d reconciliations=%d", transactions, marks, reconciliations)
	}
}
