# Testing

Augr uses several test tiers because an in-memory unit test, a migrated
PostgreSQL contract, a browser workflow, and a natural scheduled run prove
different things.

## Test tiers

| Tier | Command | Purpose |
| --- | --- | --- |
| Go short suite | `task test` | Fast package behavior without database integration |
| Race suite | `task test:race` | Short suite plus data-race detection |
| Harness maintenance | `task test:maintenance` | Fail-closed behavior of the database test runner |
| Frontend | `task web:check` | ESLint, Vitest, TypeScript, and production build |
| Database integration | `task test:integration` | Full Go contracts against a migrated disposable PostgreSQL database |
| Static quality | `task audit` | Vet, golangci-lint, govulncheck, and format checks |
| Smoke/release | runbooks and release scripts | Runtime assembly, migrations, health, recovery, and deployed artifact evidence |

## Database integration safety

Set `TEST_DATABASE_URL` to a freshly migrated disposable database, then run:

```bash
TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:55465/tradingagent?sslmode=disable' \
  task test:integration
```

The harness rejects absent or known non-disposable targets and fails when an
expected database test is unexpectedly skipped. Never point it at development,
staging, or production data.

## What the suite intentionally preserves

- Table-driven subtests for risk boundaries, provider responses, schedules,
  state transitions, and malformed input are distinct behavior cases, not
  duplicate tests.
- Migration up/down, locking, isolation, append-only accounting, account scope,
  and reconciliation tests remain database contracts even when unit tests cover
  adjacent services.
- Security and financial-safety tests should overlap at independent layers when
  they protect different boundaries.
- Frontend tests cover authenticated routing, loading/empty/error states,
  confirmed mutations, URL state, realtime updates, and API schemas.

Avoid tests that merely search documentation for exact phrases, restate the
implementation without behavior, or lock in one-time plans. Documentation links
and examples are checked separately.

## Coverage policy

Coverage is diagnostic, not a release gate by itself. At the start of the 2026-09
repository cleanup, the Go short suite contained 3,739 top-level tests (6,410
including subtests) across 148 packages and reported 57.6% statement coverage;
the frontend contained 217 tests across 17 files. Those counts are a baseline,
not a target.

Add tests when a meaningful behavior, failure mode, safety boundary, or fixed
regression is not covered. Remove or consolidate tests only when they exercise
the same contract with the same setup and assertions, or when they exist solely
to preserve deleted one-time documentation. Review coverage by package and risk
area instead of inflating a global percentage with low-value cases.

## CI ownership

Backend CI runs the short race suite once and the full database suite once; the
integration run produces the database-aware coverage artifact. Frontend CI
installs dependencies once before lint, test, build, and bundle-size checks.
This keeps the required evidence while avoiding repeated identical suites.

Before review, also run `git diff --check` and inspect skipped tests and warnings;
a green exit code with unexpected skips is not sufficient evidence.
