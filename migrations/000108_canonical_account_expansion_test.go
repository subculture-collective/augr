package migrations_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

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
		"mark_source text",
		"mark_namespace text",
		"max_mark_age_microseconds bigint",
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
	ledgerLock := strings.Index(down, "ledger_transactions, account_projection_outbox")
	frontierCheck := strings.Index(down, "checkpoint.projection_version is not null")
	if ledgerLock < 0 || frontierCheck < 0 || ledgerLock > frontierCheck {
		t.Fatal("down migration must lock ledger_transactions before checkpoint frontier safety checks")
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
	assertCanonicalExpansionOutboxContract(t, ctx, pool)

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
	var outbox, validator any
	if err := pool.QueryRow(ctx, `SELECT to_regclass('account_projection_outbox'),to_regprocedure('validate_account_projection_outbox_row()')`).Scan(&outbox, &validator); err != nil {
		t.Fatal(err)
	}
	if outbox != nil || validator != nil {
		t.Fatalf("migration-108 objects remain at 107: %v %v", outbox, validator)
	}
	assertCanonicalExpansionRemoved(t, ctx, pool)
	assertSchema69CheckpointValidatorRestored(t, ctx, pool)
	applyCanonicalExpansion(t, ctx, pool)
	assertCanonicalExpansionSeed(t, ctx, pool)
	assertCanonicalExpansionOutboxContract(t, ctx, pool)
}

func TestCanonicalAccountExpansionOldWriterCanary(t *testing.T) {
	ctx, pool := newCanonicalExpansionPool(t)
	strategyID := insertCanonicalExpansionStrategy(t, ctx, pool)
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
	legacyGraph := insertLegacyPipelineCopyGraph(t, ctx, pool, strategyID, leaderID, sourceID, observationID)
	before108 := snapshotCopyOriginFields(t, ctx, pool, legacyGraph)
	applyCanonicalExpansion(t, ctx, pool)
	assertExpansionColumnsRemainOptional(t, ctx, pool)
	after108 := snapshotCopyOriginFields(t, ctx, pool, legacyGraph)
	assertCopyOriginFieldsEqual(t, "schema 107 to 108", before108, after108)

	secondStrategyID := insertCanonicalExpansionStrategy(t, ctx, pool)
	secondGraph := insertLegacyPipelineCopyGraph(t, ctx, pool, secondStrategyID, leaderID, sourceID, observationID)
	insertLegacyOperationalGraph(t, ctx, pool, secondStrategyID, secondGraph.runID)
	insertLegacyCopyDriftGraph(t, ctx, pool, secondGraph.subscriptionID, observationID)
	intentID, orderID := insertLegacyExecutionGraph(t, ctx, pool)
	post108 := snapshotCopyOriginFields(t, ctx, pool, secondGraph)
	expectedPost108 := expectedCopyOriginFields(secondGraph.subscriptionID)
	assertCopyOriginFieldsEqual(t, "post-108 old-shape insert", expectedPost108, post108)

	var nullExpansion bool
	if err := pool.QueryRow(ctx, `SELECT
		(subscription.account_id IS NULL AND subscription.environment IS NULL AND intent.account_id IS NULL AND intent.environment IS NULL AND intent.pipeline_run_trade_date IS NULL AND run.account_id IS NULL AND run.environment IS NULL AND run.origin_type IS NULL AND run.origin_id IS NULL)
		FROM copy_subscriptions subscription JOIN copy_trade_intents intent ON intent.subscription_id=subscription.id JOIN pipeline_runs run ON run.id=$3
		WHERE subscription.id=$1 AND intent.id=$2`, legacyGraph.subscriptionID, legacyGraph.intentID, legacyGraph.runID).Scan(&nullExpansion); err != nil {
		t.Fatal(err)
	}
	if !nullExpansion {
		t.Fatal("migration 108 populated expansion fields on the schema-107 graph")
	}
	var secondNullExpansion bool
	if err := pool.QueryRow(ctx, `SELECT
		(subscription.account_id IS NULL AND subscription.environment IS NULL AND
		 intent.account_id IS NULL AND intent.environment IS NULL AND intent.pipeline_run_trade_date IS NULL AND
		 strategy.execution_strategy_version_id IS NULL AND
		 run.account_id IS NULL AND run.environment IS NULL AND run.origin_type IS NULL AND run.origin_id IS NULL AND
		 rebalance.account_id IS NULL AND rebalance.environment IS NULL AND
		 rebalance_intent.account_id IS NULL AND rebalance_intent.environment IS NULL AND
		 rebalance_intent.origin_type IS NULL AND rebalance_intent.origin_id IS NULL)
		FROM copy_subscriptions subscription
		JOIN copy_trade_intents intent ON intent.subscription_id=subscription.id
		JOIN pipeline_runs run ON run.id=$3
		JOIN strategies strategy ON strategy.id=run.strategy_id
		JOIN copy_origin_rebalance_runs rebalance ON rebalance.subscription_id=subscription.id
		JOIN copy_origin_rebalance_intents rebalance_intent ON rebalance_intent.run_id=rebalance.id
		WHERE subscription.id=$1 AND intent.id=$2`, secondGraph.subscriptionID, secondGraph.intentID, secondGraph.runID).Scan(&secondNullExpansion); err != nil {
		t.Fatal(err)
	}
	if !secondNullExpansion {
		t.Fatal("post-108 old-shape insert populated expansion fields")
	}
	var executionNulls bool
	if err := pool.QueryRow(ctx, `SELECT intent.copy_origin_rebalance_run_id IS NULL AND execution_order.copy_origin_rebalance_run_id IS NULL
		FROM execution_intents intent JOIN execution_orders execution_order ON execution_order.intent_id=intent.id
		WHERE intent.id=$1 AND execution_order.id=$2`, intentID, orderID).Scan(&executionNulls); err != nil {
		t.Fatal(err)
	}
	if !executionNulls {
		t.Fatal("schema-107 execution lifecycle insert populated migration-108 columns")
	}
	var driftNulls bool
	if err := pool.QueryRow(ctx, `SELECT run.origin_type='copy_subscription' AND run.origin_id=run.subscription_id AND run.account_id IS NULL AND run.environment IS NULL AND leg.account_id IS NULL AND leg.environment IS NULL AND leg.origin_type IS NULL AND leg.origin_id IS NULL
		FROM copy_target_drift_runs run JOIN copy_target_drift_legs leg ON leg.run_id=run.id
		WHERE run.subscription_id=$1`, secondGraph.subscriptionID).Scan(&driftNulls); err != nil {
		t.Fatal(err)
	}
	if !driftNulls {
		t.Fatal("schema-107 copy drift insert lost its origin or populated migration-108 columns")
	}
	assertEveryExpansionTableHasNullOldWriterRow(t, ctx, pool)
	assertTriggerEnabled(t, ctx, pool, "copy_origin_rebalance_runs", "copy_origin_runs_append_only")
	assertTriggerEnabled(t, ctx, pool, "copy_origin_rebalance_intents", "copy_origin_run_intents_append_only")
}

func insertLegacyOperationalGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool, strategyID, runID uuid.UUID) {
	t.Helper()
	orderID, positionID, tradeID, decisionID, replayID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	key := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO pipeline_run_snapshots(pipeline_run_id,data_type,payload) VALUES($1,'market','{}');
		INSERT INTO agent_decisions(pipeline_run_id,agent_role,phase,output_text) VALUES($1,'legacy','analysis','ok');
		INSERT INTO agent_events(pipeline_run_id,strategy_id,event_kind,title) VALUES($1,$2,'legacy','old writer');
		INSERT INTO orders(id,strategy_id,pipeline_run_id,ticker,side,order_type,quantity) VALUES($3,$2,$1,'QQQ','buy','market',1);
		INSERT INTO positions(id,strategy_id,ticker,side,quantity,avg_entry) VALUES($4,$2,'QQQ','long',1,10);
		INSERT INTO trades(id,order_id,position_id,ticker,side,quantity,price) VALUES($5,$3,$4,'QQQ','buy',1,10);
		INSERT INTO trade_decisions(id,strategy_id,pipeline_run_id,market_type,instrument_key,side,risk_status,status) VALUES($6,$2,$1,'stock','QQQ','buy','approved','candidate');
		INSERT INTO replay_events(id,trade_decision_id,event_type,occurred_at) VALUES($7,$6,'decision_created',now());
		INSERT INTO portfolio_opportunities(strategy_id,pipeline_run_id,market_type,ticker,side,signal,status,expires_at,dedupe_key) VALUES($2,$1,'stock','QQQ','buy','buy','queued',now()+interval '1 day',$8);
		INSERT INTO allocation_decisions(opportunity_id,strategy_id,mode,action) SELECT id,$2,'paper','paper_order_intent' FROM portfolio_opportunities WHERE dedupe_key=$8;
		INSERT INTO financial_fill_idempotency(idempotency_key,order_id,position_id,trade_id,fill_quantity,fill_price) VALUES('fill-'||$8,$3,$4,$5,1,10);
		INSERT INTO prediction_settlement_idempotency(idempotency_key,decision_id,position_id,trade_id,replay_event_id,payout,resolved_at) VALUES('settlement-'||$8,$6,$4,$5,$7,1,now())`,
		runID, strategyID, orderID, positionID, tradeID, decisionID, replayID, key); err != nil {
		t.Fatalf("insert schema-107 operational graph after migration 108: %v", err)
	}
}

func assertEveryExpansionTableHasNullOldWriterRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for table, columns := range canonicalExpansionColumns() {
		conditions := make([]string, len(columns))
		for i, column := range columns {
			conditions[i] = pgx.Identifier{column}.Sanitize() + " IS NULL"
		}
		var count int
		query := `SELECT count(*) FROM ` + pgx.Identifier{table}.Sanitize() + ` WHERE ` + strings.Join(conditions, " AND ")
		if err := pool.QueryRow(ctx, query).Scan(&count); err != nil {
			t.Fatalf("assert old-writer row for %s: %v", table, err)
		}
		if count == 0 {
			t.Errorf("%s has no schema-107-shape row with every migration-108 column NULL", table)
		}
	}
}

func insertLegacyCopyDriftGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool, subscriptionID, observationID uuid.UUID) {
	t.Helper()
	runID := uuid.New()
	canonical := `{"schema":"copy-target-drift-session-v1","state":"prepared","subscription_id":"` + subscriptionID.String() + `","origin_type":"copy_subscription","origin_id":"` + subscriptionID.String() + `","source_observation_id":"` + observationID.String() + `","session_key":"2026-08-27/regular","calculation_version":1,"maximum_session_turnover":100,"session_budget":10,"starting_drift":10,"prepared_turnover":10,"residual_drift":0,"converged":true,"legs":[{"sequence":0,"instrument_key":"QQQ","side":"buy","current_value":0,"target_value":10,"starting_drift":10,"requested_notional":10,"projected_value":10,"residual_drift":0}]}`
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err = tx.Exec(ctx, `INSERT INTO copy_target_drift_runs(id,schema_name,state,subscription_id,origin_type,origin_id,source_observation_id,session_key,calculation_version,maximum_session_turnover,session_budget,starting_drift,prepared_turnover,residual_drift,converged,leg_count,sha256,canonical_bytes,canonical_json)
		VALUES($1,'copy-target-drift-session-v1','prepared',$2,'copy_subscription',$2,$3,'2026-08-27/regular',1,100,10,10,10,0,true,1,encode(digest(convert_to($4,'UTF8'),'sha256'),'hex'),convert_to($4,'UTF8'),$4::JSONB)`, runID, subscriptionID, observationID, canonical); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO copy_target_drift_legs(run_id,sequence,instrument_key,side,current_value,target_value,starting_drift,requested_notional,projected_value,residual_drift,canonical_leg)
		VALUES($1,0,'QQQ','buy',0,10,10,10,10,0,($2::JSONB)->'legs'->0)`, runID, canonical); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func insertLegacyExecutionGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	fixture := commonExecutionLifecycleMigrationFixture{AccountID: uuid.MustParse("00000000-0000-4000-8000-000000000064"), InstrumentID: uuid.New(), VenueContractID: uuid.New(), QuoteSnapshotID: uuid.New(), DecisionAt: time.Date(2026, 8, 27, 19, 0, 0, 123456000, time.UTC)}
	if _, err := pool.Exec(ctx, `INSERT INTO instruments(id,identity_key,asset_class,primary_venue,currency,tick_size,lot_size,multiplier,settlement_method,status) VALUES($1,$2,'equity','test-venue','USD',0.01,1,1,'physical','active');
		INSERT INTO venue_contracts(id,instrument_id,venue,contract_id,currency,tick_size,lot_size,multiplier,settlement_method,valid_from,valid_to) VALUES($3,$1,'test-venue',$4,'USD',0.01,1,1,'physical',$5::TIMESTAMPTZ-interval '1 day',$5::TIMESTAMPTZ+interval '1 day');
		INSERT INTO quote_snapshots(id,instrument_id,venue_contract_id,provider,venue,source,observation_namespace,observation_id,exchange_at,received_at,available_at,bid,ask,bid_depth_count,ask_depth_count) VALUES($6,$1,$3,'fixture','test-venue','fixture-feed','quotes/expansion',$7,$5::TIMESTAMPTZ-interval '3 seconds',$5::TIMESTAMPTZ-interval '2 seconds',$5::TIMESTAMPTZ-interval '1 second',10.24,10.26,0,0)`, fixture.InstrumentID, "figi:expansion:"+fixture.InstrumentID.String(), fixture.VenueContractID, "EXPANSION-"+strings.ToUpper(strings.ReplaceAll(fixture.InstrumentID.String(), "-", "")), fixture.DecisionAt, fixture.QuoteSnapshotID, "quote-"+fixture.InstrumentID.String()); err != nil {
		t.Fatal(err)
	}
	key := "expansion-" + uuid.NewString()
	intentID := persistRiskApprovedMigrationLifecycle(t, ctx, pool, fixture, key)
	if err := insertMigrationLifecycleOrder(t, ctx, pool, fixture, intentID, key, "simulation", "simulation-v1"); err != nil {
		t.Fatal(err)
	}
	var orderID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM execution_orders WHERE intent_id=$1`, intentID).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	return intentID, orderID
}

func assertExpansionColumnsRemainOptional(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for table, columns := range canonicalExpansionColumns() {
		for _, column := range columns {
			var nullable string
			var defaultValue *string
			if err := pool.QueryRow(ctx, `SELECT is_nullable,column_default FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2`, table, column).Scan(&nullable, &defaultValue); err != nil {
				t.Fatalf("%s.%s: %v", table, column, err)
			}
			if nullable != "YES" || defaultValue != nil {
				t.Errorf("schema-107 writer compatibility for %s.%s: nullable=%s default=%v", table, column, nullable, defaultValue)
			}
		}
	}
}

type legacyCopyGraph struct {
	runID          uuid.UUID
	subscriptionID uuid.UUID
	intentID       uuid.UUID
	rebalanceRunID uuid.UUID
}

type copyOriginFields struct {
	subscriptionType string
	subscriptionID   []byte
	intentType       string
	intentID         []byte
	rebalanceType    string
	rebalanceID      []byte
}

func insertLegacyPipelineCopyGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool, strategyID, leaderID, sourceID, observationID uuid.UUID) legacyCopyGraph {
	t.Helper()
	var runID, subscriptionID, intentID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO pipeline_runs(strategy_id,ticker,trade_date,started_at) VALUES($1,'QQQ','2026-08-27',now()) RETURNING id`, strategyID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO copy_subscriptions(leader_id,source_id,legacy_strategy_id,capital_budget) VALUES($1,$2,$3,2000) RETURNING id`, leaderID, sourceID, strategyID).Scan(&subscriptionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO copy_trade_intents(subscription_id,source_observation_id,pipeline_run_id,instrument_key,ticker,side,policy_status) VALUES($1,$2,$3,'QQQ','QQQ','buy','approved') RETURNING id`, subscriptionID, observationID, runID).Scan(&intentID); err != nil {
		t.Fatal(err)
	}
	rebalanceID := uuid.New()
	canonical := `{"schema":"copy-origin-rebalance-v1"}`
	if _, err := pool.Exec(ctx, `INSERT INTO copy_origin_rebalance_runs(id,schema_name,state,subscription_id,origin_type,origin_id,source_observation_id,calculation_version,intent_count,sha256,canonical_bytes,canonical_json)
		VALUES($1,'copy-origin-rebalance-v1','prepared',$2,'copy_subscription',$2,$3,1,1,encode(digest(convert_to($4,'UTF8'),'sha256'),'hex'),convert_to($4,'UTF8'),$4::JSONB)`, rebalanceID, subscriptionID, observationID, canonical); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO copy_origin_rebalance_intents(run_id,sequence,intent_id,instrument_key,source_observation_id,canonical_intent) VALUES($1,0,$2,'QQQ',$3,'{}')`, rebalanceID, intentID, observationID); err != nil {
		t.Fatal(err)
	}
	return legacyCopyGraph{runID: runID, subscriptionID: subscriptionID, intentID: intentID, rebalanceRunID: rebalanceID}
}

func snapshotCopyOriginFields(t *testing.T, ctx context.Context, pool *pgxpool.Pool, graph legacyCopyGraph) copyOriginFields {
	t.Helper()
	var fields copyOriginFields
	if err := pool.QueryRow(ctx, `SELECT
		subscription.origin_type,uuid_send(subscription.origin_id),
		intent.origin_type,uuid_send(intent.origin_id),
		rebalance.origin_type,uuid_send(rebalance.origin_id)
		FROM copy_subscriptions subscription
		JOIN copy_trade_intents intent ON intent.id=$2 AND intent.subscription_id=subscription.id
		JOIN copy_origin_rebalance_runs rebalance ON rebalance.id=$3 AND rebalance.subscription_id=subscription.id
		WHERE subscription.id=$1`, graph.subscriptionID, graph.intentID, graph.rebalanceRunID).Scan(
		&fields.subscriptionType, &fields.subscriptionID,
		&fields.intentType, &fields.intentID,
		&fields.rebalanceType, &fields.rebalanceID,
	); err != nil {
		t.Fatal(err)
	}
	return fields
}

func expectedCopyOriginFields(subscriptionID uuid.UUID) copyOriginFields {
	id := append([]byte(nil), subscriptionID[:]...)
	return copyOriginFields{
		subscriptionType: "copy_subscription",
		subscriptionID:   append([]byte(nil), id...),
		intentType:       "copy_subscription",
		intentID:         append([]byte(nil), id...),
		rebalanceType:    "copy_subscription",
		rebalanceID:      append([]byte(nil), id...),
	}
}

func assertCopyOriginFieldsEqual(t *testing.T, stage string, want, got copyOriginFields) {
	t.Helper()
	if want.subscriptionType != got.subscriptionType {
		t.Errorf("%s copy_subscriptions.origin_type: want %q, got %q", stage, want.subscriptionType, got.subscriptionType)
	}
	if !bytes.Equal(want.subscriptionID, got.subscriptionID) {
		t.Errorf("%s copy_subscriptions.origin_id bytes: want %x, got %x", stage, want.subscriptionID, got.subscriptionID)
	}
	if want.intentType != got.intentType {
		t.Errorf("%s copy_trade_intents.origin_type: want %q, got %q", stage, want.intentType, got.intentType)
	}
	if !bytes.Equal(want.intentID, got.intentID) {
		t.Errorf("%s copy_trade_intents.origin_id bytes: want %x, got %x", stage, want.intentID, got.intentID)
	}
	if want.rebalanceType != got.rebalanceType {
		t.Errorf("%s copy_origin_rebalance_runs.origin_type: want %q, got %q", stage, want.rebalanceType, got.rebalanceType)
	}
	if !bytes.Equal(want.rebalanceID, got.rebalanceID) {
		t.Errorf("%s copy_origin_rebalance_runs.origin_id bytes: want %x, got %x", stage, want.rebalanceID, got.rebalanceID)
	}
}

func assertCanonicalExpansionOutboxContract(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var accountID, frontierID uuid.UUID
	var asOf time.Time
	if err := pool.QueryRow(ctx, `SELECT account_id,id,GREATEST(effective_at,observed_at) FROM ledger_transactions ORDER BY effective_at,observed_at,id LIMIT 1`).Scan(&accountID, &frontierID, &asOf); err != nil {
		t.Fatal(err)
	}
	asOf = asOf.UTC().Truncate(time.Microsecond)

	insert := func(name, kind, status string, markAsOf any, generation uuid.UUID, markSource, markNamespace any, maxAge any, claimedAt, claimedBy, claimExpiresAt any, wantOK bool) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx) //nolint:errcheck
			_, err = tx.Exec(ctx, `INSERT INTO account_projection_outbox(
				id,account_id,request_kind,through_transaction_id,as_of,mark_as_of,mark_generation,
				mark_source,mark_namespace,max_mark_age_microseconds,status,next_attempt_at,
				claimed_at,claimed_by,claim_expires_at,created_at,updated_at)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$5,$12,$13,$14,$5,$5)`,
				uuid.New(), accountID, kind, frontierID, asOf, markAsOf, generation,
				markSource, markNamespace, maxAge, status, claimedAt, claimedBy, claimExpiresAt)
			if wantOK && err != nil {
				t.Fatalf("valid outbox row rejected: %v", err)
			}
			if !wantOK && err == nil {
				t.Fatal("invalid outbox row accepted")
			}
		})
	}
	zero := uuid.Nil
	markGeneration := uuid.New()
	markAsOf := asOf.Add(-time.Microsecond)
	claimedAt := asOf.Add(time.Microsecond)
	claimExpiresAt := claimedAt.Add(time.Minute)
	insert("valid economic fill", "economic_fill", "pending", nil, zero, nil, nil, nil, nil, nil, nil, true)
	insert("economic fill rejects marks", "economic_fill", "pending", nil, zero, "test-source", nil, nil, nil, nil, nil, false)
	insert("valid mark rebuild", "mark_rebuild", "processing", markAsOf, markGeneration, "test-source", "marks/test", int64(time.Hour/time.Microsecond), claimedAt, "worker", claimExpiresAt, true)
	insert("mark rebuild requires source", "mark_rebuild", "pending", markAsOf, markGeneration, nil, "marks/test", int64(time.Hour/time.Microsecond), nil, nil, nil, false)
	insert("mark rebuild requires namespace", "mark_rebuild", "pending", markAsOf, markGeneration, "test-source", nil, int64(time.Hour/time.Microsecond), nil, nil, nil, false)
	insert("mark rebuild requires positive age", "mark_rebuild", "pending", markAsOf, markGeneration, "test-source", "marks/test", int64(0), nil, nil, nil, false)

	claimValues := []any{claimedAt, "worker", claimExpiresAt}
	for mask := 1; mask < 7; mask++ {
		fields := []any{nil, nil, nil}
		for index := range fields {
			if mask&(1<<index) != 0 {
				fields[index] = claimValues[index]
			}
		}
		insert("processing rejects partial claim "+string(rune('0'+mask)), "economic_fill", "processing", nil, zero, nil, nil, nil, fields[0], fields[1], fields[2], false)
		insert("pending rejects partial claim "+string(rune('0'+mask)), "economic_fill", "pending", nil, zero, nil, nil, nil, fields[0], fields[1], fields[2], false)
	}
	insert("pending rejects complete claim", "economic_fill", "pending", nil, zero, nil, nil, nil, claimedAt, "worker", claimExpiresAt, false)
}

func assertCanonicalExpansionRemoved(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	indexes := []string{
		"idx_pipeline_runs_account_trade_date", "idx_pipeline_run_snapshots_account_run", "idx_agent_decisions_account_run",
		"idx_agent_events_account_run", "idx_trade_decisions_account_created", "idx_orders_account_created",
		"idx_positions_account_opened", "idx_trades_account_executed", "idx_portfolio_opportunities_account_created",
		"idx_allocation_decisions_account_created", "idx_replay_events_account_occurred", "idx_financial_fill_idempotency_account",
		"idx_prediction_settlement_idempotency_account", "idx_copy_subscriptions_account_status", "idx_copy_trade_intents_account_created",
		"idx_copy_origin_rebalance_runs_account_created", "idx_copy_origin_rebalance_intents_account_run",
		"idx_copy_target_drift_runs_account_created", "idx_copy_target_drift_legs_account_run",
		"uq_account_projection_outbox_request", "idx_account_projection_outbox_claimable",
	}
	for _, index := range indexes {
		var found any
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1)`, index).Scan(&found); err != nil {
			t.Fatal(err)
		}
		if found != nil {
			t.Errorf("migration-108 index remains at 107: %s", index)
		}
	}
	columns := canonicalExpansionColumns()
	for table, names := range columns {
		for _, column := range names {
			var count int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2`, table, column).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Errorf("migration-108 column remains at 107: %s.%s", table, column)
			}
		}
	}
}

func canonicalExpansionColumns() map[string][]string {
	return map[string][]string{
		"strategies":                        {"execution_strategy_version_id"},
		"pipeline_runs":                     {"account_id", "environment", "origin_type", "origin_id"},
		"pipeline_run_snapshots":            {"account_id", "environment", "origin_type", "origin_id", "pipeline_run_trade_date"},
		"agent_decisions":                   {"account_id", "environment", "origin_type", "origin_id", "pipeline_run_trade_date"},
		"agent_events":                      {"account_id", "environment", "origin_type", "origin_id", "pipeline_run_trade_date"},
		"trade_decisions":                   {"account_id", "environment", "origin_type", "origin_id", "pipeline_run_trade_date"},
		"orders":                            {"account_id", "environment", "origin_type", "origin_id", "pipeline_run_trade_date", "copy_origin_rebalance_run_id"},
		"positions":                         {"account_id", "environment", "origin_type", "origin_id"},
		"trades":                            {"account_id", "environment", "origin_type", "origin_id"},
		"portfolio_opportunities":           {"account_id", "environment", "origin_type", "origin_id", "pipeline_run_trade_date"},
		"allocation_decisions":              {"account_id", "environment", "origin_type", "origin_id"},
		"replay_events":                     {"account_id", "environment", "origin_type", "origin_id"},
		"financial_fill_idempotency":        {"account_id", "environment", "origin_type", "origin_id"},
		"prediction_settlement_idempotency": {"account_id", "environment", "origin_type", "origin_id"},
		"copy_subscriptions":                {"account_id", "environment"},
		"copy_trade_intents":                {"account_id", "environment", "pipeline_run_trade_date"},
		"copy_origin_rebalance_runs":        {"account_id", "environment"},
		"copy_origin_rebalance_intents":     {"account_id", "environment", "origin_type", "origin_id"},
		"copy_target_drift_runs":            {"account_id", "environment"},
		"copy_target_drift_legs":            {"account_id", "environment", "origin_type", "origin_id"},
		"execution_intents":                 {"copy_origin_rebalance_run_id"},
		"execution_orders":                  {"copy_origin_rebalance_run_id"},
	}
}

func assertSchema69CheckpointValidatorRestored(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var definition string
	if err := pool.QueryRow(ctx, `SELECT pg_get_functiondef('validate_canonical_projection_checkpoint()'::regprocedure)`).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(definition, "expected_through_transaction_id") || !strings.Contains(definition, "ORDER BY effective_at DESC, observed_at DESC, id DESC") {
		t.Fatalf("schema-69 checkpoint validator not restored:\n%s", definition)
	}

	accountID := uuid.MustParse("00000000-0000-4000-8000-000000000064")
	var oldFrontier uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM ledger_transactions WHERE account_id=$1 ORDER BY effective_at,observed_at,id LIMIT 1`, accountID).Scan(&oldFrontier); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	newFrontier := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO ledger_transactions(id,account_id,event_type,idempotency_key,origin_type,origin_id,effective_at,observed_at,posting_count)
		VALUES($1,$2,'migration-test',$3,'migration-test',$3,$4,$4,2);
		INSERT INTO ledger_postings(transaction_id,idempotency_key,ledger_account,unit_kind,unit,amount) VALUES
		($1,'debit','migration-test-debit','currency','USD',1),($1,'credit','migration-test-credit','currency','USD',-1)`, newFrontier, accountID, uuid.NewString(), now); err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	fixture := ledgerProjectionMigrationFixture{AccountID: accountID, AsOf: now.Add(time.Minute), SigningKeyID: "migration-108-rollback-" + strings.ReplaceAll(uuid.NewString(), "-", ""), SigningSecret: secret}
	input := projectionMigrationCheckpoint(t, ctx, pool, fixture)
	input.ThroughTransactionID = oldFrontier
	var payload map[string]any
	if err := json.Unmarshal(input.PayloadBytes, &payload); err != nil {
		t.Fatal(err)
	}
	payload["through_transaction_id"] = oldFrontier.String()
	input.PayloadBytes, _ = json.Marshal(payload)
	input.Checksum = projectionMigrationSHA(input.PayloadBytes)
	input.AttestationHMAC = projectionMigrationHMAC(input.AttestationKeyID, secret, input.PayloadBytes)
	if err := insertProjectionMigrationCheckpointInput(ctx, pool, input); err == nil || !strings.Contains(err.Error(), "ledger boundary") {
		t.Fatalf("schema-69 validator accepted non-latest frontier: %v", err)
	}
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
