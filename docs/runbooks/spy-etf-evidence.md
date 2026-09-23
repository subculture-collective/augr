# SPY ETF evidence candidate

The rejected corporate-fundamentals strategy remains unchanged. Its strategy ID
is `7c1aea67-ca86-4645-97d3-23b7b732c260` and execution version is
`0225cc3c-0348-a063-16a3-8a3b46b0bb61`. Monday and Tuesday preparation rejections
remain evidence against that baseline. This work prepares a separate paper
candidate; it does not activate a strategy, initialize a ledger, or arm a timer.

## Source and cost

The `spy-ssga-etf-v1` contract reads two public issuer workbooks linked from
[State Street's SPY page](https://www.ssga.com/us/en/institutional/etfs/state-street-spdr-sp-500-etf-trust-spy):

- [Product data](https://www.ssga.com/library-content/products/fund-data/etfs/us/spdr-product-data-us-en.xlsx): SPY identity, total net assets, gross expense ratio, and source date.
- [Daily holdings](https://www.ssga.com/library-content/products/fund-data/etfs/us/holdings-daily-us-en-spy.xlsx): SPY identity, holding symbols, weights, currencies, and source date.

No subscription or API key is required by this adapter. It makes two bounded
HTTPS requests per preparation, follows no redirects, and uses no silent fallback.
HTTP failure, changed workbook layout, wrong identity or units, malformed rows,
and invalid evidence stop preparation. Requests use the caller's cancellation
context and a 25-second timeout per file. No request is made for the default
corporate contract.

On September 23, 2026, both workbooks returned HTTP 200 from Kvant and NUC.
NUC's successful fetch completed at 03:47:41 UTC with the same byte hashes:

| File | SHA-256 |
| --- | --- |
| Product data | `c2ea3756909d266709d3a8bbaad3f4b9f866b3a866fb459bb111d69610212e1d` |
| Holdings | `1f8884ce4363bab4cab79a8ffb0a95077ede8d9c2d5cd00963491e715c5694aa` |

Both source dates were September 21. The workbook contained 505 holding rows
with total reported weight 100.193096%. These observations establish fetch and
parser behavior for those bytes, not continuing availability or trading quality.
Raw workbooks and the NUC normalized readback are retained in the preparation
worktree's ignored `var/spy-etf-preparation/` directory.

The existing NUC credentials returned FMP ETF-profile HTTP 402 and Finnhub
ETF-profile HTTP 403. No Alpha Vantage key was configured. Those failures were
not reclassified as passes; the candidate explicitly selects the issuer source.

## Versioned contract

Set `fundamentals_contract` to `spy-ssga-etf-v1` only on a new paper SPY strategy.
The stock market type, SPY ticker, USD currency, and ISIN `US78462F1030` must agree.
The selected and explicitly required analysts must include market, fundamentals,
and news. Empty or omitted contract retains the existing corporate checks.
Unknown contracts and live ETF execution are refused.

The issuer parser converts dollar values marked in millions to USD and percentage
values to fractions. It records **gross** expense ratio; it does not claim a net
expense ratio or infer corporate financial statements. Missing fees are distinct
from a valid zero fee. Required evidence is:

- Positive finite fund assets and a present finite gross expense ratio in [0, 1].
- Nonempty, bounded holdings with unique symbols and positive finite weights.
- Total reported holdings weight from 95% through 100.5%, without normalization.
- Both original HTTPS source URLs and SHA-256 digests.
- Retrieval no older than 24 hours, source dates no older than seven calendar
  days, and no future dates or source date later than retrieval.

The seven-day source window covers ordinary publication lag and weekends; it is
separate from the 24-hour retrieval bound and the existing market-data freshness
gate. The 0.5 percentage-point upper weight tolerance is an explicit policy
bound, not a claim that the observed excess is rounding. Review that bound and
the observed issuer total before approving the candidate. Tracking error is not
supplied and is not a V1 requirement. Market, news, risk, liquidity, backtest, and
research gates remain in force.

The ETF analyst receives fund metrics, source dates/hashes, and largest holdings.
It must state that corporate metrics and tracking error are unavailable. Full
holdings remain in the fundamentals snapshot. ETF failures use durable reason
`etf_fundamentals_invalid`, retained by the qualification collector.

## Preparation and verification

Build and inspect issuer evidence without database or trading access:

```sh
go build -o /tmp/augr-etf-evidence ./cmd/etf-evidence
/tmp/augr-etf-evidence > /tmp/spy-issuer-evidence.json
```

For repeatable preparation, retain the original workbooks and their actual
retrieval timestamp. Export the rejected strategy read-only through an existing
authorized interface. Then create a new directory under a local evidence root:

```sh
/tmp/augr-etf-evidence \
  --profile /path/to/spdr-product-data-us-en.xlsx \
  --holdings /path/to/holdings-daily-us-en-spy.xlsx \
  --fetched-at 2026-09-23T03:34:18Z \
  --source-strategy /path/to/source-strategy.json \
  --prepare-dir /path/to/new-candidate-directory
```

This produces `candidate.json`, `etf-evidence.json`, `receipt.json`, and
`manifest.json`. The proposal preserves the source configuration except for the
explicit ETF contract and required analyst roles. It has inactive status, no
schedule, no runtime execution-version ID, and `activation_allowed: false`.
Output under `/var/lib` or `/etc`, including through parent symlinks, is refused.
An existing output directory cannot be overwritten. The hash identifies the
proposal bytes; it is not a registered execution-version identity or a GO receipt.

Focused verification:

```sh
go test -race -short ./internal/data/... ./internal/agent/... ./cmd/tradingagent ./cmd/etf-evidence
go vet ./...
golangci-lint run ./...
python3 scripts/test-qualification.py
```

## Remaining qualification gates

1. Review and pin resolved global model/risk settings and prompt overrides in the
   new strategy configuration; inheritance must not hide a baseline change.
2. Pass exact-commit CI, review the source contract, and deploy a separately
   identified application candidate. The production app is still release
   `1022b401`; it does not implement this contract.
3. Create a new inactive strategy through the normal authenticated interface and
   read back its distinct immutable execution version. Do not edit the rejected
   strategy in place or send the proposal envelope directly to an API endpoint.
4. Review a point-in-time backtest. Today's issuer workbook cannot be backfilled
   into historical dates or counted as historical fundamentals evidence. The
   read-only fetch probe makes no profitability or backtest claim.
5. Schedule a new prospective cycle against the new application and strategy
   identities. Retain the old failed slots and resolve the separate options,
   history, and overnight-sweep blockers.
6. Only after all gates pass: freeze the new baseline, initialize its new ledger,
   and activate monitoring for that exact baseline.

Rollback before activation is to leave the new candidate inactive. The old
corporate strategy's configuration and receipts require no restoration because
they were not changed.
