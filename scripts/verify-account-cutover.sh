#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf 'usage: %s MODE\n' "$0" >&2
  printf 'modes: --schema-matrix --writer-fixtures --api-matrix --target-zero-history-audit --target-graph-audit\n' >&2
  exit 2
}

[[ $# -eq 1 ]] || usage
mode=$1
case $mode in
  --schema-matrix|--writer-fixtures|--api-matrix|--target-zero-history-audit|--target-graph-audit) ;;
  *) usage ;;
esac

repo_root=$(git -C "$(dirname -- "$0")/.." rev-parse --show-toplevel)
cd "$repo_root"
: "${POSTGRES_USER:?set POSTGRES_USER}"
compose=(docker compose --env-file .env -f docker-compose.nuc.yml)
"${compose[@]}" config --quiet

psql_db() {
  local database=$1
  shift
  "${compose[@]}" exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$database" "$@"
}

target_mode=false
[[ $mode == --target-zero-history-audit || $mode == --target-graph-audit ]] && target_mode=true

if $target_mode; then
  : "${TARGET_DB_NAME:?set TARGET_DB_NAME}"
  [[ $TARGET_DB_NAME =~ ^[A-Za-z_][A-Za-z0-9_]*$ && $TARGET_DB_NAME != tradingagent ]] || {
    printf 'refusing unsafe target database\n' >&2
    exit 2
  }
else
  [[ -z ${TARGET_DB_NAME:-} ]] || {
    printf 'disposable modes reject TARGET_DB_NAME\n' >&2
    exit 2
  }
fi

if [[ $mode == --target-zero-history-audit ]]; then
  psql_db "$TARGET_DB_NAME" -qAt <<'SQL' | grep -qx '109|f|t|t|1|1|0'
BEGIN READ ONLY;
SET ROLE augr_db_owner;
SELECT
  (SELECT version FROM schema_migrations),
  (SELECT dirty FROM schema_migrations),
  (SELECT pg_get_userbyid(datdba)='augr_db_owner' FROM pg_database WHERE datname=current_database()),
  has_function_privilege('augr_projection_writer','public.persist_canonical_projection_checkpoint(bytea,text,bytea)','EXECUTE'),
  (SELECT count(*) FROM accounts WHERE id='00000000-0000-4000-8000-000000000064' AND status='active' AND environment='paper_scored'),
  (SELECT count(*) FROM account_capital_policy_bindings WHERE account_id='00000000-0000-4000-8000-000000000064' AND environment='paper_scored'),
  (SELECT count(*) FROM strategies)+
  (SELECT count(*) FROM pipeline_runs)+(SELECT count(*) FROM pipeline_run_snapshots)+
  (SELECT count(*) FROM agent_decisions)+(SELECT count(*) FROM agent_events)+
  (SELECT count(*) FROM trade_decisions)+(SELECT count(*) FROM orders)+
  (SELECT count(*) FROM positions)+(SELECT count(*) FROM trades)+
  (SELECT count(*) FROM portfolio_opportunities)+(SELECT count(*) FROM allocation_decisions)+
  (SELECT count(*) FROM replay_events)+(SELECT count(*) FROM financial_fill_idempotency)+
  (SELECT count(*) FROM prediction_settlement_idempotency)+(SELECT count(*) FROM execution_intents)+
  (SELECT count(*) FROM execution_orders)+(SELECT count(*) FROM execution_fills)+
  (SELECT count(*) FROM copy_subscriptions)+(SELECT count(*) FROM copy_trade_intents)+
  (SELECT count(*) FROM copy_origin_rebalance_runs)+(SELECT count(*) FROM copy_origin_rebalance_intents)+
  (SELECT count(*) FROM copy_target_drift_runs)+(SELECT count(*) FROM copy_target_drift_legs)+
  (SELECT count(*) FROM ledger_transactions)+(SELECT count(*) FROM ledger_postings)+
  (SELECT count(*) FROM economic_source_events)+(SELECT count(*) FROM economic_event_normalizations)+
  (SELECT count(*) FROM account_projection_outbox)+(SELECT count(*) FROM projection_checkpoints);
COMMIT;
SQL
  exit 0
fi

if [[ $mode == --target-graph-audit ]]; then
  : "${TARGET_ACCOUNT_ID:?set TARGET_ACCOUNT_ID}"
  : "${TARGET_RUN_ID:?set TARGET_RUN_ID}"
  : "${TARGET_RUN_TRADE_DATE:?set TARGET_RUN_TRADE_DATE}"
  [[ $TARGET_ACCOUNT_ID =~ ^[0-9a-fA-F-]{36}$ && $TARGET_RUN_ID =~ ^[0-9a-fA-F-]{36}$ && $TARGET_RUN_TRADE_DATE =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || usage
  graph_result=$(psql_db "$TARGET_DB_NAME" -qAt \
    --set=account_id="$TARGET_ACCOUNT_ID" --set=run_id="$TARGET_RUN_ID" --set=trade_date="$TARGET_RUN_TRADE_DATE" <<'SQL'
BEGIN READ ONLY;
SET ROLE augr_db_owner;
WITH target AS MATERIALIZED (
  SELECT id,trade_date,account_id,environment,origin_type,origin_id
  FROM pipeline_runs
  WHERE id=:'run_id'::uuid AND trade_date=:'trade_date'::date
), run_orders AS MATERIALIZED (
  SELECT o.* FROM orders o JOIN target r ON (o.pipeline_run_id,o.pipeline_run_trade_date)=(r.id,r.trade_date)
), run_trades AS MATERIALIZED (
  SELECT t.* FROM trades t JOIN target r ON (t.pipeline_run_id,t.pipeline_run_trade_date)=(r.id,r.trade_date)
), common_orders AS MATERIALIZED (
  SELECT eo.* FROM execution_orders eo JOIN run_orders o ON o.id=eo.id
), intents AS MATERIALIZED (
  SELECT ei.* FROM execution_intents ei JOIN common_orders eo ON eo.intent_id=ei.id
), fills AS MATERIALIZED (
  SELECT f.* FROM execution_fills f JOIN common_orders eo ON eo.id=f.order_id
), normalizations AS MATERIALIZED (
  SELECT n.* FROM economic_event_normalizations n JOIN fills f ON f.normalization_id=n.id
), ledger AS MATERIALIZED (
  SELECT t.* FROM ledger_transactions t JOIN fills f ON f.ledger_transaction_id=t.id
), outbox AS MATERIALIZED (
  SELECT o.* FROM account_projection_outbox o JOIN ledger t ON t.id=o.through_transaction_id
), checkpoints AS MATERIALIZED (
  SELECT c.* FROM projection_checkpoints c JOIN outbox o ON o.through_transaction_id=c.through_transaction_id
), lifecycle AS MATERIALIZED (
  SELECT e.* FROM execution_lifecycle_events e JOIN intents i ON i.id=e.intent_id
), direct_scope_violations AS (
  SELECT count(*) AS n FROM (
    SELECT s.account_id,s.environment,s.origin_type,s.origin_id FROM pipeline_run_snapshots s JOIN target r ON (s.pipeline_run_id,s.pipeline_run_trade_date)=(r.id,r.trade_date)
    UNION ALL SELECT d.account_id,d.environment,d.origin_type,d.origin_id FROM agent_decisions d JOIN target r ON (d.pipeline_run_id,d.pipeline_run_trade_date)=(r.id,r.trade_date)
    UNION ALL SELECT e.account_id,e.environment,e.origin_type,e.origin_id FROM agent_events e JOIN target r ON (e.pipeline_run_id,e.pipeline_run_trade_date)=(r.id,r.trade_date)
    UNION ALL SELECT d.account_id,d.environment,d.origin_type,d.origin_id FROM trade_decisions d JOIN target r ON (d.pipeline_run_id,d.pipeline_run_trade_date)=(r.id,r.trade_date)
    UNION ALL SELECT o.account_id,o.environment,o.origin_type,o.origin_id FROM run_orders o
    UNION ALL SELECT t.account_id,t.environment,t.origin_type,t.origin_id FROM run_trades t
    UNION ALL SELECT o.account_id,o.environment,o.origin_type,o.origin_id FROM portfolio_opportunities o JOIN target r ON (o.pipeline_run_id,o.pipeline_run_trade_date)=(r.id,r.trade_date)
    UNION ALL SELECT d.account_id,d.environment,d.origin_type,d.origin_id FROM allocation_decisions d JOIN target r ON (d.pipeline_run_id,d.pipeline_run_trade_date)=(r.id,r.trade_date)
  ) scoped CROSS JOIN target r
  WHERE scoped.account_id IS DISTINCT FROM r.account_id OR scoped.environment IS DISTINCT FROM r.environment
     OR scoped.origin_type IS DISTINCT FROM r.origin_type OR scoped.origin_id IS DISTINCT FROM r.origin_id
), unbalanced AS (
  SELECT t.id FROM ledger t LEFT JOIN ledger_postings p ON p.transaction_id=t.id
  GROUP BY t.id,t.posting_count
  HAVING count(p.id)<>t.posting_count
     OR EXISTS (SELECT 1 FROM ledger_postings p2 WHERE p2.transaction_id=t.id GROUP BY p2.unit_kind,p2.unit HAVING sum(p2.amount)<>0)
), violations AS (
  SELECT
    (SELECT n FROM direct_scope_violations)+
    (SELECT count(*) FROM run_orders o CROSS JOIN target r WHERE o.account_id<>r.account_id OR o.pipeline_run_id IS NULL OR o.pipeline_run_trade_date IS NULL OR (o.copy_origin_rebalance_run_id IS NULL)<>(o.origin_type<>'copy_subscription'))+
    (SELECT count(*) FROM run_trades t CROSS JOIN target r WHERE t.account_id<>r.account_id OR t.pipeline_run_id IS NULL OR t.pipeline_run_trade_date IS NULL)+
    (SELECT count(*) FROM common_orders o CROSS JOIN target r WHERE o.account_id<>r.account_id)+
    (SELECT count(*) FROM intents i CROSS JOIN target r WHERE i.account_id<>r.account_id OR i.environment<>r.environment OR i.origin_type<>r.origin_type OR i.origin_id<>r.origin_id)+
    (SELECT count(*) FROM fills f CROSS JOIN target r WHERE f.account_id<>r.account_id)+
    (SELECT count(*) FROM ledger t CROSS JOIN target r WHERE t.account_id<>r.account_id)+
    (SELECT count(*) FROM outbox o CROSS JOIN target r WHERE o.account_id<>r.account_id OR o.status NOT IN ('completed','degraded'))+
    (SELECT count(*) FROM outbox o WHERE o.status='completed' AND NOT EXISTS (
      SELECT 1 FROM checkpoints c WHERE c.account_id=o.account_id AND c.through_transaction_id=o.through_transaction_id
        AND c.attestation_key_id IS NOT NULL AND octet_length(c.attestation_hmac)=32))+
    (SELECT count(*) FROM unbalanced) AS n
)
SELECT
  (SELECT count(*) FROM target),
  (SELECT count(*) FROM target WHERE account_id=:'account_id'::uuid),
  (SELECT count(*) FROM agent_decisions d JOIN target r ON (d.pipeline_run_id,d.pipeline_run_trade_date)=(r.id,r.trade_date)),
  (SELECT count(*) FROM agent_events e JOIN target r ON (e.pipeline_run_id,e.pipeline_run_trade_date)=(r.id,r.trade_date)),
  (SELECT count(*) FROM run_orders),(SELECT count(*) FROM run_trades),
  (SELECT count(*) FROM intents),(SELECT count(*) FROM common_orders),(SELECT count(*) FROM fills),
  (SELECT count(*) FROM lifecycle),(SELECT count(*) FROM normalizations),(SELECT count(*) FROM ledger),
  (SELECT count(*) FROM outbox),(SELECT count(*) FROM checkpoints),(SELECT n FROM violations);
COMMIT;
SQL
  )
  IFS='|' read -r run_count owned_count decisions events orders trades intents common_orders fills lifecycle normalizations ledger outbox checkpoints violations <<<"$graph_result"
  [[ $run_count == 1 && $owned_count == 1 ]] || {
    printf 'target run identity is absent, ambiguous, or owned by another account\n' >&2
    exit 1
  }
  (( decisions + events > 0 )) || {
    printf 'target run graph is vacuous\n' >&2
    exit 1
  }
  [[ $violations == 0 ]] || {
    printf 'target run graph contains %s scope, linkage, ledger, outbox, or checkpoint violations\n' "$violations" >&2
    exit 1
  }
  if (( fills > 0 || trades > 0 )); then
    (( orders > 0 && intents > 0 && common_orders > 0 && fills > 0 && lifecycle > 0 && normalizations > 0 && ledger > 0 && outbox > 0 )) || {
      printf 'accepted fill graph is incomplete (orders=%s trades=%s intents=%s common_orders=%s fills=%s lifecycle=%s normalizations=%s ledger=%s outbox=%s checkpoints=%s)\n' "$orders" "$trades" "$intents" "$common_orders" "$fills" "$lifecycle" "$normalizations" "$ledger" "$outbox" "$checkpoints" >&2
      exit 1
    }
  fi
  exit 0
fi

fixture_databases=()
cleanup() {
  local database
  for database in "${fixture_databases[@]}"; do
    psql_db postgres --set=db_name="$database" <<'SQL' >/dev/null 2>&1 || true
SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=:'db_name' AND pid<>pg_backend_pid();
SELECT format('DROP DATABASE IF EXISTS %I', :'db_name') \gexec
SQL
  done
}
trap cleanup EXIT INT TERM

create_fixture() {
  local database="augr_cutover_fixture_${$}_${RANDOM}"
  fixture_databases+=("$database")
  psql_db postgres --set=db_name="$database" <<'SQL' >/dev/null
SELECT format('CREATE DATABASE %I OWNER augr_db_owner', :'db_name') \gexec
SQL
  created_fixture=$database
}

assert_schema() {
  local database=$1 expected=$2
  psql_db "$database" -At -c "SELECT version::text || '|' || dirty::text FROM schema_migrations" | grep -qx "$expected|false"
}

case $mode in
  --schema-matrix)
    create_fixture
    legacy_db=$created_fixture
    ./scripts/apply-migrations-psql.sh --database "$legacy_db" --from 0 --to 107
    assert_schema "$legacy_db" 107
    ./scripts/apply-migrations-psql.sh --database "$legacy_db" --from 107 --to 108
    assert_schema "$legacy_db" 108
    psql_db "$legacy_db" -At -c "SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND column_name='account_id'" | grep -Eq '^[1-9][0-9]*$'
    ./scripts/apply-migrations-psql.sh --database "$legacy_db" --from 108 --to 109
    assert_schema "$legacy_db" 109
    create_fixture
    fresh_db=$created_fixture
    ./scripts/apply-migrations-psql.sh --database "$fresh_db" --from 0 --to 109
    assert_schema "$fresh_db" 109
    go test ./migrations ./internal/repository/postgres -run 'Test.*(108|109|SchemaCompatibility|Enforcement)'
    ;;
  --writer-fixtures)
    create_fixture
    fixture_db=$created_fixture
    ./scripts/apply-migrations-psql.sh --database "$fixture_db" --from 0 --to 109
    assert_schema "$fixture_db" 109
    go test ./internal/execution/... ./internal/repository/postgres -run 'Test.*(Scope|Writer|Accepted|Fill|Copy|Settlement|Restart|Risk|ProjectionOutbox)'
    ;;
  --api-matrix)
    create_fixture
    fixture_db=$created_fixture
    ./scripts/apply-migrations-psql.sh --database "$fixture_db" --from 0 --to 109
    assert_schema "$fixture_db" 109
    go test ./internal/api -run 'Test.*(Account|CrossAccount|Legacy|WebSocket|Route)'
    ;;
esac
