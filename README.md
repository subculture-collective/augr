# Augr

Augr is a paper-first, multi-market trading research and execution system. A Go
service schedules data collection and strategy pipelines, applies hard risk
controls, persists evidence in PostgreSQL, and exposes a React operator UI.

Augr can submit orders when a deployment is deliberately configured for it,
but live trading is disabled by default. A healthy process or a successful scan
does not by itself prove that a strategy is qualified or that an order was sent.

## What is included

- Scheduled and manual strategy pipelines for stocks, options, crypto, Kalshi,
  and Polymarket-oriented workflows.
- Market-data adapters for Polygon, Alpha Vantage, Finnhub, Financial Modeling
  Prep, Yahoo, Alpaca, Tradier, Binance, Kalshi, and Polymarket.
- Paper execution plus guarded broker adapters for Alpaca, Binance, Kalshi, and
  Polymarket.
- Persistent runs, decisions, events, orders, trades, positions, accounting
  evidence, provider observations, and audit records.
- Kill switches, circuit breakers, exposure limits, reconciliation, projection
  checks, and capability-scoped readiness reporting.
- JWT and API-key authentication, WebSocket activity streaming, Prometheus
  metrics, and Grafana dashboards.

## Repository map

| Path | Purpose |
| --- | --- |
| `cmd/tradingagent/` | CLI, application bootstrap, scheduler wiring, and strategy runners |
| `internal/` | API, domain, provider, execution, risk, automation, and persistence code |
| `migrations/` | Ordered PostgreSQL/TimescaleDB migrations |
| `web/` | React, TypeScript, and Vite operator UI |
| `monitoring/` | Prometheus rules and Grafana provisioning |
| `scripts/` | Maintained release, recovery, verification, and operator helpers |
| `docs/` | Canonical guides, ADRs, and current runbooks |

## Quick start

Prerequisites are Go 1.25.13, Node.js 22, npm, Python 3, Docker Compose,
[Task](https://taskfile.dev/), and the `migrate` CLI.

```bash
git clone https://git.subcult.tv/subculture-collective/augr.git
cd augr
cp .env.example .env
```

For local development, set at least:

```dotenv
POSTGRES_PASSWORD=postgres
DATABASE_URL=postgres://postgres:postgres@localhost:5434/tradingagent?sslmode=disable
REDIS_URL=redis://localhost:6380/0
JWT_SECRET=replace-with-a-long-random-value
PROJECTION_ACCOUNT_ID=00000000-0000-4000-8000-000000000064

# Configure one supported LLM provider and one primary data provider.
OPENAI_API_KEY=...
POLYGON_API_KEY=...
```

Then start the dependencies, migrate, and run the applications:

```bash
docker compose up -d postgres redis
task migrate:up
task build
./bin/tradingagent serve
```

In another terminal:

```bash
npm --prefix web ci
npm --prefix web run dev
```

The native API is available at `http://localhost:8080`; Vite uses
`http://localhost:5173`. The Compose-published database and cache ports are
`5434` and `6380`. Running the application itself through Compose publishes it
at `http://localhost:8081`.

Fresh disposable databases seed a local account named `patrick@subcult.tv`.
The retained development password is `demo-pass`; never reuse that credential
in a shared or deployed environment.

See [Getting Started](docs/getting-started.md) for the complete local flow and
[Development Setup](docs/development-setup.md) for configuration and validation.

## Common commands

```bash
task build             # compile ./bin/tradingagent
task test              # short Go suite
task test:race         # short Go suite with the race detector
task test:maintenance  # verify the integration harness itself
task web:check         # frontend lint, tests, and production build
task test:integration  # full contracts against TEST_DATABASE_URL
task audit             # Go vet, lint, vulnerability, and format checks
```

The database integration task is intentionally fail-closed: use a migrated,
disposable database that is not the development or production database. See
[Testing](docs/testing.md) for the test tiers and their ownership.

## CLI

`tradingagent` provides these top-level commands:

- `serve` starts the API and optional scheduler.
- `run` executes the configured one-shot workflow.
- `strategies` manages strategies and manual runs through the API.
- `automation` inspects or operates automation jobs.
- `portfolio`, `risk`, and `capital-ladder` expose operator readbacks and controls.
- `memories` manages agent memories.
- `dashboard` starts the terminal dashboard.

Run `./bin/tradingagent --help` and `<command> --help` for the current flags.

## Safety and operational scope

- `ENABLE_LIVE_TRADING=false` is the default and should remain so until the
  release and venue-specific gates are satisfied.
- Scheduled jobs are independently capable of succeeding, degrading, failing,
  or being disabled. Container health is not workflow health.
- Schema-affecting releases are applied migration-first, followed by the app,
  then exact schema and health readback.
- Qualification evidence belongs in immutable external ledgers and release
  artifacts, not generated files committed to this repository.

Operators should start with the [Runbooks](docs/runbooks/README.md). Current
limitations are tracked in [Known Issues](docs/known-issues.md).

## Documentation

- [Documentation index](docs/README.md)
- [Architecture](docs/AUGR_ARCHITECTURE_AUDIT.md)
- [Getting Started](docs/getting-started.md)
- [Development Setup](docs/development-setup.md)
- [Testing](docs/testing.md)
- [Runbooks](docs/runbooks/README.md)
- [ADRs](docs/adr/README.md)
- [Roadmap](docs/roadmap.md)

## License

See [LICENSE](LICENSE).
