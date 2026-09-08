# Options scan freshness recovery

Status: local fix verified; production recovery awaits deployment and a newly authorized soak.

## Preserved failure evidence

The NUC protected ledger at `/var/lib/augr-cutover/canonical-20260827/soak/`
remains failed at window 5, with one blocking failure. The failed scan began
at `2026-09-04T02:00:00Z` and reported 99 stale prices out of 100 symbols.

A read-only query of `tradingagent_canonical_20260827` (schema 109, not dirty)
reproduced the distribution from OHLCV cache records fetched between
`2026-09-04T02:00:00Z` and `2026-09-04T02:01:00Z`:

| Latest bar timestamp | Series count |
| --- | ---: |
| `2026-09-02T13:30:00Z` | 99 |
| `2026-09-03T13:30:00Z` | 1 |

September 3's completed session was required. These timestamps rule out a
UTC-midnight date-label conversion as the explanation for this incident.
The cache's `stock-chain` label does not identify the individual responding
provider. The configured source policy places Yahoo before Polygon.

## Code change

Previously, any nonempty provider response ended fallback. The scan subsequently
rejected stale bars, after the data service had already cached them.

`GetOHLCVValidated` adds an optional caller acceptance policy to cache reads and
provider fallback. The options scan requires its existing completed-session
freshness check. Rejected cache entries trigger provider requests; rejected
provider results allow the next configured provider to run. Only accepted
nonempty results are cached. Exhausted stale results remain `price_stale` and
the 25% optionable / 80% usable-chain coverage requirements remain unchanged.
Ordinary historical requests keep their existing behavior. Manifest-bound
requests validate their immutable evidence without falling back to providers.

## Local verification

- Regression cases reproduce the September 2/3 boundary, stale cache and primary,
  fresh fallback, fresh-cache hits, fresh-primary short circuit, all-stale input,
  and stale input followed by empty or failing fallback.
- `go test ./internal/data ./internal/automation`: 524 passed.
- `go test ./...`: 5,759 passed across 147 packages.
- `go test -race ./internal/repository/postgres ./internal/accountingrecon ./cmd/tradingagent`:
  505 passed across three packages.
- `go build ./cmd/tradingagent`: passed, with output in a temporary build directory.

These commands do not establish a production recovery. A skip audit found 402
PostgreSQL tests and one command-package test skipped with the available
configuration; this is not a new full PostgreSQL rehearsal. Fresh fallback availability for the production watchlist
must be established on the deployed artifact.

## New soak procedure

1. Review and commit the narrow patch, record its exact commit and built app image,
   and retain the currently deployed app/web image IDs and rollback evidence.
2. Obtain approval for deploying that artifact and starting a new protected soak.
   Keep live trading disabled and release drills unverified.
3. Deploy using the canonical cutover runbook, validate the four database targets,
   schema/account identity, application reads, and scheduler state.
4. Preserve the failed ledger and all prior archives. Create a distinct protected
   ledger directory for the new attempt; record its start boundary, image IDs,
   required duration/windows, and rollback references. Do not resume window 5.
5. Observe a natural after-hours options scan. Require fresh completed-session
   prices and the existing coverage floors. If no configured provider has fresh
   data, record failure and investigate provider availability; do not widen the
   allowed age or lower the coverage floors to qualify the run.
6. Continue the full required soak and natural job-cycle checks. Any new blocking
   error fails that attempt. Local test success cannot substitute for elapsed
   production evidence or the separate scored-paper campaign.
