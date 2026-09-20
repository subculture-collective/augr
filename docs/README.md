# Augr documentation

This directory contains maintained documentation only. Runtime code,
configuration validation, migrations, and the deployed environment remain the
authority when a document and observable behavior disagree.

## Start here

- [Getting Started](getting-started.md) — first local API and UI session.
- [Development Setup](development-setup.md) — toolchain, configuration,
  migrations, and contributor workflow.
- [Testing](testing.md) — test tiers, database isolation, and coverage policy.
- [Architecture](AUGR_ARCHITECTURE_AUDIT.md) — current component boundaries and
  execution paths.

## Operate and maintain

- [Runbooks](runbooks/README.md) — incidents, safety controls, provider outages,
  recovery, and release evidence.
- [Known Issues](known-issues.md) — confirmed limitations that affect operators
  or contributors.
- [Roadmap](roadmap.md) — current priorities, without delivery-date promises.
- [ADRs](adr/README.md) — durable architecture decisions and rationale.
- [Agent execution guide](agent-execution-guide.md) — repository workflow for
  autonomous contributors.
- [Prediction-market runtime](prediction-market-runtime.md) — shared native
  execution and settlement contract.
- [Discord webhooks](discord-webhook-setup.md) and [n8n](n8n-integration.md) —
  maintained notification configuration.

## Documentation policy

- Do not commit generated status reports, soak observations, database exports,
  downloaded papers, or task-specific notes.
- Store qualification and recovery evidence in the designated external release
  or operations location.
- Add a runbook only for a repeatable operator procedure. Add an ADR only for a
  durable architecture decision.
- Delete completed implementation plans after their accepted behavior is
  represented by code, tests, maintained docs, or an ADR.
- Update the relevant page in the same change whenever a public command,
  configuration requirement, safety gate, or operator procedure changes.
