#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
COMPOSE_FILE="${ROOT_DIR}/docker-compose.prod.yml"
PROJECT_NAME="${VERIFY_PROJECT_NAME:-augr-prod-verify-$$}"
VERIFY_ROLLBACK_SCHEMA_VERSION="${VERIFY_ROLLBACK_SCHEMA_VERSION:-60}"
VERIFY_ROLLBACK_IMAGE="${VERIFY_ROLLBACK_IMAGE:-}"

case "$VERIFY_ROLLBACK_IMAGE" in
    *[!A-Za-z0-9._/@:-]*)
        echo "VERIFY_ROLLBACK_IMAGE contains unsupported characters" >&2
        exit 1
        ;;
esac

case "$PROJECT_NAME" in
    augr-prod-verify-*) ;;
    *)
        echo "VERIFY_PROJECT_NAME must start with augr-prod-verify-" >&2
        exit 1
        ;;
esac
VERIFY_WEB_IMAGE="${PROJECT_NAME}-web:latest"

if [ -n "$(docker ps -aq --filter "label=com.docker.compose.project=${PROJECT_NAME}")" ]; then
    echo "refusing to reuse existing Compose project ${PROJECT_NAME}" >&2
    exit 1
fi

VERIFY_DIR="$(mktemp -d /tmp/augr-prod-verify.XXXXXX)"
APP_ENV_FILE="${VERIFY_DIR}/app.env"
NETWORK_OVERRIDE_FILE="${VERIFY_DIR}/network-override.yml"
ROLLBACK_IMAGE_OVERRIDE_FILE="${VERIFY_DIR}/rollback-image-override.yml"

# Docker's automatic bridge address pools can be exhausted on shared hosts even
# when these short-lived networks are cleaned up correctly. Use small, explicit,
# caller-overridable subnets so the smoke stack does not depend on that allocator.
VERIFY_PUBLIC_SUBNET="${VERIFY_PUBLIC_SUBNET:-10.252.0.0/28}"
VERIFY_BACKEND_SUBNET="${VERIFY_BACKEND_SUBNET:-10.252.0.16/28}"
VERIFY_MONITORING_SUBNET="${VERIFY_MONITORING_SUBNET:-10.252.0.32/28}"
export PROJECT_NAME VERIFY_BACKEND_SUBNET VERIFY_MONITORING_SUBNET VERIFY_PUBLIC_SUBNET

cat >"$NETWORK_OVERRIDE_FILE" <<'EOF'
networks:
  public:
    ipam:
      config:
        - subnet: ${VERIFY_PUBLIC_SUBNET}
  backend:
    ipam:
      config:
        - subnet: ${VERIFY_BACKEND_SUBNET}
  monitoring:
    external: false
    name: ${PROJECT_NAME}_monitoring
    ipam:
      config:
        - subnet: ${VERIFY_MONITORING_SUBNET}
EOF

VERIFY_APP_PORT="${VERIFY_APP_PORT:-$(python3 - <<'PY'
import socket
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
)}"

POSTGRES_USER="augr_verify"
POSTGRES_DB="augr_verify"
POSTGRES_PASSWORD="$(python3 -c 'import secrets; print(secrets.token_hex(24))')"
SMOKE_JWT_SECRET="$(python3 -c 'import secrets; print(secrets.token_hex(32))')"
APP_BIND="127.0.0.1"
APP_PORT="$VERIFY_APP_PORT"
APP_ENV="smoke"

export APP_BIND APP_ENV APP_ENV_FILE APP_PORT POSTGRES_DB POSTGRES_PASSWORD POSTGRES_USER

compose() {
    compose_files=(-f "$COMPOSE_FILE" -f "$NETWORK_OVERRIDE_FILE")
    if [ -s "$ROLLBACK_IMAGE_OVERRIDE_FILE" ]; then
        compose_files+=(-f "$ROLLBACK_IMAGE_OVERRIDE_FILE")
    fi
    docker compose --project-name "$PROJECT_NAME" "${compose_files[@]}" "$@"
}

cleanup() {
    compose down --volumes --remove-orphans --rmi local >/dev/null 2>&1 || true
    docker image rm "$VERIFY_WEB_IMAGE" >/dev/null 2>&1 || true
    rm -rf "$VERIFY_DIR"
}
trap cleanup EXIT HUP INT TERM

cat >"$APP_ENV_FILE" <<EOF
APP_ENV=smoke
APP_HOST=0.0.0.0
APP_PORT=8080
JWT_SECRET=${SMOKE_JWT_SECRET}
DATABASE_URL=postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@postgres:5432/${POSTGRES_DB}?sslmode=disable
DATABASE_POOL_SIZE=10
DATABASE_SSL_MODE=disable
REDIS_URL=redis://redis:6379/0
PROJECTION_ACCOUNT_ID=00000000-0000-4000-8000-000000000064
LLM_DEFAULT_PROVIDER=ollama
LLM_DEEP_THINK_MODEL=smoke-deep
LLM_QUICK_THINK_MODEL=smoke-quick
LLM_TIMEOUT=30s
OLLAMA_BASE_URL=http://ollama.invalid/v1
OLLAMA_API_KEY=smoke-key
OLLAMA_MODEL=smoke-model
ALPHA_VANTAGE_API_KEY=smoke-key
ALPHA_VANTAGE_RATE_LIMIT_PER_MINUTE=5
FINNHUB_RATE_LIMIT_PER_MINUTE=60
RISK_MAX_POSITION_SIZE_PCT=0.10
RISK_MAX_DAILY_LOSS_PCT=0.02
RISK_MAX_DRAWDOWN_PCT=0.10
RISK_MAX_OPEN_POSITIONS=10
RISK_CIRCUIT_BREAKER_THRESHOLD=0.05
RISK_CIRCUIT_BREAKER_COOLDOWN=15m
ENABLE_SCHEDULER=false
ENABLE_REDIS_CACHE=false
ENABLE_AGENT_MEMORY=false
ENABLE_LIVE_TRADING=false
ALPACA_PAPER_MODE=true
BINANCE_PAPER_MODE=true
KALSHI_DRY_RUN=true
ENABLE_POLYMARKET_AUTOMATION=false
EOF

wait_for_postgres() {
    echo "Waiting for isolated PostgreSQL..."
    for _ in $(seq 1 30); do
        if compose exec -T postgres pg_isready -h postgres -U "$POSTGRES_USER" -d "$POSTGRES_DB" >/dev/null 2>&1; then
            return 0
        fi
        sleep 1
    done
    echo "PostgreSQL did not become ready" >&2
    exit 1
}

psql_db() {
    local database=$1
    shift
    compose exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$database" "$@"
}

schema_metadata_state() {
    psql_db "$POSTGRES_DB" -At -c \
        "SELECT count(*)::text || '|' || COALESCE(min(version)::text,'') || '|' || COALESCE(bool_or(dirty)::text,'') FROM schema_migrations"
}

require_schema_metadata() {
    local expected=$1 state count stored dirty
    state=$(schema_metadata_state)
    IFS='|' read -r count stored dirty <<<"$state"
    if [ "$count" != 1 ] || [ "$stored" != "$expected" ] || { [ "$dirty" != f ] && [ "$dirty" != false ]; }; then
        echo "isolated database metadata is not clean at version ${expected}: ${state}" >&2
        exit 1
    fi
}

initialize_schema_metadata() {
    psql_db "$POSTGRES_DB" <<'SQL'
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
}

apply_migrations() {
    local from_version=$1 to_version=$2 direction=up step=1 current migration_version next suffix prefix migration_file
    local -a up_matches

    require_schema_metadata "$from_version"
    if [ "$to_version" -lt "$from_version" ]; then
        direction=down
        step=-1
    fi

    current=$from_version
    while [ "$current" -ne "$to_version" ]; do
        if [ "$direction" = up ]; then
            migration_version=$((current + 1))
            next=$migration_version
            suffix=up
        else
            migration_version=$current
            next=$((current - 1))
            suffix=down
        fi
        printf -v prefix '%06d_' "$migration_version"
        mapfile -t up_matches < <(git -C "$ROOT_DIR" ls-files "migrations/${prefix}*.up.sql")
        if [ "${#up_matches[@]}" -ne 1 ]; then
            echo "expected exactly one tracked up migration for version ${migration_version}" >&2
            exit 1
        fi
        migration_file="${ROOT_DIR}/${up_matches[0]%.up.sql}.${suffix}.sql"
        if ! git -C "$ROOT_DIR" ls-files --error-unmatch "${migration_file#"$ROOT_DIR"/}" >/dev/null 2>&1; then
            echo "missing tracked ${suffix} migration for version ${migration_version}" >&2
            exit 1
        fi

        psql_db "$POSTGRES_DB" <<SQL
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

        if ! { printf 'SET ROLE augr_db_owner;\n'; sed -e '/^BEGIN;$/d' -e '/^COMMIT;$/d' "$migration_file"; } | \
            psql_db "$POSTGRES_DB" --single-transaction; then
            echo "migration ${migration_file} failed; isolated database remains dirty at version ${current}" >&2
            exit 1
        fi

        psql_db "$POSTGRES_DB" <<SQL
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
}

wait_for_app_health() {
    echo "Waiting for isolated app health..."
    for _ in $(seq 1 60); do
        response=$(curl -fsS "http://127.0.0.1:${VERIFY_APP_PORT}/healthz" 2>/dev/null || true)
        if python3 -c '
import json, sys
body = json.loads(sys.argv[1])
sys.exit(0 if body.get("status") == "ok" and body.get("db") == "ok" and body.get("redis") == "ok" else 1)
' "$response" 2>/dev/null; then
            return 0
        fi
        sleep 1
    done
    echo "App did not become healthy" >&2
    compose logs --no-color app >&2 || true
    exit 1
}

echo "=== Building production image for ${PROJECT_NAME} ==="
VERIFY_BUILD_VERSION="$(git -C "$ROOT_DIR" describe --tags --always --dirty)"
VERIFY_BUILD_COMMIT="$(git -C "$ROOT_DIR" rev-parse HEAD)"
VERIFY_BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
BUILD_VERSION="$VERIFY_BUILD_VERSION" \
BUILD_COMMIT="$VERIFY_BUILD_COMMIT" \
BUILD_TIME="$VERIFY_BUILD_TIME" \
compose build app

BUILT_APP_IMAGE_ID=$(docker image inspect --format '{{.Id}}' "${PROJECT_NAME}-app:latest" 2>/dev/null || true)
if [ -z "$BUILT_APP_IMAGE_ID" ]; then
    echo "could not resolve built app image" >&2
    exit 1
fi
BUILT_APP_REVISION=$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}' "$BUILT_APP_IMAGE_ID")
BUILT_APP_VERSION=$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.version" }}' "$BUILT_APP_IMAGE_ID")
BUILT_APP_CREATED=$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.created" }}' "$BUILT_APP_IMAGE_ID")
if [ "$BUILT_APP_REVISION" != "$VERIFY_BUILD_COMMIT" ]; then
    echo "built app revision label mismatch: got ${BUILT_APP_REVISION}, expected ${VERIFY_BUILD_COMMIT}" >&2
    exit 1
fi
if [ "$BUILT_APP_VERSION" != "$VERIFY_BUILD_VERSION" ]; then
    echo "built app version label mismatch: got ${BUILT_APP_VERSION}, expected ${VERIFY_BUILD_VERSION}" >&2
    exit 1
fi
if [ "$BUILT_APP_CREATED" != "$VERIFY_BUILD_TIME" ]; then
    echo "built app creation label mismatch: got ${BUILT_APP_CREATED}, expected ${VERIFY_BUILD_TIME}" >&2
    exit 1
fi

docker buildx build --load \
    --tag "$VERIFY_WEB_IMAGE" \
    --build-arg "BUILD_VERSION=$VERIFY_BUILD_VERSION" \
    --build-arg "BUILD_COMMIT=$VERIFY_BUILD_COMMIT" \
    --build-arg "BUILD_TIME=$VERIFY_BUILD_TIME" \
    --file "${ROOT_DIR}/Dockerfile.web" \
    "$ROOT_DIR"
BUILT_WEB_REVISION=$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}' "$VERIFY_WEB_IMAGE")
BUILT_WEB_VERSION=$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.version" }}' "$VERIFY_WEB_IMAGE")
BUILT_WEB_CREATED=$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.created" }}' "$VERIFY_WEB_IMAGE")
if [ "$BUILT_WEB_REVISION" != "$VERIFY_BUILD_COMMIT" ]; then
    echo "built web revision label mismatch: got ${BUILT_WEB_REVISION}, expected ${VERIFY_BUILD_COMMIT}" >&2
    exit 1
fi
if [ "$BUILT_WEB_VERSION" != "$VERIFY_BUILD_VERSION" ]; then
    echo "built web version label mismatch: got ${BUILT_WEB_VERSION}, expected ${VERIFY_BUILD_VERSION}" >&2
    exit 1
fi
if [ "$BUILT_WEB_CREATED" != "$VERIFY_BUILD_TIME" ]; then
    echo "built web creation label mismatch: got ${BUILT_WEB_CREATED}, expected ${VERIFY_BUILD_TIME}" >&2
    exit 1
fi

echo "=== Starting isolated dependencies ==="
compose up -d postgres redis
wait_for_postgres

echo "=== Preparing isolated database owner roles ==="
psql_db postgres <<SQL
CREATE ROLE augr_db_owner LOGIN;
CREATE ROLE augr_app_runtime NOLOGIN;
CREATE ROLE augr_projection_writer NOLOGIN;
GRANT augr_db_owner TO "$POSTGRES_USER";
ALTER DATABASE "$POSTGRES_DB" OWNER TO augr_db_owner;
SQL

echo "=== Applying ordered tracked migrations through isolated PostgreSQL ==="
psql_db "$POSTGRES_DB" <<'SQL'
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS timescaledb;
SQL
initialize_schema_metadata
EXPECTED_VERSION=$(find "${ROOT_DIR}/migrations" -maxdepth 1 -type f -name '*.up.sql' -printf '%f\n' | sort -V | tail -1 | cut -d_ -f1 | sed 's/^0*//')
apply_migrations 0 "$EXPECTED_VERSION"

echo "=== Verifying schema version ==="
SCHEMA_VERSION=$(compose exec -T postgres psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc \
    "SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1" | tr -d '[:space:]')
if [ "$SCHEMA_VERSION" != "$EXPECTED_VERSION" ]; then
    echo "schema version mismatch after migrations: got ${SCHEMA_VERSION}, expected ${EXPECTED_VERSION}" >&2
    exit 1
fi
case "$VERIFY_ROLLBACK_SCHEMA_VERSION" in
    ''|*[!0-9]*)
        echo "VERIFY_ROLLBACK_SCHEMA_VERSION must be a non-negative integer" >&2
        exit 1
        ;;
esac
if [ "$VERIFY_ROLLBACK_SCHEMA_VERSION" -ge "$EXPECTED_VERSION" ]; then
    echo "VERIFY_ROLLBACK_SCHEMA_VERSION must be lower than ${EXPECTED_VERSION}" >&2
    exit 1
fi
echo "=== Starting isolated production app ==="
compose up -d app
wait_for_app_health

AUTH_TOKEN=$(JWT_SECRET="$SMOKE_JWT_SECRET" python3 - <<'PY'
import base64, hashlib, hmac, json, os, time
encode = lambda value: base64.urlsafe_b64encode(value).rstrip(b"=")
now = int(time.time())
header = encode(json.dumps({"alg": "HS256", "typ": "JWT"}, separators=(",", ":")).encode())
payload = encode(json.dumps({"sub": "production-smoke", "iat": now, "exp": now + 300, "token_type": "access"}, separators=(",", ":")).encode())
unsigned = header + b"." + payload
signature = encode(hmac.new(os.environ["JWT_SECRET"].encode(), unsigned, hashlib.sha256).digest())
print((unsigned + b"." + signature).decode())
PY
)

echo "=== Smoke-testing authenticated read-only API ==="
curl -fsS \
    -H "Authorization: Bearer ${AUTH_TOKEN}" \
    "http://127.0.0.1:${VERIFY_APP_PORT}/api/v1/strategies" | \
    python3 -c 'import json, sys; json.load(sys.stdin)'

echo "=== Rehearsing lossless schema rollback ==="
compose stop app >/dev/null
NEW_STRUCTURE_WRITES=$(compose exec -T postgres psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc \
    "SELECT (SELECT count(*) FROM automation_job_controls)::text || '|' || (SELECT count(*) FROM trades WHERE exit_reason IS NOT NULL)::text" | tr -d '[:space:]')
if [ "$NEW_STRUCTURE_WRITES" != "0|0" ]; then
    echo "refusing rollback rehearsal with writes in schema 61/62 structures: ${NEW_STRUCTURE_WRITES}" >&2
    exit 1
fi

apply_migrations "$EXPECTED_VERSION" "$VERIFY_ROLLBACK_SCHEMA_VERSION"

ROLLBACK_SCHEMA_VERSION=$(compose exec -T postgres psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc \
    "SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1" | tr -d '[:space:]')
if [ "$ROLLBACK_SCHEMA_VERSION" != "$VERIFY_ROLLBACK_SCHEMA_VERSION" ]; then
    echo "schema rollback mismatch: got ${ROLLBACK_SCHEMA_VERSION}, expected ${VERIFY_ROLLBACK_SCHEMA_VERSION}" >&2
    exit 1
fi

echo "=== Verifying schema-${VERIFY_ROLLBACK_SCHEMA_VERSION} predeployment backup and restore ==="
BACKUP_FILE="${VERIFY_DIR}/predeploy-schema-${VERIFY_ROLLBACK_SCHEMA_VERSION}.dump"
RESTORE_DB="${POSTGRES_DB}_restore"
compose exec -T postgres pg_dump \
    -U "$POSTGRES_USER" \
    -d "$POSTGRES_DB" \
    --format=custom \
    --no-owner >"$BACKUP_FILE"
if [ ! -s "$BACKUP_FILE" ]; then
    echo "isolated predeployment backup is empty" >&2
    exit 1
fi
compose exec -T postgres createdb -U "$POSTGRES_USER" "$RESTORE_DB"
compose exec -T postgres pg_restore \
    -U "$POSTGRES_USER" \
    -d "$RESTORE_DB" \
    --clean \
    --if-exists \
    --single-transaction \
    --exit-on-error \
    --no-owner <"$BACKUP_FILE"
RESTORED_SCHEMA_VERSION=$(compose exec -T postgres psql -U "$POSTGRES_USER" -d "$RESTORE_DB" -tAc \
    "SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1" | tr -d '[:space:]')
if [ "$RESTORED_SCHEMA_VERSION" != "$VERIFY_ROLLBACK_SCHEMA_VERSION" ]; then
    echo "restored backup schema mismatch: got ${RESTORED_SCHEMA_VERSION}, expected ${VERIFY_ROLLBACK_SCHEMA_VERSION}" >&2
    exit 1
fi
compose exec -T postgres dropdb -U "$POSTGRES_USER" "$RESTORE_DB"

if [ -n "$VERIFY_ROLLBACK_IMAGE" ]; then
    echo "=== Verifying exact rollback image with scheduler disabled ==="
    cat >"$ROLLBACK_IMAGE_OVERRIDE_FILE" <<EOF
services:
  app:
    image: ${VERIFY_ROLLBACK_IMAGE}
EOF
    compose up -d --no-build app
    wait_for_app_health
    rollback_container=$(compose ps -q app)
    actual_rollback_image=$(docker inspect -f '{{.Config.Image}}' "$rollback_container")
    if [ "$actual_rollback_image" != "$VERIFY_ROLLBACK_IMAGE" ]; then
        echo "rollback image mismatch: got ${actual_rollback_image}, expected ${VERIFY_ROLLBACK_IMAGE}" >&2
        exit 1
    fi
    actual_rollback_image_id=$(docker inspect -f '{{.Image}}' "$rollback_container")
    expected_rollback_image_id=$(docker image inspect --format '{{.Id}}' "$VERIFY_ROLLBACK_IMAGE")
    if [ "$actual_rollback_image_id" != "$expected_rollback_image_id" ]; then
        echo "rollback image content mismatch: got ${actual_rollback_image_id}, expected ${expected_rollback_image_id}" >&2
        exit 1
    fi
    rollback_automation_code=$(curl -sS -o /dev/null -w '%{http_code}' \
        -H "Authorization: Bearer ${AUTH_TOKEN}" \
        "http://127.0.0.1:${VERIFY_APP_PORT}/api/v1/automation/status")
    if [ "$rollback_automation_code" != "503" ]; then
        echo "rollback scheduler check returned HTTP ${rollback_automation_code}, expected 503" >&2
        exit 1
    fi

    echo "=== Proving rollback image drains admitted work on SIGTERM ==="
    compose stop app >/dev/null
    cat >"$ROLLBACK_IMAGE_OVERRIDE_FILE" <<EOF
services:
  app:
    image: ${VERIFY_ROLLBACK_IMAGE}
    environment:
      ENABLE_SCHEDULER: "true"
EOF
    compose up -d --no-build --force-recreate app
    wait_for_app_health
    rollback_container=$(compose ps -q app)
    if [ "$(docker inspect -f '{{.Image}}' "$rollback_container")" != "$expected_rollback_image_id" ]; then
        echo "rollback shutdown rehearsal did not use the exact rollback image content" >&2
        exit 1
    fi
    rollback_strategy=$(curl -fsS -X POST \
        -H "Authorization: Bearer ${AUTH_TOKEN}" \
        -H 'Content-Type: application/json' \
        --data '{"name":"rollback-shutdown-smoke","ticker":"SMOKE","market_type":"stock","is_paper":true,"schedule_cron":"","config":{"risk_config":{"position_size_pct":0.5}}}' \
        "http://127.0.0.1:${VERIFY_APP_PORT}/api/v1/strategies")
    rollback_strategy_id=$(python3 -c 'import json,sys; value=json.load(sys.stdin).get("id", ""); assert value; print(value)' <<<"$rollback_strategy")
    rollback_run_code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST \
        -H "Authorization: Bearer ${AUTH_TOKEN}" \
        "http://127.0.0.1:${VERIFY_APP_PORT}/api/v1/strategies/${rollback_strategy_id}/run")
    if [ "$rollback_run_code" = 404 ]; then
        rollback_run_code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST \
            -H "Authorization: Bearer ${AUTH_TOKEN}" \
            "http://127.0.0.1:${VERIFY_APP_PORT}/api/v1/accounts/00000000-0000-4000-8000-000000000064/strategies/${rollback_strategy_id}/run")
    fi
    if [ "$rollback_run_code" != 202 ]; then
        echo "rollback pipeline admission returned HTTP ${rollback_run_code}, expected 202" >&2
        exit 1
    fi
    rollback_job=$(curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" \
        "http://127.0.0.1:${VERIFY_APP_PORT}/api/v1/automation/status" | \
        python3 -c 'import json,sys; rows=json.load(sys.stdin); assert rows; print(rows[0]["name"])')
    rollback_job_code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST \
        -H "Authorization: Bearer ${AUTH_TOKEN}" \
        "http://127.0.0.1:${VERIFY_APP_PORT}/api/v1/automation/jobs/${rollback_job}/run")
    if [ "$rollback_job_code" != 200 ]; then
        echo "rollback automation admission returned HTTP ${rollback_job_code}, expected 200" >&2
        exit 1
    fi
    docker stop --time 300 "$rollback_container" >/dev/null
    ROLLBACK_RUNNING_WORK=$(psql_db "$POSTGRES_DB" -At -c \
        "SELECT (SELECT count(*) FROM pipeline_runs WHERE status='running')::text || '|' || (SELECT count(*) FROM automation_job_runs WHERE status='running')::text")
    if [ "$ROLLBACK_RUNNING_WORK" != "0|0" ]; then
        echo "rollback image left running work after SIGTERM: ${ROLLBACK_RUNNING_WORK}" >&2
        exit 1
    fi
    rm -f "$ROLLBACK_IMAGE_OVERRIDE_FILE"
fi

apply_migrations "$VERIFY_ROLLBACK_SCHEMA_VERSION" "$EXPECTED_VERSION"

REAPPLIED_SCHEMA_VERSION=$(compose exec -T postgres psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc \
    "SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1" | tr -d '[:space:]')
if [ "$REAPPLIED_SCHEMA_VERSION" != "$EXPECTED_VERSION" ]; then
    echo "schema reapply mismatch: got ${REAPPLIED_SCHEMA_VERSION}, expected ${EXPECTED_VERSION}" >&2
    exit 1
fi

compose up -d app
wait_for_app_health

echo "=== Production build verification passed; isolated stack will be removed ==="
