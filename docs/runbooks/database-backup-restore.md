# Database backup and restore verification

Use this procedure before schema-affecting releases and during scheduled
recovery rehearsals. A successful `pg_dump` command is not restore evidence.

## Capture the baseline

Record, without exposing credentials:

- host, Compose project/service, database name, and PostgreSQL image/version;
- schema version and dirty flag;
- critical financial/account table counts and stable fingerprints;
- application revision and timestamp.

Use the deployment's explicit database service and credentials. Write the
backup to the protected external release directory, not the repository.

```bash
pg_dump --format=custom --no-owner --file="$BACKUP_FILE" "$DATABASE_URL"
sha256sum "$BACKUP_FILE"
pg_restore --list "$BACKUP_FILE" >/dev/null
```

Restrict the backup and evidence-file permissions. Retain the SHA-256 checksum
with the release record.

## Restore into isolation

1. Create a uniquely named empty database on an isolated/rehearsal PostgreSQL
   instance of the compatible version.
2. Restore with `--clean --if-exists --single-transaction --exit-on-error
   --no-owner` as appropriate for the target role model.
3. Verify the restored schema version is clean and exactly matches the captured
   baseline.
4. Recompute critical row counts and stable fingerprints and compare them with
   the baseline.
5. Run bounded read-only application or SQL checks needed for the release.
6. Drop only the exact uniquely named rehearsal database after preserving the
   evidence.

Never restore over the canonical database as a verification shortcut. Never
count an archive listing alone as a successful restore.

## Production recovery

Production restoration requires explicit incident authority, confirmed target
identity, a stopped or fenced writer set, a selected verified backup checksum,
and a documented recovery point. After restore, verify schema, application
health, safety settings, account/ledger fingerprints, pending work, and broker
reconciliation before re-enabling automation. Live trading remains disabled
unless separately authorized.
