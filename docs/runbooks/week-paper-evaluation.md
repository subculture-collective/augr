---
title: "Seven-day paper evaluation"
status: "canonical"
updated: "2026-09-20"
---

# Seven-day paper evaluation

A paper week starts only after the separate prospective qualification gates pass.
The Sunday preparation commands collect evidence without creating a ledger.
Live trading, automatic shadow promotion, Polymarket automation and release-drill
attestation remain disabled. A clean week does not qualify live-capital activation.

## Prepare before the market week

Use the reviewed tooling checkout on **NUC**. The versioned configuration at
`monitoring/qualification/schema114-1022b401.json` pins the canonical database
`tradingagent_canonical_20260827`, schema 114, application source revision and
app/web image IDs. The tool refuses another host/project/database/schema/image,
unsafe flags, an empty or unbound paper cohort, or changed runtime identity.
Never change the expected values merely to clear a mismatch.

```sh
./scripts/paper-week.sh prepare --evidence-dir /var/lib/augr-qualification/preparation
./scripts/qualify-paper.py plan --evidence-dir /var/lib/augr-qualification/preparation
```

These commands require no ledger. They never run an automation, issue an order,
change a strategy, migrate a database or initialize evaluation state. SQL uses
explicit `BEGIN READ ONLY`, read-only connection defaults, statement/lock timeouts,
and the canonical database name. Container environment values are filtered in
memory; secrets, prompts, raw error strings and provider bodies are not retained.

Supply `--token-file /var/lib/augr-qualification/operator-token` for authenticated
scheduler status. This file must contain the operator token obtained through the
normal application login, with mode 0600. Do not invent/sign a token, reset a
password, borrow another account, copy a server secret or bypass authentication.
An absent/expired session is an explicit evidence gap. No token is stored in the
configuration or receipt. HTTP redirects cannot forward the token.

The configuration points at Dozor Prometheus, `http://10.0.0.57:9090`. A reachable
Prometheus with no matching Augr target is still a failure finding. Sunday readback
found no matching target and no supplied authenticated session: these are real
prequalification prerequisites, not reasons to relax the collector. API health,
startup registration, durable controls and current API enabled state are separate
claims. See [the qualification runbook](paper-qualification.md).

Each invocation creates a new mode-0700 evidence directory with `receipt.json`
and a SHA-256 manifest. Files are mode 0600. Partial and failed receipts remain.
The collector defaults to a bounded 30-minute window; `--since` requires an
explicit timezone. Detail sections cap at 501 rows and report truncation as
incomplete: choose narrower windows and preserve all receipts rather than silently
losing rows. Startup schedule evidence covers the current application lifetime.

Exit statuses: **0** means collection completed (inspect findings; it is not GO),
**2** means a refused/failed operation, **3** means incomplete collection or a
monitoring finding. Dry-run receipts and historical context never count toward
the natural-run qualification gates.

## Initialize only after GO

Complete issues #38–#41, applicable exact-commit release/start checks, required
capability review, authenticated UI/readiness evidence and the full enabled
natural scheduler chain. Use the current gate report to freeze the exact runtime,
cohort/immutable versions, controls, configuration and tool content hashes.
Any subsequent restart, image/config/cohort/tool change invalidates that baseline.

Prepare a reviewed `go.json` using the contract in `paper-qualification.md`. It
must identify a reviewer, decision time and matching baseline/tool hashes. Every
required gate needs a pass and existing checksummed evidence files. Run initialization
within an hour of that decision, after fresh preflight. The start is now, never
backdated to Sunday or to the prequalification observation window.

```sh
./scripts/paper-week.sh init \
  --go /var/lib/augr-qualification/decision/go.json \
  --ledger /var/lib/augr-qualification/paper-week-1022b401 \
  --token-file /var/lib/augr-qualification/operator-token \
  --evidence-dir /var/lib/augr-qualification/preparation
```

The wrapper validates everything before publishing a complete new ledger directory.
A persistent exclusive lock prevents concurrent initializers. Existing directories,
including empty/legacy directories, are refused. Interrupted initialization cleans
only its own unpublished staging directory. It never overwrites a previous ledger.

The new ledger uses **JSON**, not sourced shell state: `baseline.json`, `go.json`,
`initial.json`, and `snapshots/`. The old `start.env` / `cohort.ids` format is not
silently imported or resumed. Keep old failed ledgers intact under
`/var/lib/augr-cutover/canonical-20260827/`.

## Monitor the week

```sh
./scripts/paper-week.sh status --token-file /var/lib/augr-qualification/operator-token
./scripts/paper-week.sh snapshot --token-file /var/lib/augr-qualification/operator-token
```

Snapshots verify baseline/cohort identity and retain bounded detail windows.
Review the accumulated receipts over the entire evaluation window; a single
30-minute snapshot is not a seven-day report. Monitor pipeline completion and
signals; paper orders/fills/fees and position P&L; journal/replay/order linkage;
provider/model latency, tokens and cost; source freshness, automation outcomes,
reconciliation and alert state. Retain anomaly timestamps, run/order/decision IDs,
original failed receipts and follow-up actions. Never edit failed evidence.

Stage and validate monitoring before initialization; activate only after GO and
ledger readback. The timer/service and exact activation/disable commands are in
`paper-qualification.md`. Installation is not natural-run proof; verify the first
scheduled monitor execution separately.

## Week acceptance

- No live orders or unexplained broker/local reconciliation drift.
- Schema, API, scheduler, database, Redis and Prometheus remain available.
- At least one complete scheduled cycle for every required enabled paper market.
- No stuck runs; explain every failed run, skipped dependency, degraded result and report error.
- Every decision has appropriate replay evidence, and every paper-ordered decision has its order link.
- Required settlement/expiration jobs complete for contracts that resolve or expire.
- Provider freshness/coverage and latency, token use and costs meet the predeclared application/operator gates; fallback remains visible.
- Report P&L, win rate, fill rate and drawdown, explicitly inconclusive when closed round trips are insufficient.

Preserve the final evidence and compare it with initialization. The longer paper
validation requirements still govern any later live-capital decision.
