#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf 'usage: %s --database tradingagent --record-dir /var/lib/augr-cutover/canonical-20260827\n' "$0" >&2
  exit 2
}

[[ $# -eq 4 && $1 == --database && $3 == --record-dir ]] || usage
database=$2
record_dir=$4
[[ $database == tradingagent && $record_dir == /var/lib/augr-cutover/canonical-20260827 ]] || usage
: "${POSTGRES_USER:?set POSTGRES_USER}"
repo_root=$(git -C "$(dirname -- "$0")/.." rev-parse --show-toplevel)
cd "$repo_root"
docker compose --env-file .env -f docker-compose.nuc.yml config --quiet
(cd "$record_dir" && sha256sum -c manifest.sha256)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT
umask 077

psql_old() {
  docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres \
    psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$database" "$@"
}

psql_old -qAt <<'SQL' | python3 scripts/parse-old-db-snapshot.py "$work_dir"
BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
SELECT '__AUGR_SECTION_schema_catalog.tsv__';
SELECT version::text || E'\t' || dirty::text FROM schema_migrations;
SELECT n.nspname || '.' || c.relname || E'\t' || string_agg(a.attname || ':' || pg_catalog.format_type(a.atttypid,a.atttypmod), ',' ORDER BY a.attnum)
FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
WHERE n.nspname='public' AND c.relkind IN ('r','p') GROUP BY n.nspname,c.relname ORDER BY n.nspname,c.relname;
SELECT '__AUGR_SECTION_table_fingerprints.tsv__';
WITH baseline_tables AS (
  SELECT n.nspname AS schema_name,c.relname AS table_name FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE n.nspname='public' AND c.relkind IN ('r','p') AND NOT EXISTS (SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid)
    AND c.relname NOT IN ('risk_state','automation_job_controls','pipeline_runs','automation_job_runs','agent_events','audit_log')
)
SELECT format(
  'SELECT %L; WITH row_hashes AS MATERIALIZED (SELECT digest(to_jsonb(t)::text,''sha256'') AS row_hash FROM %I.%I t), bucket_summaries AS (SELECT encode(substring(row_hash FROM 1 FOR 1),''hex'') AS bucket,count(*) AS row_count,encode(digest(string_agg(encode(row_hash,''hex''),'''' ORDER BY row_hash),''sha256''),''hex'') AS bucket_digest FROM row_hashes GROUP BY 1) SELECT E''B\t'' || bucket || E''\t'' || row_count::text || E''\t'' || bucket_digest FROM bucket_summaries ORDER BY bucket; SELECT %L;',
  '__AUGR_TABLE_BEGIN__' || schema_name || '.' || table_name,
  schema_name,table_name,
  '__AUGR_TABLE_END__' || schema_name || '.' || table_name)
FROM baseline_tables ORDER BY schema_name,table_name \gexec
SELECT '__AUGR_SECTION_current_snapshots.json__';
SELECT jsonb_build_object(
  'risk_state',COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text) FROM risk_state t),'[]'::jsonb),
  'automation_job_controls',COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text) FROM automation_job_controls t),'[]'::jsonb),
  'pipeline_runs',COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text) FROM pipeline_runs t),'[]'::jsonb),
  'automation_job_runs',COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text) FROM automation_job_runs t),'[]'::jsonb),
  'agent_events',COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text) FROM agent_events t),'[]'::jsonb),
  'audit_log',COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text) FROM audit_log t),'[]'::jsonb)
);
SELECT '__AUGR_SECTION_pipeline_invariants.tsv__';
SELECT id::text || E'\t' || encode(digest((to_jsonb(t)-'status'-'signal'-'completed_at'-'error_message'-'phase_timings')::text,'sha256'),'hex') FROM pipeline_runs t ORDER BY id,trade_date;
SELECT '__AUGR_SECTION_automation_invariants.tsv__';
SELECT id::text || E'\t' || encode(digest((to_jsonb(t)-'status'-'completed_at'-'duration_ns'-'result'-'error'-'last_error_at'-'consecutive_failures')::text,'sha256'),'hex') FROM automation_job_runs t ORDER BY id;
COMMIT;
SQL
LC_ALL=C sort -o "$work_dir/table_fingerprints.tsv" "$work_dir/table_fingerprints.tsv"
cut -f1 "$work_dir/table_fingerprints.tsv" >"$work_dir/table_list.txt"

cmp "$record_dir/schema_catalog.tsv" "$work_dir/schema_catalog.tsv"
cmp "$record_dir/table_list.txt" "$work_dir/table_list.txt"
cmp "$record_dir/table_fingerprints.tsv" "$work_dir/table_fingerprints.tsv"
cmp "$record_dir/pipeline_invariants.tsv" "$work_dir/pipeline_invariants.tsv"
cmp "$record_dir/automation_invariants.tsv" "$work_dir/automation_invariants.tsv"

python3 - "$record_dir/protected_snapshots.json" "$work_dir/current_snapshots.json" <<'PY'
import json, sys

with open(sys.argv[1], encoding="utf-8") as handle:
    before = json.load(handle)
with open(sys.argv[2], encoding="utf-8") as handle:
    after = json.load(handle)

def fail(message):
    raise SystemExit(message)

if len(before["risk_state"]) != len(after["risk_state"]):
    fail("risk_state row set changed")
for old, new in zip(before["risk_state"], after["risk_state"]):
    for key in old.keys() - {"kill_switch", "updated_at"}:
        if old[key] != new.get(key):
            fail(f"risk_state changed outside approved fields: {key}")
    if not new.get("kill_switch", {}).get("active") or new.get("kill_switch", {}).get("reason") != "canonical cutover drain":
        fail("risk_state kill switch does not record canonical cutover drain")

old_controls = {row["job_name"]: row for row in before["automation_job_controls"]}
new_controls = {row["job_name"]: row for row in after["automation_job_controls"]}
if not old_controls.keys() <= new_controls.keys():
    fail("pre-existing automation control was deleted")
for name, old in old_controls.items():
    new = new_controls[name]
    for key in old.keys() - {"enabled", "updated_by", "updated_at"}:
        if old[key] != new.get(key):
            fail(f"automation control {name} changed outside approved fields")
    if new.get("enabled") is not False:
        fail(f"automation control {name} is still enabled")
for name in new_controls.keys() - old_controls.keys():
    new = new_controls[name]
    if new.get("enabled") is not False or new.get("updated_by") != "canonical-cutover-operator" or not new.get("updated_at"):
        fail(f"new automation control {name} is not an approved disabled materialization")

pipeline = {(row["id"], row["trade_date"]): row for row in after["pipeline_runs"]}
for old in before["nonrunning_pipeline_runs"]:
    if pipeline.get((old["id"], old["trade_date"])) != old:
        fail("pre-existing non-running pipeline row changed")
for old in before["running_pipeline_runs"]:
    current = pipeline.get((old["id"], old["trade_date"]))
    if current is None or current.get("status") not in {"completed", "failed", "cancelled"}:
        fail("captured running pipeline did not become terminal")
automation = {row["id"]: row for row in after["automation_job_runs"]}
for old in before["nonrunning_automation_job_runs"]:
    if automation.get(old["id"]) != old:
        fail("pre-existing non-running automation row changed")
for old in before["running_automation_job_runs"]:
    current = automation.get(old["id"])
    if current is None or current.get("status") not in {"ok", "degraded", "error", "cancelled"}:
        fail("captured running automation did not become terminal")

old_events = {json.dumps(row, sort_keys=True) for row in before["agent_events"]}
new_events = {json.dumps(row, sort_keys=True) for row in after["agent_events"]}
if not old_events <= new_events:
    fail("pre-existing agent event was deleted or changed")
captured_run_ids = {row["id"] for row in before["running_pipeline_runs"]}
for row in after["agent_events"]:
    if json.dumps(row, sort_keys=True) in old_events:
        continue
    if row.get("pipeline_run_id") not in captured_run_ids or row.get("event_kind") not in {"pipeline_completed", "pipeline_failed", "pipeline_cancelled"}:
        fail("unapproved agent event appeared during drain")

old_audit = {json.dumps(row, sort_keys=True) for row in before["audit_log"]}
new_audit = {json.dumps(row, sort_keys=True) for row in after["audit_log"]}
if not old_audit <= new_audit:
    fail("pre-existing audit row was deleted or changed")
for row in after["audit_log"]:
    if json.dumps(row, sort_keys=True) in old_audit:
        continue
    if row.get("event_type") != "kill_switch.activated" or row.get("entity_type") != "system" or (row.get("details") or {}).get("reason") != "canonical cutover drain":
        fail("unapproved audit event appeared during drain")
PY

printf 'old database matches the approved drain-only delta\n'
