---
title: "Release readiness and recovery drills"
status: "canonical"
updated: "2026-08-07"
---

# Release readiness and recovery drills

Live trading stays disabled throughout this gate. Query
`GET /api/v1/release/readiness` with an authenticated operator token. Every
required paper capability must be ready. Polymarket remains visible as an
optional, blocked historical capability and is not a release requirement for
the US deployment. `live_execution` remains a separate, non-required, blocked capability until a broker/market/strategy/capital-tier
activation is explicitly approved.

Run the automated gate from the repository root:

```sh
./scripts/release-gate.sh
```

## Commit identity and synchronization order

The gate is valid only for the exact commit it prints. Reconcile upstream
before running it:

1. fetch the configured remote and inspect divergence without rewriting or
   discarding local work;
2. reconcile any upstream commits and commit the result;
3. confirm the intended release tree is clean, then run the complete gate;
4. push the exact verified commit without force and confirm the remote ref
   resolves to the same object ID; and
5. build and deploy immutable images from that same object ID.

The release gate records `HEAD` before verification, rechecks the clean tree
after all gate commands, and fails if `HEAD` changed. Any edit, conflict
resolution, merge, rebase, amend, or generated tracked change after a passing
gate invalidates the result; rerun the complete gate on the new candidate.
Pushing an unchanged verified commit does not invalidate it.

## Compromised-secret gate

The automated gate cannot prove that an externally managed credential was
revoked. If a credential, token, passphrase, or private key was exposed in
application logs, audit output, CI output, or another retained channel, the
release is blocked until its owner:

1. revokes or rotates the exposed value at the provider;
2. updates the production secret source without printing the replacement;
3. records owner confirmation in the release evidence; and
4. identifies a bounded postdeployment canary that proves errors are redacted
   without copying, hashing, or otherwise retaining the credential in the
   evidence artifact.

A redaction code change prevents another disclosure but does not remediate the
already exposed value. Passing tests, `RELEASE_DRILLS_VERIFIED=true`, a process
restart, or an unavailable retained log window cannot substitute for rotation
confirmation. Optional integrations with unmet credentials or entitlements may
remain unavailable only when they are explicitly reported as blocked and stay
disabled or fail closed; do not enable them merely to make release readiness
appear green.

For qualification preparation and prospective natural-run evidence, follow
[paper qualification](paper-qualification.md). The legacy observer entrypoints
now delegate to the canonical schema-114 collector and fail on wrong targets.

```sh
./scripts/observe-paper-boundary.sh --evidence-dir /var/lib/augr-qualification/preparation
./scripts/observe-automation-run.sh options_scan 2026-09-22T02:00:00Z \
  --token-file /var/lib/augr-qualification/operator-token \
  --evidence-dir /var/lib/augr-qualification/observations
```

The previous positional label and `AUGR_COMPOSE_FILE`, `AUGR_BASE_URL`,
`OBSERVATION_REPORT` environment overrides are no longer supported. Use the
versioned qualification configuration and CLI flags. Collection uses canonical
read-only SQL, bounded requests, allowlisted evidence and preserved failures.
A terminal row alone does not establish provider contact or downstream effects.

Validate Prometheus rules with `promtool check rules
monitoring/prometheus/alerts.yml` (or the matching Prometheus container image).
Do not set `RELEASE_DRILLS_VERIFIED=true` until the evidence table below is
complete for the deployment being promoted. The flag only records operator
attestation; it does not bypass any capability check or enable live trading.

| Drill | Required evidence | Recovery criterion |
|---|---|---|
| Restart | rolling-restart steps, schema gate output, paper account bootstrap logs | runtime returns healthy with kill-switch and durable positions restored |
| Dependency outage | broker and LLM outage tests/runbooks, alert delivery | deterministic/paper paths fail closed or fall back as documented |
| Stale data | snapshot freshness tests and provider last-success metric | entry is rejected and stale source is identified |
| Order rejection | order-manager rejection test and journal/replay row | no position appears; rejection remains explainable |
| Partial fill | fill-engine/broker partial-fill tests and reconciliation result | filled quantity, cash, trade, and position agree |
| Reconciliation | Alpaca, Polymarket, Kalshi, and options reconciliation tests | zero unexplained drift; any deliberate fixture drift alerts |
| Kill switch | API/file/env and mid-run cancellation tests | new orders stop and active execution is cancelled safely |
| WebSocket reconnect | API smoke/reconnect tests | authenticated reconnect resumes without corrupting persisted state |
| Prediction settlement | shared settler and provider-job tests | 0/1 payout, P&L, closed decision, and replay outcome agree |
| Options expiration | expiry workflow tests | worthless and intrinsic cash settlement persist correctly |
| Options assignment | explicit paper assignment-boundary test | no underlying shares are fabricated; paper options cash-settle and live assignment remains blocked |

For a real soak, run at least one complete scheduler cycle for each enabled
paper market, inspect `/api/v1/automation/status`, `/api/v1/risk/cockpit`, the
decision journal/replay, and Prometheus alerts, then attach timestamps and query
outputs to the release record. Any reconciliation drift, incomplete decision
journal, missing settlement, or unexplained alert fails the release.
