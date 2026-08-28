package migrations_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCanonicalAccountEnforcementContract(t *testing.T) {
	up := normalizeSQL(t, readMigrationFile(t, "000109_enforce_canonical_account.up.sql"))
	down := normalizeSQL(t, readMigrationFile(t, "000109_enforce_canonical_account.down.sql"))
	for _, fragment := range []string{
		"in access exclusive mode",
		"create function enforce_canonical_account_row",
		"canonical operational row requires account_id",
		"canonical operational account_id is immutable",
		"canonical operational environment does not match account",
		"where run.id=(row_data->>'pipeline_run_id')::uuid and run.trade_date=(row_data->>'pipeline_run_trade_date')::date and run.account_id=scoped_account_id",
		"copy order does not match its account-owned origin run",
		"copy execution intent does not match its account-owned origin run",
		"execution order does not match parent intent account and copy origin",
		"grant select,insert,update on account_projection_outbox to augr_app_runtime",
		"grant insert on mark_observations to augr_app_runtime",
		"revoke all privileges on account_projection_outbox from augr_projection_writer",
		"revoke insert on mark_observations from augr_projection_writer",
	} {
		if !strings.Contains(up, fragment) {
			t.Errorf("up migration missing %q", fragment)
		}
	}
	if strings.Contains(up, "order by trade_date desc limit 1") {
		t.Fatal("enforcement migration must use the exact composite pipeline key")
	}
	wantPrefix := normalizeSQL(t, "LOCK TABLE pipeline_runs, pipeline_run_snapshots, agent_decisions, agent_events, trade_decisions, orders, positions, trades, portfolio_opportunities, allocation_decisions, replay_events, financial_fill_idempotency, prediction_settlement_idempotency, execution_intents, execution_orders, copy_subscriptions, copy_trade_intents, copy_origin_rebalance_runs, copy_origin_rebalance_intents, copy_target_drift_runs, copy_target_drift_legs, conversations, conversation_messages, agent_memories, account_projection_outbox IN ACCESS EXCLUSIVE MODE;")
	if !strings.HasPrefix(down, wantPrefix) {
		t.Fatal("down migration does not begin with the required complete lock")
	}
	if !strings.Contains(down, "cannot roll back migration 109 while canonical scoped rows exist") ||
		!strings.Contains(down, "drop function enforce_canonical_account_row") {
		t.Fatal("down migration does not preserve schema 108 with a guarded rollback")
	}
}

func TestCanonicalAccountEnforcement(t *testing.T) {
	ctx, pool := newCanonicalExpansionPool(t)
	strategyID := insertCanonicalExpansionStrategy(t, ctx, pool)
	applyCanonicalExpansion(t, ctx, pool)
	if _, err := pool.Exec(ctx, readMigrationFile(t, "000109_enforce_canonical_account.up.sql")); err != nil {
		t.Fatalf("apply migration 109: %v", err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO pipeline_runs(strategy_id,ticker,trade_date,started_at) VALUES($1,'SPY',current_date,now())`, strategyID); err == nil || !strings.Contains(err.Error(), "requires account_id") {
		t.Fatalf("unscoped insert error = %v", err)
	}
	accountID := uuid.MustParse("00000000-0000-4000-8000-000000000064")
	originID := uuid.NewString()
	var runID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO pipeline_runs(strategy_id,ticker,trade_date,started_at,account_id,environment,origin_type,origin_id)
		VALUES($1,'SPY',current_date,now(),$2,'paper_scored','strategy_version',$3) RETURNING id`, strategyID, accountID, originID).Scan(&runID); err != nil {
		t.Fatalf("insert scoped run: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pipeline_run_snapshots(pipeline_run_id,data_type,payload,account_id,environment,origin_type,origin_id,pipeline_run_trade_date)
		VALUES($1,'market','{}',$2,'paper_scored','strategy_version',$3,current_date-1)`, runID, accountID, originID); err == nil || !strings.Contains(err.Error(), "pipeline run identity") {
		t.Fatalf("wrong composite pipeline parent error = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE pipeline_runs SET account_id=$2 WHERE id=$1 AND trade_date=current_date`, runID, uuid.New()); err == nil || !strings.Contains(err.Error(), "account_id is immutable") {
		t.Fatalf("account mutation error = %v", err)
	}
	if _, err := pool.Exec(ctx, readMigrationFile(t, "000109_enforce_canonical_account.down.sql")); err == nil || !strings.Contains(err.Error(), "canonical scoped rows exist") {
		t.Fatalf("nonempty migration-109 rollback error = %v", err)
	}
}
