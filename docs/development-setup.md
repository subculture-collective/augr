---
title: "Development Setup"
description: "Complete local development workflow for backend, frontend, database, testing, and smoke-mode execution."
status: "canonical"
updated: "2026-08-15"
tags: [development, setup, local-dev]
---

# Development Setup

This guide is for contributors who need the full day-to-day workflow rather than the shortest first-run path.

## Toolchain

Required:

- Go 1.25.13 (pinned in `go.mod` and `mise.toml`)
- Node.js 22 (pinned in `.nvmrc` and `mise.toml`, and used by frontend CI)
- npm
- Python 3 (standard library only, for the integration gate)
- Docker and Docker Compose v2+
- PostgreSQL client tools if you want to inspect the database outside Compose

Recommended:

- [Task](https://taskfile.dev) for the project command runner
- `jq` for API and login scripting
- the Go quality/migration tools installed by `task tools`: `gofumpt`,
  `golangci-lint`, `govulncheck`, and `migrate`

## Repository layout

These are the directories you will touch most often:

| Path | Purpose |
| --- | --- |
| `cmd/tradingagent` | app bootstrap, runtime wiring, strategy runner, docs tests |
| `internal/api` | REST API, middleware, auth, settings, WebSocket hub |
| `internal/agent` | agent runtime, config resolution, prompts, runner orchestration |
| `internal/data` | provider chains, caching, historical downloads |
| `internal/execution` | brokers, paper trading, order management |
| `internal/risk` | hard risk engine, kill switch, exposure limits |
| `internal/repository/postgres` | persistence layer |
| `web/` | React/Vite frontend |
| `migrations/` | SQL migrations |
| `docs/` | canonical docs plus archive material |

## Configuration model

The server loads configuration from environment variables through `internal/config`.

Important behavior:

- `.env` is auto-loaded only when `APP_ENV=development`.
- `JWT_SECRET` is required for the API server to start.
- most provider integrations are opt-in by key presence
- non-secret settings edited through the API/UI persist to the `app_settings` table when the DB-backed persister is wired; secrets are not written back to `.env` or stored in the database
- startup fails fast on database schema mismatch before the rest of the runtime boots; fix by running migrations, then restarting the process

Start from:

```bash
cp .env.example .env
```

Then set the minimum viable local config:

```dotenv
APP_ENV=development
JWT_SECRET=replace-this-with-a-real-secret
OPENAI_API_KEY=...
```

## Running the stack with Docker Compose

The default contributor path is:

```bash
docker compose up --build
```

That Compose stack is backend-only in current local and production wiring. Run the frontend separately from `web/`.

Or with Task:

```bash
task dev
```

Useful Compose/Task commands:

```bash
task dev
task dev:down
task dev:logs
task dev:restart
task dev:psql
```

### Isolated Phase 1 and Phase 2 services

When another Augr Compose project is already using this checkout, keep Phase 1
and Phase 2 schema work on separate loopback-only ports and named volumes. The
built-in Docker bridge avoids allocating another custom subnet. The existing
`augr-phase1-*` container names are retained while Phase 2 uses the same
isolated development database; they do not refer to a shared or deployed
environment.

First-time creation:

```bash
docker volume create augr_phase1_postgres_data
docker volume create augr_phase1_redis_data

docker run -d \
  --name augr-phase1-postgres \
  --network bridge \
  --label com.subcult.augr.environment=phase1-local \
  -e POSTGRES_USER=postgres \
  -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=tradingagent \
  -p 127.0.0.1:55464:5432 \
  -v augr_phase1_postgres_data:/var/lib/postgresql/data \
  timescale/timescaledb-ha:pg17

docker run -d \
  --name augr-phase1-redis \
  --network bridge \
  --label com.subcult.augr.environment=phase1-local \
  -p 127.0.0.1:56380:6379 \
  -v augr_phase1_redis_data:/data \
  redis:7-alpine
```

Start or stop the existing environment without touching the default Augr
stack:

```bash
docker start augr-phase1-postgres augr-phase1-redis
docker stop augr-phase1-postgres augr-phase1-redis
```

Apply or inspect migrations explicitly:

```bash
export AUGR_PHASE1_DB_URL='postgres://postgres:postgres@127.0.0.1:55464/tradingagent?sslmode=disable'
migrate -path migrations -database "$AUGR_PHASE1_DB_URL" up
migrate -path migrations -database "$AUGR_PHASE1_DB_URL" version
docker exec augr-phase1-redis redis-cli ping
```

Schema 68 is the local economic-event adapter boundary. It adds append-only
raw source events, provenance-backed physical option terms, and typed
normalizations linked to exact balanced ledger aggregates. Adapter code must
call `RecordEconomicSourceEvent` and allow that transaction to commit before
calling `ApplyEconomicNormalization`; a failed or temporarily impossible
normalization must leave the original wire JSON and SHA-256 evidence durable.
The option-term and physical-normalization repositories serialize on the same
per-option database lock so a concurrent terms change cannot silently alter an
already-selected deliverable.

Migration 68 performs no legacy backfill and does not cut over the existing
order, trade, position, broker, expiration, or settlement paths. Its down
migration succeeds only while all three schema-68 tables and all
`economic_source_event` ledger origins remain empty. Run repository and
migration integration tests only against this disposable database:

```bash
DB_URL="$AUGR_PHASE1_DB_URL" go test -race -count=1 \
  -run '^TestLedgerRepo|^TestEconomicEventRepo|^TestOptionTermsRepo|^TestOptionTermsAndPhysical' \
  ./internal/repository/postgres
DB_URL="$AUGR_PHASE1_DB_URL" go test -race -count=1 \
  -run '^TestEconomicEventMigration' ./migrations
```

These credentials and ports are intentionally local-development-only. Do not
reuse them for a shared, staging, or production database.

## Running the backend natively

If you want the API server outside Docker:

1. Start PostgreSQL and Redis yourself, or run only those services via Compose.
2. Set `DATABASE_URL`, `REDIS_URL`, and `JWT_SECRET`.
3. Run migrations.
4. Start the server:

```bash
go run ./cmd/tradingagent serve
```

Or build first:

```bash
task build
./bin/tradingagent serve
```

## Running the frontend

```bash
# Use the version manager already available on this host. `mise exec` makes
# the selected version explicit even in shells without the activation hook:
mise install
mise exec -- npm --prefix web install
mise exec -- npm --prefix web run dev

# Or, with nvm:
nvm use
cd web
npm install
npm run dev
```

The frontend default API base URL is `http://localhost:8080`.

The frontend is a separate Vite app. Backend root `/` is not the SPA in the current Compose or production stack.

## Database migrations

The project uses SQL migrations under `migrations/`.

Run them explicitly before expecting a new build to boot cleanly against an updated database. If the server already started and failed with a schema mismatch, apply migrations and then restart it; the mismatch is fail-fast and does not self-heal inside the running process.

Common commands:

```bash
task migrate:up
task migrate:down
task migrate:status
task migrate:create -- add_feature_name
```

The schema includes persistence for:

- strategies
- pipeline runs and phase timings
- pipeline run snapshots
- agent decisions and events
- conversations and messages
- orders, positions, trades
- memories
- market-data cache and historical OHLCV
- audit log
- users
- API keys
- backtest configs and backtest runs
- explicit accounts and append-only capital flows
- immutable ledger transactions and balanced postings
- mark observations and projection checkpoints
- canonical instruments and immutable dated alias events
- venue contracts and corporate-action facts
- explicit instrument-identity quarantine findings
- canonical append-only quote snapshots and exact ordered depth levels
- immutable legacy-versus-ledger accounting reconciliation evidence

Schema 66 deliberately leaves existing ticker-based application reads in
place. It backfills legacy symbols as deterministic quarantined identities and
does not infer currency, tick size, lot size, multiplier, settlement, or
tradability. Inspect the local quarantine before using any canonical identity
in new work:

```sql
SELECT
    instrument.identity_key,
    instrument.asset_class,
    instrument.primary_venue,
    finding.finding_code,
    finding.source,
    finding.details
FROM instruments AS instrument
JOIN instrument_identity_quarantine AS finding
  ON finding.instrument_id = instrument.id
WHERE instrument.status = 'quarantined'
ORDER BY instrument.identity_key, finding.observed_at, finding.id;
```

Schema 67 adds the canonical market-observation boundary without cutting over
any provider, strategy, order, fill, cache, or recorder path. It intentionally
does not backfill legacy `DOUBLE PRECISION`/JSON snapshots: those rows do not
prove a canonical instrument, source namespace/revision, exact decimal input,
or decision-availability time. New adapters must provide that evidence
explicitly.

Inspect which observations are actually eligible for a point-in-time consumer:

```sql
SELECT
    quote.id,
    quote.ingest_sequence,
    instrument.identity_key,
    quote.provider,
    quote.venue,
    quote.observation_namespace,
    quote.observation_id,
    quote.source_revision,
    quote.exchange_at,
    quote.received_at,
    quote.available_at,
    quote.bid,
    quote.ask,
    quote.bid_depth_count,
    quote.ask_depth_count
FROM quote_snapshots AS quote
JOIN instruments AS instrument ON instrument.id = quote.instrument_id
WHERE quote.available_at IS NOT NULL
ORDER BY quote.available_at DESC, quote.source_sequence DESC NULLS LAST,
         quote.ingest_sequence DESC;
```

An observation with a missing `available_at`, source, venue contract, bid, ask,
market/session status, or requested depth side may still preserve attributable
evidence, but the `internal/marketdata` assessment contract fails closed when a
consumer requires that fact. Present zero prices remain distinct from SQL
`NULL`; missing spread is never treated as zero. `QuoteSnapshot.Assess` checks
fact sufficiency only. An intent, order, or fill route must call
`QuoteSnapshot.AssessForExecution` with the resolved immutable instrument and
venue contract; that joined boundary requires an active, unexpired instrument,
checks the contract at both observation and evaluation time, and rejects
off-tick executable prices or off-lot displayed sizes.

Schema 68 adds the raw-first economic-event boundary. Provider or operator
evidence is appended as exact JSON bytes, JSONB, and SHA-256 before a typed
normalizer may create a ledger transaction. Fill, cost, cash settlement,
expiration, physical option exercise/assignment, and prediction-payout
normalizations retain their source identity, immutable dated mechanics, and
execution provenance. This schema is additive: legacy order, trade, position,
settlement, and accounting paths are not cut over by applying it.

Schema 69 adds canonical instrument marks and deterministic full-rebuild FIFO
portfolio checkpoints. A rebuild uses only ledger transactions and marks whose
effective and observed timestamps are both available at its `as_of`, starts
from zero, and fails on an unknown transaction, missing/stale mark, inconsistent
mechanics, or a violated P&L equation. Checkpoints preserve the exact canonical
bytes and input/output checksums, but remain caches and evidence artifacts; the
immutable ledger is still the source of economic truth. Applying migration 69
does not schedule rebuilds or change any legacy API, UI, risk, order, trade, or
position read/write path. OVR-105 must prove dual-run parity before cutover.

Checkpoint persistence has an explicit database trust boundary. Migration 69
revokes public access to its byte-only persistence function and to immutable
versioned HMAC verification keys, and grants no role automatically. A
projection worker must receive the matching 32-byte signing secret from a
separate runtime secret provider and use a dedicated non-owner, non-superuser
database login. That login gets read access to replay inputs, append/select
access to canonical marks, select access to checkpoints, and only `EXECUTE`
access to the controlled function. It must not own the schema, read the HMAC
key tables, or have direct checkpoint `INSERT`, `UPDATE`, or `DELETE`.
`ProjectionRepo` checks these database capabilities and refuses to rebuild
under the current owner-style development/Compose database login. The database
writer credential alone can replay identical signed bytes but cannot sign an
altered projection. Do not grant the function to the general app role or use
the migration owner as a shortcut.

For a disposable local database, an administrator can create a narrowly scoped
example role after applying migration 69. Supply a fresh local-only password;
also generate a distinct cryptographically random 32-byte HMAC secret, store it
outside the repository, and provision the verifier copy through the database
owner. Do not copy this role, password, or HMAC secret into a shared environment
without a separate deployment review:

```sql
CREATE ROLE augr_projection_writer LOGIN PASSWORD '<fresh-local-password>'
    NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT;
GRANT CONNECT ON DATABASE tradingagent TO augr_projection_writer;
GRANT USAGE ON SCHEMA public TO augr_projection_writer;
GRANT SELECT ON
    accounts,
    ledger_transactions,
    ledger_postings,
    economic_event_normalizations,
    venue_contracts,
    option_contract_terms,
    instruments,
    mark_observations,
    projection_checkpoints
TO augr_projection_writer;
GRANT INSERT ON mark_observations TO augr_projection_writer;
GRANT EXECUTE ON FUNCTION persist_canonical_projection_checkpoint(BYTEA, TEXT, BYTEA)
TO augr_projection_writer;
REVOKE INSERT, UPDATE, DELETE ON projection_checkpoints
FROM augr_projection_writer;

INSERT INTO projection_checkpoint_signing_keys (
    key_id,
    signing_secret,
    created_by
) VALUES (
    'local-2026-08-v1',
    decode('<64-lowercase-hex-characters-from-a-random-32-byte-secret>', 'hex'),
    'local-developer'
);
```

Pass the same decoded secret and key ID to `NewProjectionRepo` through
`ProjectionCheckpointAttestor`; do not log either value or put the secret in a
tracked env file. The concrete Vault/cloud/native secret store and workload
identity for a shared environment remain an OVR-105 deployment decision.

Verify the effective privileges while connected as that login before using the
repository. The required result is `direct_insert = false`,
`signing_key_read = false`, and `controlled_write = true`:

```sql
SELECT
    has_table_privilege(
        current_user,
        'projection_checkpoints',
        'INSERT'
    ) AS direct_insert,
    has_table_privilege(
        current_user,
        'projection_checkpoint_signing_keys',
        'SELECT'
    ) AS signing_key_read,
    has_function_privilege(
        current_user,
        'persist_canonical_projection_checkpoint(bytea,text,bytea)',
        'EXECUTE'
    ) AS controlled_write;
```

Rotate without rewriting old checkpoints: append a new key row, switch the
worker's key ID/secret, verify new checkpoints, then append a revocation for the
old key. Revocation is itself immutable and causes every later persistence
attempt under that key to fail:

```sql
INSERT INTO projection_checkpoint_signing_key_revocations (
    key_id,
    reason,
    revoked_by
) VALUES (
    'local-2026-08-v1',
    'rotated after local verification',
    'local-developer'
);
```

Schema 70 adds the OVR-105 accounting dual-run evidence boundary. It stores
the exact legacy snapshot, immutable-ledger snapshot, comparison bytes,
SHA-256 checksums, deterministic identities, capture-fence identity/epoch,
classification rows, and opaque future attestation fields. Parent and result
rows are append-only; incomplete child sets fail at commit; downgrade takes
exclusive locks first and refuses to discard any recorded evidence.

Applying schema 70 does not make dual-run evidence authentic, start a worker,
grant a runtime role, or switch an accounting read. Database triggers establish
structural consistency only. A qualifying run additionally requires an
approved verifier for the exact bytes and named identities. The present code
has no shared runtime capture fence covering every paper mutation and ledger
normalization, and it has no approved reconciliation attestation/workload
identity. Therefore do not grant `INSERT` on
`accounting_reconciliation_runs` or `accounting_reconciliation_results`, do not
schedule `accountingrecon.Runner`, and do not interpret a manually inserted row
as parity evidence.

Use only the disposable loopback database for the current schema-70 tests:

```bash
export AUGR_PHASE1_DB_URL='postgres://postgres:postgres@127.0.0.1:55464/tradingagent?sslmode=disable'

go test -race -count=1 ./internal/accountingrecon ./internal/execution/paper
DB_URL="$AUGR_PHASE1_DB_URL" go test -race -count=1 \
  -run '^TestAccountingReconciliationRepo' ./internal/repository/postgres
DB_URL="$AUGR_PHASE1_DB_URL" go test -race -count=1 \
  -run '^TestAccountingDualRunMigration' ./migrations
```

The pure cutover evaluator requires an injected trusted wall clock and 30
consecutive fully completed UTC dates for one account under one unchanged
projection/mark/comparison policy; the current UTC date cannot count. Every
required fact and position must be exactly equal or carry an allowed independently reviewed
explanation; missing, unexplained, conflicting, future, synthetic, unsigned,
unknown-key, revoked-key, or invalid evidence fails closed. Passing that pure
evaluator still returns evidence only and cannot change a runtime read path.
See [Accounting read cutover](runbooks/accounting-read-cutover.md) before any
deployment design or operational work.

## Creating a local user

There is no self-service registration flow yet. For local dev:

```bash
docker compose exec postgres psql -U postgres -d tradingagent <<'SQL'
INSERT INTO users (username, password_hash)
VALUES ('demo', crypt('demo-pass', gen_salt('bf')))
ON CONFLICT (username) DO NOTHING;
SQL
```

## Smoke mode for deterministic runs

`APP_ENV=smoke` activates a deterministic manual-run path that is useful for end-to-end testing without depending on real LLMs and live upstream providers.

Because `.env` auto-loading only happens in `development`, export your env file before starting smoke mode:

```bash
set -a
source .env
set +a
export APP_ENV=smoke
./bin/tradingagent serve
```

Smoke mode is especially useful when you want to verify:

- strategy creation
- login/auth
- manual run dispatch
- run detail pages
- event plumbing
- persistence wiring

## Testing and quality checks

Run commands through `mise exec --` to use the repository's Go and Node versions.
Install frontend dependencies with `npm ci` in `web/`, as CI does.

| Command | Contracts exercised |
| --- | --- |
| `task test:race` | Go short suite with race detection; database skips are expected here |
| `task test:cover` | Short-suite statement coverage, with race detection |
| `task test:integration` | Complete Go suite against a migrated disposable `TEST_DATABASE_URL`; required contracts must pass and unexpected skips fail |
| `task test:maintenance` | Negative tests of the integration gate: missing contracts, changed skip reasons, stale exceptions and package failures |
| `task web:check` | Frontend ESLint, Vitest, TypeScript project build and Vite production build |
| `task fmt:check` | Formatting check that exits nonzero on differences |
| `task check` | Go build, race tests and lint |
| `task ci` | Local Go build/race/lint/vulnerability and maintenance checks, plus frontend checks; database and Docker smoke remain separate |

### Database execution and exceptions

`task test:integration` requires an explicit PostgreSQL URL in `TEST_DATABASE_URL`
whose database name ends in `_test`. Use a disposable Timescale/PostgreSQL 17
service, apply the up migrations in filename order, then run:

```bash
mise exec -- task test:integration -- --output-dir /tmp/augr-integration
```

CI provisions the service and applies migrations before invoking the same Python
runner. The runner executes all tests in `cmd/`, `internal/`, `migrations/`, and `monitoring/`,
without relying on an `Integration` naming convention. Packages run serially;
individual concurrency tests retain their own writers and race detection. Each
package has a 30-minute timeout: the complete PostgreSQL package applies real
migrations per isolated scenario and can take about 19 minutes locally.
Database tests own isolated schemas and cleanup. Never point these commands at
an existing application database.

Current repository fixtures that build on the execution-lifecycle, projection,
dataset or strategy-catalog schemas apply real migrations through the canonical
account boundary (108), then any later migration needed by that contract. The
shared `execRepositoryMigration` helper records applied files inside each isolated
schema so layered fixtures do not replay an older up migration over newer tables.
Down migrations clear that record. Historical migration and rollback tests still
construct their explicit target versions; keep those distinct from current-service
fixtures. Small CRUD fixtures must include the account, environment, origin and
run fields read by the current repository API.

The runner writes JSONL execution evidence and a summary outside tracked source
(by default a temporary directory). `--coverage PATH` adds the integration
coverage profile used by CI. Unit and integration profiles remain separately
reported and merged by CI; no coverage threshold has been lowered or introduced.

`scripts/integration-exceptions.json` records exact test names and prerequisites
for retained historical-schema qualifications and external OpenCode/Docker smoke
tiers. These are explicitly unexecuted in the normal database gate. Their database
variables are cleared by that runner so a developer's retained qualification data
cannot be used accidentally. Run a qualification separately with its documented
schema, evidence and test command. A missing exception test, changed skip reason,
unlisted skip, or required contract that does not pass fails the normal gate.
Do not add an exception for a failing current-service fixture.

### Contract ownership

- Monitoring tests retain alert metric and job-scope contracts in the local Go targets.
- Account/ledger repository tests own exact decimals, retry conflicts, atomic
  failed writes and concurrent convergence. The integration runner requires named
  examples to actually pass, including the canonical signal recorder.
- Market-payload repository tests own migration 110's forged binding rejection,
  immutability and rollback. Migration 111's real-database policy test owns digest
  integrity, immutable policy evidence and refusal of nonempty rollback. SQL-text
  checks remain structural guards, not substitutes for these exercises.
- Copy-origin planning, execution claims, reauthorization, durable failure state
  and order recovery replace obsolete pipeline-finalization copy tests. Current
  service tests and repository concurrency tests own those contracts; no inactive
  legacy test body is retained behind an unconditional skip.
- Feature component tests own repeated list empty/error/retry/unavailable states.
  App tests retain account/auth, cross-route navigation, realtime and confirmed
  mutation journeys. MSW rejects unexpected requests. The full app's development
  mocks are still intentional dynamic entrypoints, not unused test dependencies.
- Frontend lint rejects focused, skipped and placeholder tests. Vitest also rejects
  focused tests and empty discovery. `npm run typecheck` uses `tsc -b`, including
  `src` test code, rather than checking only the empty project-reference root.

Docker/API smoke, browser journeys, schema-specific qualification, restore/load
and deployment soak are separate tiers. A passing local unit or database gate does
not establish those results. Run `scripts/release-gate.sh` when preparing a release.

Historical files under `docs/reports` include executable recovery/evidence checks;
classify their purpose before cleanup. Research PDFs and recovery provenance are
maintained inputs. Reproducible local scratch output belongs under ignored `.tmp/`
or outside the checkout.

## CLI workflow

The CLI talks to the local API server. Typical env setup:

```bash
export TRADINGAGENT_API_URL=http://127.0.0.1:8080
export TRADINGAGENT_TOKEN=...
```

Examples:

```bash
./bin/tradingagent strategies list
./bin/tradingagent run AAPL
./bin/tradingagent portfolio
./bin/tradingagent risk status
./bin/tradingagent dashboard
./bin/tradingagent memories search earnings
```

For CLI entry points, see the command summary in the repository [README](../README.md#cli) and run `./bin/tradingagent --help` after building.

## Frontend workflow

The web app lives in `web/` and exposes these routes:

- `/login`
- `/`
- `/strategies`
- `/strategies/:id`
- `/runs`
- `/runs/:id`
- `/portfolio`
- `/memories`
- `/settings`
- `/risk`
- `/realtime`

For the mounted route list, see `web/src/App.tsx`.

## Operational development notes

Useful health endpoints:

```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/health
curl http://localhost:8080/metrics
```

Useful database access:

```bash
docker compose exec postgres psql -U postgres -d tradingagent
```

Useful log inspection:

```bash
docker compose logs -f app
docker compose logs -f postgres
docker compose logs -f redis
```

## Current contributor hazards

Before doing anything expensive, read [Known Issues](known-issues.md).

The big ones today:

- unresolved merge conflicts exist in several runtime, risk, API-test, and frontend files
- some documented integrations are partially wired rather than fully productionized
- WebSocket auth is not enforced by the current handler
- secret values entered through the settings UI do not persist across restarts; non-secret settings persist through `app_settings`

## Suggested contributor reading order

1. [Getting Started](getting-started.md)
2. [Architecture Audit](AUGR_ARCHITECTURE_AUDIT.md)
3. [Roadmap](roadmap.md)
4. [ADRs](adr/README.md)
5. [Known Issues](known-issues.md)
