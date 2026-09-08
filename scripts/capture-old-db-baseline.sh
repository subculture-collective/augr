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
umask 077
install -d -m 0700 "$record_dir"
[[ ! -e $record_dir/manifest.sha256 ]] || {
  printf 'baseline manifest already exists\n' >&2
  exit 1
}

psql_old() {
  docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres \
    psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$database" "$@"
}

psql_old -qAt <<'SQL' | python3 scripts/parse-old-db-snapshot.py "$record_dir"
BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
SELECT '__AUGR_SECTION_schema_catalog.tsv__';
SELECT version::text || E'\t' || dirty::text FROM schema_migrations;
SELECT n.nspname || '.' || c.relname || E'\t' ||
       string_agg(a.attname || ':' || pg_catalog.format_type(a.atttypid,a.atttypmod), ',' ORDER BY a.attnum)
FROM pg_class c
JOIN pg_namespace n ON n.oid=c.relnamespace
JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
WHERE n.nspname='public' AND c.relkind IN ('r','p')
GROUP BY n.nspname,c.relname
ORDER BY n.nspname,c.relname;
SELECT '__AUGR_SECTION_table_fingerprints.tsv__';
WITH baseline_tables AS (
  SELECT n.nspname AS schema_name,c.relname AS table_name
  FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE n.nspname='public' AND c.relkind IN ('r','p')
    AND NOT EXISTS (SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid)
    AND c.relname NOT IN ('risk_state','automation_job_controls','pipeline_runs','automation_job_runs','agent_events','audit_log')
)
SELECT format(
  'SELECT %L; WITH row_hashes AS MATERIALIZED (SELECT digest(to_jsonb(t)::text,''sha256'') AS row_hash FROM %I.%I t), bucket_summaries AS (SELECT encode(substring(row_hash FROM 1 FOR 1),''hex'') AS bucket,count(*) AS row_count,encode(digest(string_agg(encode(row_hash,''hex''),'''' ORDER BY row_hash),''sha256''),''hex'') AS bucket_digest FROM row_hashes GROUP BY 1) SELECT E''B\t'' || bucket || E''\t'' || row_count::text || E''\t'' || bucket_digest FROM bucket_summaries ORDER BY bucket; SELECT %L;',
  '__AUGR_TABLE_BEGIN__' || schema_name || '.' || table_name,
  schema_name,table_name,
  '__AUGR_TABLE_END__' || schema_name || '.' || table_name)
FROM baseline_tables ORDER BY schema_name,table_name \gexec
SELECT '__AUGR_SECTION_protected_snapshots.json__';
SELECT jsonb_build_object(
  'risk_state',COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text) FROM risk_state t),'[]'::jsonb),
  'automation_job_controls',COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text) FROM automation_job_controls t),'[]'::jsonb),
  'running_pipeline_runs',COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text) FROM pipeline_runs t WHERE status='running'),'[]'::jsonb),
  'nonrunning_pipeline_runs',COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text) FROM pipeline_runs t WHERE status<>'running'),'[]'::jsonb),
  'running_automation_job_runs',COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text) FROM automation_job_runs t WHERE status='running'),'[]'::jsonb),
  'nonrunning_automation_job_runs',COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text) FROM automation_job_runs t WHERE status<>'running'),'[]'::jsonb),
  'agent_events',COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text) FROM agent_events t),'[]'::jsonb),
  'audit_log',COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text) FROM audit_log t),'[]'::jsonb)
);
SELECT '__AUGR_SECTION_pipeline_invariants.tsv__';
SELECT id::text || E'\t' || encode(digest((to_jsonb(t)-'status'-'signal'-'completed_at'-'error_message'-'phase_timings')::text,'sha256'),'hex')
FROM pipeline_runs t ORDER BY id,trade_date;
SELECT '__AUGR_SECTION_automation_invariants.tsv__';
SELECT id::text || E'\t' || encode(digest((to_jsonb(t)-'status'-'completed_at'-'duration_ns'-'result'-'error'-'last_error_at'-'consecutive_failures')::text,'sha256'),'hex')
FROM automation_job_runs t ORDER BY id;
SELECT '__AUGR_SECTION_nonrunning_pipeline_digest.txt__';
SELECT encode(digest(COALESCE(string_agg(to_jsonb(t)::text,E'\n' ORDER BY to_jsonb(t)::text),''),'sha256'),'hex')
FROM pipeline_runs t WHERE status<>'running';
COMMIT;
SQL
LC_ALL=C sort -o "$record_dir/table_fingerprints.tsv" "$record_dir/table_fingerprints.tsv"
cut -f1 "$record_dir/table_fingerprints.tsv" >"$record_dir/table_list.txt"

files=(schema_catalog.tsv table_fingerprints.tsv table_list.txt protected_snapshots.json pipeline_invariants.tsv automation_invariants.tsv nonrunning_pipeline_digest.txt)
(
  cd "$record_dir"
  sha256sum "${files[@]}" >manifest.sha256
)
for file in "${files[@]}" manifest.sha256; do
  chmod 0600 "$record_dir/$file"
done
printf 'old database baseline captured in %s\n' "$record_dir"
