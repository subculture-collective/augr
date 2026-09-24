# Runbooks

These pages describe repeatable operator procedures. Capture timestamps,
revisions, schema versions, and readbacks in the external operations record;
do not add generated incident reports to this directory.

## Before changing a running system

- Confirm the target host, checkout, release revision, container identity, and
  canonical database.
- Gather current job, provider, reconciliation, and account evidence.
- Keep live trading disabled unless the specific procedure authorizes it.
- Preserve backups, rollback artifacts, failed ledgers, and provider evidence.
- For schema changes, migrate first, restart the app second, and verify exact
  schema and health readbacks third. `GET /healthz` is liveness; `GET /readyz`
  reports trading readiness (schema, kill switch, scheduler, automation, LLM)
  and lists failing checks in its 503 body.

## Safety and incidents

- [Emergency kill switch](emergency-kill-switch.md)
- [Circuit breaker investigation](circuit-breaker.md)
- [Broker API outage](broker-api-outage.md)
- [LLM provider outage](llm-provider-outage.md)
- [Investigating a bad trade](bad-trade.md)
- [Reviewing agent decisions](review-agent-decisions.md)

## Recovery and releases

- [Database backup and restore](database-backup-restore.md)
- [Rolling restart](rolling-restart.md)
- [Release readiness and recovery drills](release-readiness.md)
- [OpenCode OAuth provider](opencode-oauth-fallback.md)

## Routine evidence workflows

- [Adding a strategy](add-strategy.md)
- [Capital and margin policy](capital-margin-policy.md)
- [Point-in-time dataset evidence](dataset-evidence.md)
- [Reproducible experiments](reproducible-experiment-runner.md)
- [Venue reconciliation](venue-reconciliation.md)
- [Seven-day paper evaluation](week-paper-evaluation.md)
- [Strategy catalog and experiment declarations](strategy-catalog.md)
- [Promotion and retirement decisions](promotion-retirement.md)
- [Statistical robustness evidence](statistical-robustness.md)

## Specialized execution and data procedures

- [Common execution lifecycle](common-execution-lifecycle.md)
- [Common simulation venue](common-simulation-venue.md)
- [Alpaca and Kalshi lifecycle operations](alpaca-kalshi-common-lifecycle.md)
- [Kalshi paper/data setup](kalshi-paper-data.md)
- [Kalshi live readiness](kalshi-live-readiness.md)
- [Exact stock source evidence](exact-stock-source-evidence.md)
- [Local economic operator](local-economic-operator.md)
- [PostgreSQL collation maintenance](postgres-collation-maintenance.md)

Runbooks do not establish that a deployment is currently qualified. Confirm the
current external release and soak records before reporting production status.
