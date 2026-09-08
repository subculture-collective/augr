# History refresh revalidation recovery

The protected signal-scope soak failed at window 9. The job started at
2026-09-08T04:00:00Z and emitted an ERROR at 04:56:13Z: updated=0,
selected=251, minimum usable coverage=50%. Preserve that attempt unchanged.

## Diagnosis and repair

220 tickers returned no new bars; 31 retained provider failures. The request
logs include successful empty responses for the gap after Friday through the
Labor Day closure, and entitlement/absent-history errors for older ranges.
The job only revalidated tickers with zero provider requests. Empty gap requests
therefore prevented revalidation even when existing history was current.

Revalidate a trailing ten-day window when a nonfailed incremental result has
zero fresh bars, as well as on a cache-only result. Require the latest bar
actually returned by the provider to match the expected completed session;
a newer cached bar must not mask a stale provider response. Preserve historical
provider failures and all existing coverage thresholds. No entitlement upgrades,
watchlist changes, fabricated history, or calendar bypasses are part of this fix.

The deterministic regression uses midnight after Labor Day with Friday's bar,
and tests fresh, empty, stale, and failed trailing requests. All four fail on the
old implementation (no revalidation), and pass with the repair. Also retain
partial-result, cancellation, and missing-repository checks.

## Deployment boundary

Main includes schema 110/111 work not in the deployed schema-109 runtime. Apply
this narrow repair onto production commit 53e024c9 and keep its schema contract,
web image, database targets, and safety flags unchanged. Preserve rollback image
and the old ledgers. A new attempt must start at the new app's actual start time.

Require a natural history_refresh cycle in addition to the prior required job
cycles. Partial coverage is not a clean full-history success: individually review
any provider failure with its exact receipt. Never waive a new ERROR or accept
less than the existing 50% operational coverage floor. Degraded deep scans with
one insufficient series likewise need explicit review, not blanket acceptance.

Tests establish the repaired control flow, not provider entitlement or future
natural-cycle success. Keep live trading disabled and release drills unverified.
