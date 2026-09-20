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
