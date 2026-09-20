---
title: "Schema-114 paper qualification tooling"
status: "canonical"
updated: "2026-09-20"
---

# Paper qualification tooling

Tracks Gitea #33–#42. Sunday #34–#37 prepare tools and rehearse failure paths.
Monday/Tuesday #38–#41 require actual prospective natural-run evidence. Only
#42 may initialize a new ledger and activate monitoring after explicit GO.

## Verified schedule correction

The application constructs cron with `cron.WithLocation(easternTime)`, where
`easternTime` is **America/New_York**. Earlier issue descriptions interpreted
unqualified cron expressions as UTC; those times were corrected. The source
regression test checks both next-run times and the application's session/holiday
gate. September 21 is an admitted NYSE trading day in this candidate's calendar.

| Boundary | America/Chicago | UTC |
|---|---|---|
| SPY strategy, Monday September 21 | 09:00 Monday | September 21 14:00 |
| Options scan | 21:00 Monday | September 22 02:00 |
| History refresh | 23:00 Monday | September 22 04:00 |
| Overnight sweep | 23:30 Monday | September 22 04:30 |
| Backtest slots, if enabled | 00:00–04:30 Tuesday, every 30 minutes | September 22 05:00–09:30 |
| Generation, if enabled | 05:00 Tuesday | September 22 10:00 |
| Options discovery, if enabled | 05:30 Tuesday | September 22 10:30 |

The decision waits for required terminal results, not the last start time.
NUC startup readback registered history refresh and sweep, but did not register
backtest/generation/options discovery. The generated plan marks absent jobs and
unverified enabled state. Review applicability before arming; never enable jobs
to make the checklist green. Startup registration and durable controls do not
prove current in-memory enabled state. Use a normal authenticated status read.

## Sunday commands and verification

Run offline checks in the tooling checkout:

```sh
python3 scripts/test-qualification.py
go test ./internal/automation -run TestQualificationSeptemberBoundariesUseEasternCron -count=1
shellcheck scripts/paper-week.sh scripts/observe-automation-run.sh scripts/observe-paper-boundary.sh
```

CI also compiles every collector SQL query against its migrated disposable
TimescaleDB and exercises the numeric result-field allowlist with a real JSON
fixture. Production SQL remains read-only. The standard release gate includes
the behavioral checks; this tooling PR does not redeploy the application.

On NUC, prepare a fresh receipt and plan:

```sh
./scripts/paper-week.sh prepare --evidence-dir /var/lib/augr-qualification/preparation
./scripts/qualify-paper.py plan --evidence-dir /var/lib/augr-qualification/preparation
```

Add `--token-file /var/lib/augr-qualification/operator-token` when a normal operator
login session is available. Read `collection_status`, `blockers`, `findings` and
`qualification` separately. An exit 3 with an unavailable authenticated session
is a correctly retained incomplete dry-run, not natural qualification evidence.
No `start.env`, cohort freeze, ledger initialization, timer install or activation
occurs in these commands.

`--receipt /absolute/receipt-directory/receipt.json` permits offline `plan` and
`monitor-check` only and checks the sibling SHA-256 manifest. It cannot initialize
or activate anything. The receipt must match the configuration. Each invocation
retains its own report. Observer plans and local notifications are additional
artifacts in the invocation directory.

When copying a reviewed tool tree to NUC instead of using a Git checkout, export
`qualification-source.json` from `qualification.core.tool_identity()` alongside
it. The collector verifies every listed content hash before accepting this
provenance; missing Git/export identity is shown as unknown. Regenerate the export
after any edit. Keep original candidate source SHA, tooling Git SHA and content
hash separate. Never label a dirty dry-run tree as an exact committed release.

## Arm prospective observers

Generate/review a fresh plan, resolve scheduler authentication and monitoring
findings, then arm before each `arm_by`. The default lead is two minutes and the
plan asks for five minutes of headroom. Every command has a two-hour terminal
timeout; choose an explicit bounded longer timeout only for an approved long job.
The two-hour default is inherited from the previous observer, not a trading gate.

```sh
./scripts/qualify-paper.py observe --kind strategy \
  --target 7c1aea67-ca86-4645-97d3-23b7b732c260 \
  --due 2026-09-21T14:00:00Z \
  --token-file /var/lib/augr-qualification/operator-token \
  --evidence-dir /var/lib/augr-qualification/observations

./scripts/observe-automation-run.sh options_scan 2026-09-22T02:00:00Z \
  --token-file /var/lib/augr-qualification/operator-token \
  --evidence-dir /var/lib/augr-qualification/observations
```

Run each observer in an operator-owned persistent session or an explicitly named
one-shot service. Record its PID/unit and evidence directory. The slot lock is
keyed by kind, target and due timestamp. Duplicate ownership, late arming, missed
precheck, ambiguous/changing run ID, manual trigger, failed terminal outcome,
version drift and timeout all fail. Admission covers the first minute of the
specified boundary, so a later recurrence cannot substitute for a missed one.
Polling is five seconds; waits are at most 30 seconds.

The strategy observer checks immutable execution version, schedule-trigger log
metadata and manual-run audit entries. The automation observer checks admitted
job start metadata and rejects manual-trigger events. These are corroborating
observations; the schema lacks a universal immutable trigger-kind field for these
rows. Job-specific input/output review remains required. A terminal receipt says
`natural_terminal_evidence_collected`, never GO or domain-qualified.

An append-only, flushed journal retains progress before a final receipt. SIGTERM
and normal interruption retain failure receipts. SIGKILL/host loss leave an
incomplete journal: preserve it and record failure. Do not delete the journal to
re-arm the same slot; wait for a new natural boundary. Persistent lock files are
empty ownership inodes and deliberately remain. Cleanup is limited to an
initializer's own unpublished staging directory; failed ledgers/reports remain.

## GO contract and exact baseline

A reviewer creates `go.json` after all required natural evidence passes. Required
keys are `decision: "GO"`, `reviewer`, timezone-aware `decided_at`,
`baseline_sha256`, `tool_sha256`, and `gates`. Gate names are configured as
`strategy_session`, `options_scan`, `history_refresh`, `overnight`,
`provider_freshness`, `decision_integrity`, `release_readiness`, `capability_review`.
Each gate has `status: "pass"` and a nonempty `evidence` list of objects containing
absolute `path` and `sha256`. These files must exist and match their hashes.
This is explicit operator attestation with artifact integrity, not a digital
signature or an automatic interpretation of arbitrary report text.

Use baseline/tool hashes from a fresh complete preflight with no unresolved
findings. The baseline includes deployed source/images/container identities and
restart counts, canonical schema identity, cohort/immutable versions, durable
controls, safety values, configuration hash and tool content hash. Review source
freshness/coverage thresholds, failure receipts, provider/model behavior,
reconciliation, domain writes, authenticated readiness and capability applicability
before attesting. Neither generic terminal success nor health HTTP 200 replaces
these checks. Missing/incomplete evidence cannot be marked pass.

See [paper-week initialization](week-paper-evaluation.md#initialize-only-after-go).
A GO expires after one hour for initialization/activation; refresh the decision
and actual baseline if operational work takes longer. No ledger or monitoring
activation is part of Sunday work.

## Staged monitoring

The versioned `.service` and `.timer` files are **inactive templates**. The service
runs the collector every five minutes and writes local JSON notification artifacts.
It sends no external messages. Existing Prometheus release rules remain in force
and are validated with the same pinned promtool image used by `release-gate.sh`.
`monitoring/qualification/prometheus-scrape.yml` is a validated, inactive scrape
fragment for the missing Dozor target. Merge its scrape job into the managed fleet
configuration when operational activation is authorized; never replace the fleet
configuration with this fragment. Sunday tooling preparation does not reload it.

Monitoring checks target/config/baseline identity, flags, health/restarts, current
scheduler enabled state and failures, reconciliation result/age, stuck queues,
decision/replay/order linkage, source failure/staleness counters and risk guards.
Receipts include allowlisted provider last-success/cooldown metrics for review.
Source metrics are not equivalent to fresh bar/chain evidence. Required domain
freshness remains governed by application checks and the reviewed GO evidence.

Receipt/reconciliation age bounds are two five-minute intervals. The existing
five-failure auto-disable threshold is retained. The default job deadline is two
hours. A monitoring observation never authorizes a trade, lowers a provider gate
or automatically decides GO. The monitor checks expected options/history/sweep
start and completion against Eastern cron after ledger start. Its reviewed
calendar coverage is September 21–29; dates beyond that report a review finding.
The existing application's holiday gates remain authoritative.

Only after GO and fresh ledger readback, install the exact reviewed tooling at
`/opt/augr-qualification`, ensure its content matches the qualified hashes, and
run:

```sh
./scripts/qualify-paper.py monitor-activate \
  --token-file /var/lib/augr-qualification/operator-token \
  --evidence-dir /var/lib/augr-qualification/activation
sudo install -m 0644 monitoring/qualification/augr-paper-monitor.service /etc/systemd/system/
sudo install -m 0644 monitoring/qualification/augr-paper-monitor.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now augr-paper-monitor.timer
systemctl list-timers augr-paper-monitor.timer
```

The activation marker requires the exact ledger baseline, GO evidence, config and
tool content. A prematurely installed timer still refuses without activation.
Verify loaded unit files/config hashes, first observation, scheduled next-run time
and the first natural timer execution separately. A successful one-shot is not
proof of the timer's natural execution.

To stop the new monitor while preserving evidence:

```sh
sudo systemctl disable --now augr-paper-monitor.timer
./scripts/qualify-paper.py monitor-disable
```

The marker is archived, not deleted. No old failed monitor/ledger is resumed.
