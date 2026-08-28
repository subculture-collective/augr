#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf 'usage: %s --database NAME --from VERSION --to VERSION\n' "$0" >&2
  exit 2
}

[[ $# -eq 6 ]] || usage
[[ $1 == --database && $3 == --from && $5 == --to ]] || usage
database=$2
from_version=$4
to_version=$6
[[ $database =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || usage
[[ $database != tradingagent ]] || { printf 'refusing protected database tradingagent\n' >&2; exit 2; }
[[ $from_version =~ ^[0-9]+$ && $to_version =~ ^[0-9]+$ ]] || usage
: "${POSTGRES_USER:?set POSTGRES_USER}"

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"
exec 9>".git/augr-migration-runner.lock"
if ! flock -n 9; then
  printf 'another Augr migration runner holds %s\n' "$repo_root/.git/augr-migration-runner.lock" >&2
  exit 1
fi

compose=(docker compose --env-file .env -f docker-compose.nuc.yml)
psql_base=(psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$database")

run_sql() {
  "${compose[@]}" exec -T postgres "${psql_base[@]}" "$@"
}

metadata_state() {
  run_sql -At -c "SELECT count(*)::text || '|' || COALESCE(min(version)::text,'') || '|' || COALESCE(bool_or(dirty)::text,'') FROM schema_migrations"
}

check_existing_metadata() {
  local expected=$1 state count stored dirty
  state=$(metadata_state)
  IFS='|' read -r count stored dirty <<<"$state"
  if [[ $count != 1 ]]; then
    printf 'database %s must contain exactly one schema_migrations row\n' "$database" >&2
    exit 1
  fi
  if [[ $dirty == t || $dirty == true ]]; then
    printf 'database %s is dirty at version %s; restore the disposable database or fresh target from its pre-migration backup\n' "$database" "$stored" >&2
    exit 1
  fi
  if [[ $stored != "$expected" ]]; then
    printf 'database %s is at version %s, not requested --from %s\n' "$database" "$stored" "$expected" >&2
    exit 1
  fi
}

if (( from_version == 0 )); then
  # PostgreSQL requires the administrative connection to install the untrusted
  # vector and TimescaleDB extensions. Install the exact extension set declared
  # by tracked migrations before handing every migration body to the non-login
  # database-owner role. CREATE EXTENSION IF NOT EXISTS remains idempotent when
  # a disposable database is retried before metadata initialization.
  run_sql <<'SQL'
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS timescaledb;
SQL
  run_sql <<'SQL'
BEGIN;
SELECT pg_advisory_xact_lock(hashtextextended(current_database() || ':schema_migrations',0));
CREATE TABLE IF NOT EXISTS schema_migrations(version BIGINT NOT NULL, dirty BOOLEAN NOT NULL);
LOCK TABLE schema_migrations IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
  IF (SELECT count(*) FROM schema_migrations)<>0 THEN
    RAISE EXCEPTION 'schema_migrations must be empty for initialization';
  END IF;
END $$;
INSERT INTO schema_migrations(version,dirty) VALUES(0,false);
COMMIT;
SQL
else
  check_existing_metadata "$from_version"
fi

if (( from_version == to_version )); then
  exit 0
fi

direction=up
step=1
if (( to_version < from_version )); then
  direction=down
  step=-1
fi

current=$from_version
while (( current != to_version )); do
  if [[ $direction == up ]]; then
    migration_version=$((current + 1))
    next=$migration_version
    suffix=up
  else
    migration_version=$current
    next=$((current - 1))
    suffix=down
  fi
  printf -v prefix '%06d_' "$migration_version"
  mapfile -t up_matches < <(git ls-files "migrations/${prefix}*.up.sql")
  if (( ${#up_matches[@]} != 1 )); then
    printf 'expected exactly one tracked up migration for version %d, found %d\n' "$migration_version" "${#up_matches[@]}" >&2
    exit 1
  fi
  migration_file=${up_matches[0]%.up.sql}.${suffix}.sql
  if ! git ls-files --error-unmatch "$migration_file" >/dev/null 2>&1; then
    printf 'missing tracked %s migration for version %d: %s\n' "$suffix" "$migration_version" "$migration_file" >&2
    exit 1
  fi

  run_sql <<SQL
BEGIN;
SELECT pg_advisory_xact_lock(hashtextextended(current_database() || ':schema_migrations',0));
LOCK TABLE schema_migrations IN ACCESS EXCLUSIVE MODE;
DO \$\$ BEGIN
  IF (SELECT count(*) FROM schema_migrations)<>1 OR
     NOT EXISTS(SELECT 1 FROM schema_migrations WHERE version=${current} AND dirty=false) THEN
    RAISE EXCEPTION 'schema metadata changed before migration ${migration_version}';
  END IF;
END \$\$;
UPDATE schema_migrations SET dirty=true;
COMMIT;
SQL

  if ! { printf 'SET ROLE augr_db_owner;\n'; sed -n '1,$p' "$migration_file"; } | run_sql --single-transaction; then
    printf 'migration %s failed; database %s remains dirty at version %d\n' "$migration_file" "$database" "$current" >&2
    exit 1
  fi

  run_sql <<SQL
BEGIN;
SELECT pg_advisory_xact_lock(hashtextextended(current_database() || ':schema_migrations',0));
LOCK TABLE schema_migrations IN ACCESS EXCLUSIVE MODE;
DO \$\$ BEGIN
  IF (SELECT count(*) FROM schema_migrations)<>1 OR
     NOT EXISTS(SELECT 1 FROM schema_migrations WHERE version=${current} AND dirty=true) THEN
    RAISE EXCEPTION 'schema metadata changed while applying migration ${migration_version}';
  END IF;
END \$\$;
UPDATE schema_migrations SET version=${next},dirty=false;
COMMIT;
SQL
  current=$((current + step))
done
