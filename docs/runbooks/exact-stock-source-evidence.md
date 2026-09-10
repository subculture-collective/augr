# Exact stock import source evidence

## Scope

The stock dataset importer requires `ExactHistoricalProvider`. The Polygon
implementation preserves decimal tokens without converting them through
`float64`. The legacy market-data reader is unchanged. Options imports still
use the legacy reader and do not have this exact-source guarantee.

Each new stock payload contains optional `source_evidence`: the credential-free
relative request path and canonical query, original response page bytes, row
index, and original row bytes. Byte fields use base64 in canonical JSON so that
serialization preserves source whitespace. The payload digest covers this
envelope. The validator reconstructs the selected row from the page and checks
its values, timestamp, instrument, timeframe, feed, and adjustment against the
payload. Explicitly contradictory response identity is rejected.

Missing trade count or VWAP fails the stock import. Unknown values are not
replaced with zero. Duplicate keys, malformed numeric tokens, out-of-range
timestamps, incomplete pagination, and mismatched source rows fail closed.
The provider limits each page to 16 MiB and retained source to 64 MiB and 100
pages. The importer also limits expanded per-payload source bytes to 64 MiB;
larger imports require smaller intervals. These are retained-evidence limits,
not a guarantee that the HTTP transport never allocates a larger response.

## Compatibility

Existing payloads omit `source_evidence` and retain their canonical bytes and
digests. The new reader accepts both formats. Migration 110's existing JSON and
digest constraints accept the added envelope; this change needs no new SQL
migration. Existing records are not rewritten.

Older readers use strict unknown-field rejection. After the first source-bearing
payload is persisted, rollback to an older reader is incompatible even when the
SQL schema version is unchanged. A schema-only check cannot qualify rollback.
Before any production import, release qualification must include a retained
rollback binary that reads both formats and a restore-tested backup. An older
binary is a rollback candidate only while a verified read-only inventory shows
no source-bearing records. Deleting evidence or rewriting payloads is not a
rollback procedure.

## Evidence boundaries

Synthetic tests cover exact decimals, source binding, malformed responses,
pagination, and PostgreSQL round trips for both payload formats. Those tests do
not establish production deployment, provider licensing, historical publication
times, dataset quality, or strategy profitability. Provider access alone is not
an attestation of research or non-display rights. Actual acquisition and dataset
use remain subject to the independently established account authorization.

The complete disposable-database gate is `scripts/test-integration.py` with a
migrated `TEST_DATABASE_URL` whose database name ends in `_test`. Its required
contracts include exact provider pages and pagination, canonical source
round-trip, exact stock import, and persisted source-evidence round-trip.
