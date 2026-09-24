# Known issues and operational limitations

This page lists current architectural or operational limitations. Deployment
incidents and qualification results belong in the external operations record.

## Provider completeness varies

Provider adapters have different entitlements, rate limits, timestamp quality,
and asset coverage. A fallback may return data while still leaving a scan
degraded. Operators must inspect per-job coverage and freshness rather than
treating a non-empty result as success.

## Reconciliation depends on upstream availability

Broker reconciliation is an independent safety workflow. Repeated upstream
timeouts can fail or automatically suppress scheduled work even while the API
and UI remain healthy. Recovery requires diagnosing the upstream call,
restoring the job deliberately, and observing a subsequent natural run.

## Preflight rejections need the alerting release to be visible

A scheduled strategy rejected in preflight records a
`strategy.preparation_rejected` agent event and appears in the daily review.
Releases that include the strategy runner alerting also log the rejection at
WARN, increment `tradingagent_strategy_preparation_rejected_total`, and send a
warning alert. On older releases none of those signals exist, so a strategy
that never trades can look idle rather than broken. Check
`GET /api/v1/accounts/{id}/events?kind=strategy.preparation_rejected` when a
strategy produces no runs.

## Kalshi demo API returns zero-volume markets first

The Kalshi demo host (`KALSHI_DEMO=true`) lists zero-volume markets on the
first catalog page. The discovery screener rejects those candidates, so a
small `KALSHI_DISCOVERY_FETCH_LIMIT` on the demo host yields no proposals.
Use `KALSHI_DEMO=false` (the runtime then defaults to the elections API host)
for real market data, or raise the fetch limit.

## Binance paper mode means testnet credentials

`BINANCE_PAPER_MODE=true` selects `testnet.binance.vision`, which accepts only
keys created on the testnet site. Production Binance keys fail authentication
in paper mode.

## Polymarket and Kalshi live wire formats are unverified

The retail-API order payloads for Polymarket and the native Kalshi order and
portfolio-event requests have not been verified against live exchanges.
`POLYMARKET_SIGNATURE_TYPE` is stored but not sent. Treat live event-market
execution as blocked until a recorded exchange round-trip exists.

## Live account environments are unsupported by the runtime

The canonical account must be `paper_scored` or `paper_stress`; startup
rejects any other environment. `GET /api/v1/release/readiness` lists the
concrete live-execution blockers (`ENABLE_LIVE_TRADING`, allowlists, account
environment) but live execution stays blocked regardless of configuration.

## Strategy scheduling is explicit

An active strategy without a schedule is available for authenticated manual
execution but will not run periodically. Scheduler enablement, a valid schedule,
strategy qualification, and execution eligibility are separate conditions.

## Local seed credentials are intentionally weak

Migrations retain a predictable account for disposable development databases.
That credential must be replaced in any shared or deployed environment. The
repository does not contain the deployed password.

## Frontend is a separate application

The Go API does not serve the Vite application at its root. Local development
uses separate API and UI ports; deployed environments use their own reverse
proxy and authentication boundary.

## Coverage is useful but incomplete

The Go short suite exercises thousands of cases, including safety, provider,
and orchestration behavior, but aggregate statement coverage is not a release
qualification metric. PostgreSQL contracts, browser behavior, external
providers, and natural scheduler runs have their own evidence tiers.

## Optional capabilities can be unavailable

Kalshi, Polymarket, options, social data, notifications, and individual model
providers may be unconfigured without making every other capability unusable.
Readiness and status reporting should name the unavailable capability rather
than collapse the entire service into a single healthy/unhealthy label.

## Operational qualification is external to Git

Soak observations, live-provider diagnostics, backup/restore evidence, and
release fingerprints are deliberately not committed as changing repository
reports. Consult the current deployment ledger before making a production
claim.
