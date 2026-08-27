package migrations_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCanonicalAccountExpansionContract(t *testing.T) {
	up := normalizeSQL(t, readMigrationFile(t, "000108_canonical_account_expansion.up.sql"))
	down := normalizeSQL(t, readMigrationFile(t, "000108_canonical_account_expansion.down.sql"))
	for _, fragment := range []string{
		"create or replace function strategy_legacy_snapshot_sha",
		"'active_thesis', s.active_thesis",
		"add column execution_strategy_version_id uuid references strategy_versions(id) on delete restrict",
		"create table account_projection_outbox",
		"mark_generation uuid not null",
		"create unique index uq_account_projection_outbox_request",
		"create index idx_account_projection_outbox_claimable",
		"create function validate_account_projection_outbox_row",
		"create trigger trg_validate_account_projection_outbox_row",
		"where account_id is not null",
		"capital_margin_policy_v1_canonical_bytes(reviewed_json)",
		"economic_deterministic_uuid('capital-policy-binding'",
		"insert into capital_margin_policy_artifacts",
		"insert into account_capital_policy_bindings",
		"create or replace function validate_canonical_projection_checkpoint",
		"frontier_effective_at,frontier_observed_at,new.through_transaction_id",
	} {
		if !strings.Contains(up, fragment) {
			t.Errorf("up migration missing %q", fragment)
		}
	}
	for _, forbidden := range []string{"to_jsonb(s)::text", "on conflict", "alter column account_id set not null", "update pipeline_runs", "update orders"} {
		if strings.Contains(up, forbidden) {
			t.Errorf("up migration contains forbidden compatibility change %q", forbidden)
		}
	}
	for _, fragment := range []string{
		"in access exclusive mode",
		"cannot roll back migration 108 while projection outbox rows exist",
		"another binding references the seeded artifact",
		"disable trigger trg_account_capital_policy_bindings_immutable",
		"enable trigger trg_account_capital_policy_bindings_immutable",
		"drop trigger trg_validate_account_projection_outbox_row",
		"drop function validate_account_projection_outbox_row",
		"expected_through_transaction_id uuid",
		"order by effective_at desc, observed_at desc, id desc",
	} {
		if !strings.Contains(down, fragment) {
			t.Errorf("down migration missing %q", fragment)
		}
	}
}

func TestCanonicalAccountExpansionCyclesAndLocksRollback(t *testing.T) {
	ctx, pool := newCanonicalExpansionPool(t)
	strategyID := insertCanonicalExpansionStrategy(t, ctx, pool)
	var hash107 string
	if err := pool.QueryRow(ctx, `SELECT strategy_legacy_snapshot_sha($1)`, strategyID).Scan(&hash107); err != nil {
		t.Fatal(err)
	}
	applyCanonicalExpansion(t, ctx, pool)
	var hash108 string
	if err := pool.QueryRow(ctx, `SELECT strategy_legacy_snapshot_sha($1)`, strategyID).Scan(&hash108); err != nil {
		t.Fatal(err)
	}
	if hash108 != hash107 {
		t.Fatalf("legacy strategy hash changed across 108: 107=%s 108=%s", hash107, hash108)
	}
	assertCanonicalExpansionSeed(t, ctx, pool)

	t.Run("modified seed blocks and leaves triggers enabled", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		if _, err = tx.Exec(ctx, `ALTER TABLE account_capital_policy_bindings DISABLE TRIGGER trg_account_capital_policy_bindings_immutable;
			UPDATE account_capital_policy_bindings SET margin_profile='portfolio';
			ALTER TABLE account_capital_policy_bindings ENABLE TRIGGER trg_account_capital_policy_bindings_immutable`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, readMigrationFile(t, "000108_canonical_account_expansion.down.sql")); err == nil || !strings.Contains(err.Error(), "missing or modified") {
			t.Fatalf("modified seed rollback error=%v", err)
		}
		_ = tx.Rollback(ctx)
		assertTriggerEnabled(t, ctx, pool, "account_capital_policy_bindings", "trg_account_capital_policy_bindings_immutable")
		assertTriggerEnabled(t, ctx, pool, "capital_margin_policy_artifacts", "trg_capital_margin_policy_artifacts_immutable")
	})

	t.Run("missing seed blocks", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		if _, err = tx.Exec(ctx, `ALTER TABLE account_capital_policy_bindings DISABLE TRIGGER trg_account_capital_policy_bindings_immutable; DELETE FROM account_capital_policy_bindings`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, readMigrationFile(t, "000108_canonical_account_expansion.down.sql")); err == nil || !strings.Contains(err.Error(), "missing or modified") {
			t.Fatalf("missing seed rollback error=%v", err)
		}
	})

	t.Run("second binding blocks", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		accountID := uuid.New()
		if _, err = tx.Exec(ctx, `INSERT INTO accounts(id,name,environment,venue,base_currency,storage_namespace,evidence_class,starting_capital,buying_power_multiplier,margin_profile,status,created_by)
			VALUES($1,'rollback blocker','paper_scored','internal','USD',$2,'promotion_evidence',100000,2,'reg_t','active','migration-108-test')`, accountID, "paper_scored/"+accountID.String()); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO account_capital_policy_bindings(id,account_id,policy_artifact_id,policy_version,tier,margin_profile,environment,starting_capital,buying_power_multiplier,evidence_class,storage_namespace,currency)
			SELECT economic_deterministic_uuid('capital-policy-binding',$1::TEXT,policy_version),$1,id,policy_version,100000,'reg_t','paper_scored',100000,2,'promotion_evidence',$2,'USD' FROM capital_margin_policy_artifacts`, accountID, "paper_scored/"+accountID.String()); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, readMigrationFile(t, "000108_canonical_account_expansion.down.sql")); err == nil || !strings.Contains(err.Error(), "another binding") {
			t.Fatalf("second binding rollback error=%v", err)
		}
	})

	t.Run("outbox row blocks", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		if _, err = tx.Exec(ctx, `INSERT INTO account_projection_outbox(id,account_id,request_kind,through_transaction_id,as_of,mark_generation,status,next_attempt_at,created_at,updated_at)
			SELECT gen_random_uuid(),account_id,'economic_fill',id,
			GREATEST(effective_at,observed_at),'00000000-0000-0000-0000-000000000000','pending',GREATEST(effective_at,observed_at),GREATEST(effective_at,observed_at),GREATEST(effective_at,observed_at)
			FROM ledger_transactions WHERE account_id='00000000-0000-4000-8000-000000000064' ORDER BY effective_at,observed_at,id LIMIT 1`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, readMigrationFile(t, "000108_canonical_account_expansion.down.sql")); err == nil || !strings.Contains(err.Error(), "outbox rows exist") {
			t.Fatalf("outbox rollback error=%v", err)
		}
	})

	if _, err := pool.Exec(ctx, readMigrationFile(t, "000108_canonical_account_expansion.down.sql")); err != nil {
		t.Fatalf("108 to 107: %v", err)
	}
	var outbox, validator, claimIndex any
	if err := pool.QueryRow(ctx, `SELECT to_regclass('account_projection_outbox'),to_regprocedure('validate_account_projection_outbox_row()'),to_regclass('idx_account_projection_outbox_claimable')`).Scan(&outbox, &validator, &claimIndex); err != nil {
		t.Fatal(err)
	}
	if outbox != nil || validator != nil || claimIndex != nil {
		t.Fatalf("migration-108 objects remain at 107: %v %v %v", outbox, validator, claimIndex)
	}
	applyCanonicalExpansion(t, ctx, pool)
	assertCanonicalExpansionSeed(t, ctx, pool)
}

func TestCanonicalAccountExpansionOldWriterCanary(t *testing.T) {
	ctx, pool := newCanonicalExpansionPool(t)
	strategyID := insertCanonicalExpansionStrategy(t, ctx, pool)
	var runID, subscriptionID, intentID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO pipeline_runs(strategy_id,ticker,trade_date,started_at) VALUES($1,'SPY','2026-08-27',now()) RETURNING id`, strategyID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	var leaderID, sourceID, observationID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO copy_leaders(entity_type,display_name) VALUES('individual','legacy leader') RETURNING id`).Scan(&leaderID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO copy_leader_sources(leader_id,provider,source_type,external_key) VALUES($1,'test','sec_13f',$2) RETURNING id`, leaderID, uuid.NewString()).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO copy_source_observations(source_id,provider_observation_id,observation_kind,effective_at,published_at,content_hash) VALUES($1,$2,'portfolio_snapshot',now(),now(),'hash') RETURNING id`, sourceID, uuid.NewString()).Scan(&observationID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO copy_subscriptions(leader_id,source_id,legacy_strategy_id,capital_budget) VALUES($1,$2,$3,1000) RETURNING id`, leaderID, sourceID, strategyID).Scan(&subscriptionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO copy_trade_intents(subscription_id,source_observation_id,pipeline_run_id,instrument_key,ticker,side,policy_status) VALUES($1,$2,$3,'SPY','SPY','buy','approved') RETURNING id`, subscriptionID, observationID, runID).Scan(&intentID); err != nil {
		t.Fatal(err)
	}
	applyCanonicalExpansion(t, ctx, pool)
	var subscriptionOrigin, intentOrigin uuid.UUID
	var nullExpansion bool
	if err := pool.QueryRow(ctx, `SELECT subscription.origin_id,intent.origin_id,
		(subscription.account_id IS NULL AND subscription.environment IS NULL AND intent.account_id IS NULL AND intent.environment IS NULL AND intent.pipeline_run_trade_date IS NULL AND run.account_id IS NULL AND run.environment IS NULL AND run.origin_type IS NULL AND run.origin_id IS NULL)
		FROM copy_subscriptions subscription JOIN copy_trade_intents intent ON intent.subscription_id=subscription.id JOIN pipeline_runs run ON run.id=$3
		WHERE subscription.id=$1 AND intent.id=$2`, subscriptionID, intentID, runID).Scan(&subscriptionOrigin, &intentOrigin, &nullExpansion); err != nil {
		t.Fatal(err)
	}
	if subscriptionOrigin != subscriptionID || intentOrigin != subscriptionID || !nullExpansion {
		t.Fatalf("old writer changed: subscription origin=%s intent origin=%s nulls=%t", subscriptionOrigin, intentOrigin, nullExpansion)
	}
	assertTriggerEnabled(t, ctx, pool, "copy_origin_rebalance_runs", "copy_origin_runs_append_only")
	assertTriggerEnabled(t, ctx, pool, "copy_origin_rebalance_intents", "copy_origin_run_intents_append_only")
}

func newCanonicalExpansionPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping canonical-account expansion integration test in short mode")
	}
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("DB_URL")
	}
	if databaseURL == "" {
		databaseURL = os.Getenv("DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("skipping canonical-account expansion integration test: TEST_DATABASE_URL, DB_URL, and DATABASE_URL are unset")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if config.ConnConfig.Database == "tradingagent" {
		t.Fatal("refusing canonical-account expansion test against protected database tradingagent")
	}
	ctx := context.Background()
	admin, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := "migr_account_expansion_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, `CREATE SCHEMA `+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, `DROP SCHEMA IF EXISTS `+identifier+` CASCADE`) })
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, filename := range sortedUpMigrationsThrough(t, "000107_robustness_assessment_scope.up.sql") {
		if _, err = pool.Exec(ctx, readMigrationFile(t, filename)); err != nil {
			t.Fatalf("apply %s: %v", filename, err)
		}
	}
	return ctx, pool
}

func applyCanonicalExpansion(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, readMigrationFile(t, "000108_canonical_account_expansion.up.sql")); err != nil {
		t.Fatalf("apply migration 108: %v", err)
	}
}

func insertCanonicalExpansionStrategy(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO strategies(name,description,ticker,market_type,schedule_cron,config,is_active,is_paper,status,skip_next_run,active_thesis)
		VALUES($1,'legacy description','SPY','stock','0 9 * * *','{"legacy":true}',true,true,'active',false,'legacy thesis') RETURNING id`, "legacy-"+uuid.NewString()).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func assertCanonicalExpansionSeed(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var artifacts, bindings, outbox, activeAccounts, profiles int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM capital_margin_policy_artifacts),(SELECT count(*) FROM account_capital_policy_bindings),
		(SELECT count(*) FROM account_projection_outbox),(SELECT count(*) FROM accounts WHERE id='00000000-0000-4000-8000-000000000064' AND status='active'),
		(SELECT count(*) FROM account_capital_policy_bindings WHERE account_id='00000000-0000-4000-8000-000000000064' AND environment='paper_scored' AND margin_profile='reg_t')`).Scan(&artifacts, &bindings, &outbox, &activeAccounts, &profiles); err != nil {
		t.Fatal(err)
	}
	if artifacts != 1 || bindings != 1 || outbox != 0 || activeAccounts != 1 || profiles != 1 {
		t.Fatalf("seed counts artifacts=%d bindings=%d outbox=%d active=%d profiles=%d", artifacts, bindings, outbox, activeAccounts, profiles)
	}
}

func assertTriggerEnabled(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table, trigger string) {
	t.Helper()
	var enabled string
	if err := pool.QueryRow(ctx, `SELECT tgenabled::TEXT FROM pg_trigger WHERE tgrelid=$1::regclass AND tgname=$2`, table, trigger).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != "O" {
		t.Fatalf("trigger %s on %s enabled=%q", trigger, table, enabled)
	}
}
