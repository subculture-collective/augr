# Roadmap

This roadmap states priorities, not delivery dates. Current deployment evidence
may narrow or reorder them.

## Reliability before expansion

1. Keep venue reconciliation observable, bounded, and recoverable after
   upstream timeouts or automatic job suppression.
2. Improve historical and options-data freshness so provider fallback produces
   explicit, dependable coverage rather than intermittent candidate sets.
3. Make schedule, eligibility, qualification, and execution state easy to
   distinguish in the API and operator UI.
4. Continue validating backup, restore, rollback, and natural scheduled-run
   behavior before promoting releases.

## Test and delivery quality

- Raise coverage in high-risk branches based on gaps found by review, not an
  arbitrary repository-wide percentage.
- Keep PostgreSQL isolation, migration, concurrency, accounting, and safety
  contracts in the disposable-database tier.
- Add browser journeys for critical operator mutations and failure recovery.
- Keep CI free of duplicate full-suite runs and generated repository reports.

## Product and execution maturity

- Improve strategy schedule and qualification explanations.
- Expand portfolio attribution and reconciliation diagnostics.
- Harden partial-fill, retry, stale-observation, and provider-outage handling.
- Improve run replay and decision provenance without obscuring hard risk gates.
- Treat additional venues or providers as capability-scoped integrations with
  explicit readiness checks.

## Research and learning

- Evaluate memory retrieval quality with representative cases before changing
  storage technology.
- Measure whether model/debate changes improve decisions rather than assuming
  that more agents or prompts create an edge.
- Preserve dataset and source-rights evidence for any strategy used in
  qualification.

Completed implementation plans are not retained as roadmap material. Durable
decisions belong in [ADRs](adr/README.md), repeatable procedures in
[runbooks](runbooks/README.md), and observed limitations in
[Known Issues](known-issues.md).
