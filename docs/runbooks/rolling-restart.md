# Rolling restart and release replacement

Use this procedure for a reviewed deployment. It does not authorize a release,
schema change, or live-trading change on its own.

## Preconditions

1. Identify the exact source revision, immutable app/web images, active Compose
   project, canonical database, and previous image IDs.
2. Confirm the worktree/release tree and migration set match the candidate.
3. Record the current clean schema version, service health, job controls,
   live-trading setting, broker modes, and critical financial row counts.
4. Complete the [backup and restore](database-backup-restore.md) procedure and
   retain its checksum and readback evidence.
5. Confirm the candidate passed the applicable test, release, and rollback
   gates. A local build is not a deployable image identity.

## Replacement

1. Apply pending migrations using the deployment's migration role and tool.
2. Verify the exact clean schema version before starting candidate processes.
3. Replace only the intended app and web services with pinned candidate images;
   do not rebuild on the target host during replacement.
4. Wait for container health, then verify database and Redis health through the
   application endpoint.
5. Read back served asset identity, application revision/image, schema version,
   scheduler state, live-trading state, broker modes, and critical account data.
6. Perform the authenticated operator journey appropriate to the change.

Do not restart PostgreSQL, Redis, or unrelated services merely because the app
changed. Preserve their identities unless the reviewed release explicitly
requires otherwise.

## Failure and rollback

- Stop replacement work if migrations are dirty, images are mutable or
  mismatched, health does not converge, account data changes unexpectedly, or
  safety settings differ from the baseline.
- Prefer application-image rollback while retaining a forward-compatible
  schema. A database down-migration is allowed only when the migration's data
  guards pass, the release plan explicitly authorizes it, and a restore-tested
  backup is available.
- Before replacing a running process, allow admitted work to drain within the
  configured shutdown window. Verify there are no orphaned running pipeline or
  automation rows after termination.
- After rollback, repeat exact image, schema, health, safety, account, and
  authenticated-journey readbacks. Record what was restored and what evidence
  remains invalidated.

## After replacement

Observe natural scheduler, provider, reconciliation, and projection work. Do
not declare a release qualified from a healthy container or manually triggered
job. Start or continue the applicable external soak ledger only when its actual
prerequisites pass.
