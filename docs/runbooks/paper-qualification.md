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

## Normal login on a headless NUC

Use an interactive SSH terminal; no browser is required:

```sh
ssh -t nuc
cd /opt/augr-qualification
./scripts/qualify-paper.py login --token-file /var/lib/augr-qualification/operator-token
```

Use the actual reviewed tool directory if it has only been staged. The helper
checks the deployed identity, prompts for the operator username/email and hidden
password, and calls the ordinary `/api/v1/auth/login` endpoint. It saves only the
returned access/refresh session and expiry in a private file; it never saves the
password, emits tokens, changes credentials, grants roles or alters token lifetimes.
The session directory must be writable by the operator/service user.

Collectors using a renewable session call the normal `/api/v1/auth/refresh`
endpoint when access expiry is within a minute. A persistent lock and atomic
replacement serialize concurrent observers. Authentication/renewal are the only
HTTP POST operations; database inspection stays read-only and no trading/job API
is called. Missing, expired or rejected refresh sessions fail closed and require
a new normal login. Existing access-only files cannot be renewed automatically.
For a long pre-armed wait, ensure the refresh session remains valid until precheck;
the collector does not keep sessions alive while an observer is sleeping.

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

For a new attempt within the reviewed calendar, pass `--session-date YYYY-MM-DD`
to `plan`. A new date creates new observation slots; never delete the old slot
journals to reuse a failed boundary. Dates outside the reviewed calendar fail.

Prechecks cover the preceding 30 minutes, independently of when the process was
armed. Postchecks cover that precheck window through completion. Large SQL
sections use 500-row cursor pages in one read-only repeatable-read transaction,
with a 10,000-row cap plus an overflow sentinel and a 25-second process deadline.
Overflow remains incomplete evidence. A strategy rejected before a pipeline is
created retains its allowlisted reason from `agent_events`.

If Docker has rotated away startup registration logs, pass
`--registration-receipt /absolute/receipt-directory/receipt.json` to `plan` and
`observe`. This must be a checksummed, complete receipt from the same configuration
and unchanged app ID, image, source revision, start time, and restart count.
Only startup registration events are reused. Enabled state, natural starts,
completions and manual-trigger checks are always collected afresh. A restart
invalidates the archive. Generated observer commands carry this argument.

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

Sunday read-only inventory also found a user crontab entry at `55 23 * * *`
calling the legacy `scripts/paper-week.sh` through
`/home/onnwee/Projects/patrickfanella/augr` (resolving to
`/srv/repos/patrickfanella/augr`). Its old `var/paper-week/start.env` and
`cohort.ids` exist. This entry was not changed during preparation. Before activating
the new monitor, archive the current user crontab and old ledger, identify and
retire only that exact legacy entry, then verify the remaining crontab. Do not
run both writers or repoint the old ledger. No `augr-paper-monitor.timer` was
installed or active at Sunday readback.

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

## September 21–22 failed attempt and recovery

All four observers armed Sunday but stopped at precheck. The original collector
queried from arming time and hit its 500-row bound: 1,242 automation rows already
existed by the first precheck. The sweep also overflowed coverage detail. Preserve
those four receipts and journals under
`/home/onnwee/.local/state/augr/qualification-observations/2026-09-21` on NUC.
Their outcomes remain failures; the corrected tools do not reconstruct prospective
passes from retrospective data.

Loki and durable preparation events establish that SPY triggered on Monday and
Tuesday, then failed the required fundamentals completeness check before pipeline
creation. The strategy requires corporate fundamentals unavailable for this SPY
input. Keep that gate and execution version intact pending a reviewed replacement.
History and options runs retained provider/coverage failures. The sweep reported
`all_unqualified=1`; this is a research result, not evidence that its fetch failed.
No performance threshold or provider requirement was relaxed during recovery.

Dozor's missing `augr-api` scrape job was restored on September 23 UTC from the
versioned fragment. The full existing config/rules validated before reload, and
the target read back `up`. Canonical host documentation records the configuration
backup and rollback. This scrape restoration does not activate the gated paper
ledger or paper-monitor timer.

The September 23 live check also found that NUC's unbounded `docker logs --since`
read omitted recent records while a finite tail returned them. The collector now
requests at most 10,001 lines and refuses a window at that bound (or above 8 MB).
With a verified registration receipt, current log collection starts at the
requested observation window; old startup registrations come only from the
instance-bound archive. An overflow is incomplete evidence, never a passing
absence of manual triggers. This does not replay archived starts or completions.
