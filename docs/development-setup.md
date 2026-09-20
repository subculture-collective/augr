# Development setup

## Toolchain

The repository pins Go 1.25.13 and Node.js 22. Contributors also need npm,
Python 3, Docker Compose v2, Task, and the `migrate` CLI. `task tools` installs
the Go formatting, lint, vulnerability, and migration utilities.

## Local topology

| Component | Native/host URL | Compose-published URL |
| --- | --- | --- |
| API | `http://localhost:8080` | `http://localhost:8081` |
| Vite UI | `http://localhost:5173` | not part of Compose |
| PostgreSQL | `localhost:5434` | `postgres:5432` inside Compose |
| Redis | `localhost:6380` | `redis:6379` inside Compose |
| Prometheus | — | `http://localhost:9091` |
| Grafana | — | `http://localhost:3002` |

The backend does not serve the Vite application at `/`.

## Configuration

Copy `.env.example` and keep real secrets outside version control. The most
important startup requirements are:

- `DATABASE_URL`, `JWT_SECRET`, and `PROJECTION_ACCOUNT_ID`.
- One usable LLM provider. The current defaults route through OpenCode using
  `openai/gpt-5.6-sol` for deep work and `openai/gpt-5.6-luna` for quick work.
- One primary market-data credential: Polygon, Alpha Vantage, Finnhub, or
  Financial Modeling Prep.

`.env` is loaded automatically only when `APP_ENV=development`. Other
environments must receive their variables from the process supervisor or an
explicitly sourced file. Configuration stored through the settings API is for
non-secret runtime settings; it is not a secret manager.

Live execution is off by default. Do not enable it merely to make a test or
readiness check pass.

## Backend workflow

```bash
docker compose up -d postgres redis
task migrate:up
task build
./bin/tradingagent serve
```

Or run the complete backend stack:

```bash
task dev
task dev:logs
```

Useful lifecycle commands are `task dev:restart`, `task dev:psql`, and
`task dev:down`.

## Frontend workflow

```bash
npm --prefix web ci
npm --prefix web run dev
```

Use `npm ci`, not a second package manager, so local and CI dependency
resolution follows `web/package-lock.json`.

## Migrations

Migration files are ordered pairs under `migrations/`. The application checks
the expected schema during startup and fails closed on a mismatch.

```bash
task migrate:status
task migrate:up
task migrate:down
task migrate:create -- descriptive_name
```

Apply schema changes before restarting the application, then verify both the
schema version and service health. Never run integration tests against a shared
or deployed database. The current integration harness requires an explicit
`TEST_DATABASE_URL` and rejects known unsafe targets.

## Validation before review

For ordinary backend and frontend work:

```bash
task test:race
task test:maintenance
task web:check
task audit
git diff --check
```

Run `task test:integration` against a freshly migrated disposable database when
the change touches persistence, migrations, accounting, concurrency, provider
storage, reconciliation, or any end-to-end database contract. See
[Testing](testing.md) for the exact tiers.

## Runtime boundaries

- Normal environments construct the real strategy runner. Smoke mode replaces
  it with a deterministic runner for bounded validation.
- The scheduler, strategy schedules, job controls, and provider readiness are
  separate gates. Enabling the scheduler does not guarantee that a strategy is
  eligible to run.
- Scans, strategy decisions, order submission, fills, reconciliation, and
  qualification are separately persisted outcomes.
- The local seed user and password are disposable development conveniences,
  not deployment credentials.

## Repository hygiene

Do not commit build products, Vite output, Task checksums, generated reports,
soak ledgers, downloaded research papers, database exports, or temporary
operator notes. Keep reusable behavior in code and tests; preserve durable
decisions in ADRs and repeatable procedures in runbooks.
