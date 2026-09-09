package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
)

func TestInternalAccountCapitalSnapshotPersistsReplaysAndRejectsForgery(t *testing.T) {
	ctx := context.Background()
	pools := newProjectionIntegrationPool(t, ctx)
	applyRepositoryMigrationRange(t, ctx, pools.owner, "000108", "000112")
	down, err := os.ReadFile("../../../migrations/000112_internal_portfolio_capital.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pools.owner.Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../../../migrations/000112_internal_portfolio_capital.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pools.owner.Exec(ctx, string(up)); err != nil {
		t.Fatal(err)
	}
	accountID := uuid.MustParse("00000000-0000-4000-8000-000000000064")
	source, err := NewCanonicalExperimentCapitalStateSource(pools.owner, pools.attestor, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.CaptureInternalPortfolioCapital(ctx, accountID); err == nil {
		t.Fatal("missing projection accepted")
	}
	asOf := time.Now().UTC().Truncate(time.Microsecond)
	frontier, err := NewProjectionOutboxRepository(pools.owner).LatestProjectionFrontier(ctx, accountID, asOf)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := NewProjectionRepo(pools.writer, pools.attestor).RebuildPortfolioProjection(ctx, ledger.ProjectionRequest{
		AccountID: accountID, ThroughTransactionID: frontier, AsOf: asOf, MarkSource: "kalshi", MarkNamespace: "internal-snapshot-test", MaxMarkAge: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.CaptureInternalPortfolioCapital(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ProjectionCheckpointID != projection.CheckpointID || !snapshot.Equity.Equal(decimal.NewFromInt(100000)) || !snapshot.LongBuyingPower.Equal(decimal.NewFromInt(200000)) {
		t.Fatalf("snapshot does not derive from canonical account: %+v", snapshot)
	}
	replay, err := source.CaptureInternalPortfolioCapital(ctx, accountID)
	if err != nil || replay.ID != snapshot.ID {
		t.Fatalf("replay differs: %+v %v", replay, err)
	}
	for _, test := range []struct{ name, sql, want string }{
		{"mutation", `UPDATE internal_portfolio_capital_snapshots SET equity=equity+1 WHERE id=$1`, "append-only"},
		{"deletion", `DELETE FROM internal_portfolio_capital_snapshots WHERE id=$1`, "append-only"},
		{"forged balance", `INSERT INTO internal_portfolio_capital_snapshots SELECT id,account_id,capital_binding_id,projection_checkpoint_id,through_transaction_id,observed_at,equity+1,long_buying_power,sha256,canonical_bytes,canonical_json FROM internal_portfolio_capital_snapshots WHERE id=$1`, "balances do not reconstruct"},
		{"wrong frontier", `INSERT INTO internal_portfolio_capital_snapshots SELECT id,account_id,capital_binding_id,projection_checkpoint_id,'00000000-0000-4000-8000-000000000001'::uuid,observed_at,equity,long_buying_power,sha256,canonical_bytes,canonical_json FROM internal_portfolio_capital_snapshots WHERE id=$1`, "checkpoint does not reconstruct"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := pools.owner.Exec(ctx, test.sql, snapshot.ID); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %s, got %v", test.want, err)
			}
		})
	}
	if _, err := pools.owner.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "immutable internal capital") {
		t.Fatalf("rollback did not protect evidence: %v", err)
	}
	var count int
	if err := pools.owner.QueryRow(ctx, `SELECT count(*) FROM internal_portfolio_capital_snapshots`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	applyRepositoryMigrationRange(t, ctx, pools.owner, "000112", "000113")
	downBinding, err := os.ReadFile("../../../migrations/000113_internal_portfolio_snapshot_binding.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pools.owner.Exec(ctx, string(downBinding)); err != nil {
		t.Fatal(err)
	}
	upBinding, err := os.ReadFile("../../../migrations/000113_internal_portfolio_snapshot_binding.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pools.owner.Exec(ctx, string(upBinding)); err != nil {
		t.Fatal(err)
	}
	broker := &countingPortfolioBalanceSource{}
	riskRepo := NewPortfolioRiskRepo(pools.owner, accountID, broker).WithInternalCapitalSource(source)
	accountSnapshot, err := riskRepo.CaptureAccountSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if broker.calls != 0 || accountSnapshot.Equity != 100000 || accountSnapshot.BuyingPower != 200000 || accountSnapshot.OptionsBuyingPower != 0 {
		t.Fatalf("invalid internal account snapshot: %+v broker calls=%d", accountSnapshot, broker.calls)
	}
	accountReplay, err := riskRepo.CaptureAccountSnapshot(ctx)
	if err != nil || accountReplay.ID != accountSnapshot.ID {
		t.Fatalf("account replay differs: %+v %v", accountReplay, err)
	}
	riskState, err := riskRepo.LoadPortfolioRiskState(ctx, accountSnapshot.ID, accountSnapshot.ObservedAt)
	if err != nil {
		t.Fatal(err)
	}
	if riskState.ReconciliationID != "internal-ledger-capital/v1/"+snapshot.ID.String() || riskState.DailyLossPct != 0 || riskState.DrawdownPct != 0 {
		t.Fatalf("risk state does not reference internal evidence: %+v", riskState)
	}
	if _, err := riskRepo.LoadPortfolioRiskState(ctx, accountSnapshot.ID, accountSnapshot.ObservedAt.Add(2*time.Minute)); err == nil {
		t.Fatal("stale internal evidence accepted")
	}
	if _, err := NewPortfolioRiskRepo(pools.owner, accountID, broker).LoadPortfolioRiskState(ctx, accountSnapshot.ID, accountSnapshot.ObservedAt); err == nil {
		t.Fatal("internal evidence accepted without attestor")
	}
	wrongAttestor := pools.attestor
	wrongAttestor.Secret = make([]byte, 32)
	wrongSource, err := NewCanonicalExperimentCapitalStateSource(pools.owner, wrongAttestor, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewPortfolioRiskRepo(pools.owner, accountID).WithInternalCapitalSource(wrongSource).LoadPortfolioRiskState(ctx, accountSnapshot.ID, accountSnapshot.ObservedAt); err == nil {
		t.Fatal("internal evidence accepted with wrong HMAC key")
	}
	_, err = pools.owner.Exec(ctx, `WITH forged AS (
		SELECT source.*,jsonb_set(canonical_json,'{equity}',to_jsonb(to_char(equity+1,'FM99999999999999999990.00000000'))) AS payload
		FROM portfolio_account_snapshots source WHERE id=$1
	), encoded AS (SELECT forged.*,convert_to(payload::text,'UTF8') AS raw FROM forged),
	identified AS (SELECT encoded.*,encode(digest(raw,'sha256'),'hex') AS digest FROM encoded)
	INSERT INTO portfolio_account_snapshots(id,account_id,environment,external_account_id,observed_at,equity,buying_power,options_buying_power,fallback_used,sha256,canonical_bytes,canonical_json,created_at,internal_capital_snapshot_id)
	SELECT economic_deterministic_uuid('portfolio-account-snapshot','portfolio-account-snapshot-internal-v1@sha256:'||digest),account_id,environment,NULL,observed_at,equity+1,buying_power,0,false,digest,raw,payload,created_at,internal_capital_snapshot_id FROM identified`, accountSnapshot.ID)
	if err == nil || !strings.Contains(err.Error(), "does not match capital evidence") {
		t.Fatalf("forged account balance did not fail binding: %v", err)
	}
	if _, err := pools.owner.Exec(ctx, string(downBinding)); err == nil || !strings.Contains(err.Error(), "immutable internal portfolio") {
		t.Fatalf("binding rollback did not protect evidence: %v", err)
	}
}
