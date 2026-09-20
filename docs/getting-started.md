# Getting started

This guide runs PostgreSQL and Redis in Compose, with the API and frontend on
the host. It is intended for a disposable local environment.

## 1. Install the toolchain

Use Go 1.25.13, Node.js 22, npm, Python 3, Docker Compose v2,
[Task](https://taskfile.dev/), and the `migrate` CLI. The pinned language
versions are also recorded in `go.mod`, `.nvmrc`, and `mise.toml`.

## 2. Configure the application

```bash
git clone https://git.subcult.tv/subculture-collective/augr.git
cd augr
cp .env.example .env
```

Set these local values in `.env`:

```dotenv
APP_ENV=development
POSTGRES_PASSWORD=postgres
DATABASE_URL=postgres://postgres:postgres@localhost:5434/tradingagent?sslmode=disable
REDIS_URL=redis://localhost:6380/0
JWT_SECRET=replace-with-a-long-random-value
PROJECTION_ACCOUNT_ID=00000000-0000-4000-8000-000000000064
```

Startup also requires at least one configured LLM path and primary market-data
provider. For example:

```dotenv
OPENAI_API_KEY=...
POLYGON_API_KEY=...
```

For local Ollama, set both `OLLAMA_BASE_URL` and a non-empty
`OLLAMA_API_KEY`; the token is part of Augr's provider validation even when the
local endpoint does not enforce it. OpenCode, Anthropic, Google, OpenRouter, and
xAI are also supported. Primary data-provider validation accepts Polygon,
Alpha Vantage, Finnhub, or Financial Modeling Prep.

## 3. Start dependencies and migrate

```bash
docker compose up -d postgres redis
task migrate:up
task migrate:status
```

Compose publishes PostgreSQL on `localhost:5434` and Redis on
`localhost:6380`. Service names such as `postgres:5432` work only inside the
Compose network.

## 4. Start the API

```bash
task build
./bin/tradingagent serve
```

In development, the process loads `.env`. Confirm readiness:

```bash
curl http://localhost:8080/healthz
```

If you run the `app` service through Compose instead, its host URL is
`http://localhost:8081`.

## 5. Start the frontend

```bash
npm --prefix web ci
npm --prefix web run dev
```

Open `http://localhost:5173/login`. The frontend defaults to an API base of
`http://localhost:8080`; set `VITE_API_BASE_URL` when using a different port.

For a newly migrated disposable database, use:

- User: `patrick@subcult.tv`
- Password: `demo-pass`

Those are seed credentials for local development only. Shared and deployed
environments must use a separately managed password.

## 6. Exercise the paper path

Create a strategy from the Strategies page, keep paper trading enabled, and
either assign a valid schedule or use **Run now**. Normal development and
production-like runtimes wire the real strategy runner; `APP_ENV=smoke` instead
uses a deterministic runner suitable for smoke checks without external model
calls.

Inspect the resulting run, decisions, and any orders separately. A completed
pipeline may legitimately produce a monitor or hold decision and no order.

## Troubleshooting

### Configuration validation fails

Check that the environment includes `DATABASE_URL`, `JWT_SECRET`,
`PROJECTION_ACCOUNT_ID`, one LLM provider, and one primary data provider. In
non-development environments, source the file explicitly before starting the
binary.

### Migration fails

```bash
docker compose ps postgres
docker compose logs postgres --tail=50
task migrate:status
```

If a disposable local database is dirty and contains nothing worth retaining:

```bash
docker compose down -v
docker compose up -d postgres redis
task migrate:up
```

Never use that reset procedure against shared or deployed data.

### The UI cannot reach the API

```bash
curl http://localhost:8080/healthz
```

Confirm that `VITE_API_BASE_URL` matches the actual API and restart Vite after
changing it. A browser CORS error is commonly a stale or unavailable API URL.

### A run produces no trade

That can be correct. Check the run's terminal status, final signal, risk result,
qualification state, broker mode, and audit events. Candidate discovery,
strategy qualification, order submission, and fill reconciliation are distinct
stages.

Next, read [Development Setup](development-setup.md) and [Testing](testing.md).
