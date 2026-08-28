# Canonical account production cutover

This runbook cuts Augr from the protected schema-107 `tradingagent` database to a fresh schema-109 database. It is an outage procedure. A recorded operator approval is required before the first production command. Live execution remains disabled throughout.

## Immutable boundaries

- Never migrate, canary, seed, or otherwise write to `tradingagent` except for the approved global kill-switch activation, disabling automation controls, and terminal writes from work that was already running when shutdown began.
- Run every database command through `docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER"` over the container-local socket.
- The new database starts at metadata version 0 and migrates through 109. Do not copy strategies or operational history.
- Use the exact clean Phase-C app and web image content IDs. Do not rebuild during the outage.
- Change exactly `POSTGRES_DB`, `APP_DATABASE_URL`, `DATABASE_URL`, and `KALSHI_PROJECTION_DATABASE_URL` with `scripts/update-db-targets.sh`.
- Roll back by restoring those four values with the immutable rollback artifact and `deploy/docker-compose.nuc.rollback.yml`; never migrate the old database.

## Phase-C evidence before approval

From a clean tree, record the commit and run:

```bash
git status --porcelain=v1 --untracked-files=all
bash scripts/update-db-targets_test.sh
bash scripts/apply-migrations-psql_test.sh
./scripts/verify-account-cutover.sh --schema-matrix
./scripts/verify-account-cutover.sh --writer-fixtures
./scripts/verify-account-cutover.sh --api-matrix
./scripts/release-gate.sh
git diff --check
```

Build both images from that exact commit with version `canonical-$(git rev-parse --short=12 HEAD)`. Record the commit, OCI revision/version/created labels, content IDs, and old app/web content IDs. Verify the rollback app image against schema 107 with `VERIFY_ROLLBACK_IMAGE` and `VERIFY_ROLLBACK_SCHEMA_VERSION=107` before the outage.

## Prepare immutable recovery evidence

After approval and before provisioning:

```bash
umask 077
install -d -m 0700 /var/lib/augr-cutover/canonical-20260827
./scripts/update-db-targets.sh --prepare-rollback --env-file .env
./scripts/update-db-targets.sh --validate --env-file .env
sha256sum .env.canonical-cutover.rollback .env.canonical-cutover.rollback.meta
```

Record both hashes. A second prepare attempt must fail. Do not modify or chmod either artifact.

## Provision the fresh database

Use `NEW_DB_NAME=tradingagent_canonical_20260827`. Prove it does not exist, create it with owner `augr_db_owner`, and run:

```bash
./scripts/apply-migrations-psql.sh --database "$NEW_DB_NAME" --from 0 --to 109
TARGET_DB_NAME="$NEW_DB_NAME" ./scripts/verify-account-cutover.sh --target-zero-history-audit
```

Apply the role grants from the reviewed implementation plan as `augr_db_owner`. Install the existing projection signing key without printing it. Verify that migration 108 seeded exactly the configured active `paper_scored` account and its matching profile; do not insert another account or profile. Re-run the zero-history audit and record table and sequence snapshots; it must still report zero strategies, operational rows, outbox rows, and checkpoints.

## Drain the old database

Capture the baseline before either safety write:

```bash
./scripts/capture-old-db-baseline.sh --database tradingagent --record-dir /var/lib/augr-cutover/canonical-20260827
```

Through the authenticated global APIs, activate the kill switch with the exact reason `canonical cutover drain` and disable every automation job. Stop app and web with the normal Compose stop path. Wait for all captured running pipeline and automation rows to become terminal, then require no runtime-role sessions and run:

```bash
./scripts/verify-old-db-after-drain.sh --database tradingagent --record-dir /var/lib/augr-cutover/canonical-20260827
```

Any unexpected row, digest, schema, ID, market-kill-switch, event, audit, or invariant change aborts the cutover.

## Atomically switch the four targets

Keep shell tracing off and open the three URLs from protected variables:

```bash
set +x
umask 077
exec 3<<<"$NEW_APP_DATABASE_URL"
exec 4<<<"$NEW_DATABASE_URL"
exec 5<<<"$NEW_PROJECTION_DATABASE_URL"
./scripts/update-db-targets.sh --env-file .env --postgres-db "$NEW_DB_NAME" \
  --app-database-url-fd 3 --database-url-fd 4 --kalshi-projection-database-url-fd 5
exec 3<&- 4<&- 5<&-
./scripts/update-db-targets.sh --validate --env-file .env
```

Require `.env` mode 600, the exact new database in all three URLs, distinct general and projection users, and unchanged rollback-artifact hashes.

## Restart and qualify

Start the exact Phase-C app and web images with `deploy/docker-compose.nuc.scheduler-paused.yml`. Require scheduler disabled, live trading disabled, health, schema 109, the configured account, and no old-database runtime sessions. Run one explicitly approved paper canary at a time. Capture its exact `(run_id, trade_date)` and run:

```bash
TARGET_DB_NAME="$NEW_DB_NAME" \
TARGET_ACCOUNT_ID="$PROJECTION_ACCOUNT_ID" \
TARGET_RUN_ID="$CANARY_RUN_ID" \
TARGET_RUN_TRADE_DATE="$CANARY_RUN_TRADE_DATE" \
./scripts/verify-account-cutover.sh --target-graph-audit
```

Do not enable the scheduler until each provider-backed paper writer has terminal evidence, balanced reachable ledger state, completed or explicit degraded outbox work, and valid signed projection evidence where completion is claimed. Keep live execution disabled. Do not clear the old database's kill switch or automation controls.

## Rollback

Stop app and web. Do not down-migrate or otherwise modify either database. Restore the original target file and restart the recorded old images with the rollback override:

```bash
./scripts/update-db-targets.sh --restore --env-file .env
./scripts/update-db-targets.sh --validate --env-file .env
docker compose --env-file .env -f docker-compose.nuc.yml -f deploy/docker-compose.nuc.rollback.yml up -d --no-build app web
```

Verify the restored `.env` equals the protected rollback artifact, `.canonical-cutover.pre-restore` preserves the failed target configuration, the old image content IDs are exact, the old database remains schema 107, and safety controls remain active. Preserve all cutover evidence for incident review.
