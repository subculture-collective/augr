# Promotion evidence cutover

This procedure verifies readiness only. It does not backfill data, switch a reader, deploy, or grant live-trading authority.

## Preconditions

- Set `PROJECTION_ACCOUNT_ID` to the reviewed `paper_scored` account UUID.
- Enable the read-only account inspection gate with `OVERHAUL_ACCOUNTS_READ_ENABLED=true`.
- Authenticate as an operator. Do not pass an account ID in the request; the server-owned binding is the trust boundary.

## Verify

```bash
curl -fsS -H "Authorization: Bearer $AUGR_TOKEN" \
  "$AUGR_API_URL/api/v1/release/cutover-status" | jq .
```

Proceed with a separately authorized reader cutover only when `promotion_ready` and `account_trusted` are `true`, `promotion_block_reasons` and `unavailable_reasons` are empty, all marks are fresh, both mismatch counts are zero, and reconciliation passed for the configured account venue.

Verify the implementation before operator use:

```bash
go test ./internal/...
(cd web && npm test -- --run && npm run build)
./scripts/release-gate.sh
```

## Alerts and response

| Condition | Required response |
|---|---|
| `scope_mismatches > 0` or `missing_canonical_links > 0` | Treat P&L as unavailable. Block promotion. Investigate source identity; never infer an account from dates, `is_paper`, strategy, or a default account. |
| `stale_marks > 0` or `unavailable_marks > 0` | Treat P&L as unavailable. Block promotion. Restore source-qualified marks; do not substitute entry prices. |
| Reconciliation unavailable, failed, or for a different venue | Treat P&L as unavailable. Block promotion. Reconcile the configured account against its matching venue and ledger checkpoint. |
| Legacy rows present | Keep them quarantined. They are historical only and never promotion or profitability evidence. |

After remediation, rerun the status request. A release-readiness result does not override a blocked cutover status.
