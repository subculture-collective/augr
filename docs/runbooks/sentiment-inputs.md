# Sentiment inputs and source coverage

## Verified observations, September 24, 2026

Read-only probes ran from NUC using the application's existing API credentials
where needed. No credential values or post bodies were retained in this report.

| Input | Observation | Current use |
| --- | --- | --- |
| StockTwits symbol stream | HTTP 200 for SPY | Labeled bullish/bearish messages; labels are a subset of posts |
| Reddit r/stocks RSS | HTTP 200 | Public discussion classified by the configured quick LLM |
| Reddit r/ETFs and r/dividends RSS | HTTP 200, 25 entries each | Added to the default public feed corpus |
| Reddit r/ValueInvesting and r/SecurityAnalysis RSS | HTTP 429 | Not added to defaults; existing provider cooldown remains in force |
| Bluesky public search | HTTP 403 | Unavailable from this runtime; profile lookup still returned 200 |
| Finnhub social sentiment | HTTP 403 with the configured key | Existing key does not provide usable endpoint access in this probe |
| Polygon news for SPY | HTTP 200, five articles, all with insights | Existing news adapter already parses ticker-specific sentiment |

The running app had no Bluesky credentials configured. Its Alpha Vantage key was
empty. NewsAPI, Finnhub, and Polygon keys were configured; that does not establish
entitlement to every endpoint. The persisted social table contained only
StockTwits rows at inspection; this is not proof of an end-to-end analyst run.

## Implemented repair

The aggregator previously discarded observations timestamped after the request
cutoff. StockTwits, Reddit, and Bluesky generated their timestamp after fetching
or scoring, so usable current observations could be filtered out. These adapters
now use included message/post timestamps. StockTwits filters messages to the
requested window; Reddit rejects undated posts; Bluesky supplies search bounds
and still filters returned post timestamps locally. The strict shared time filter
remains in place. `MeasuredAt` is the latest included observation timestamp.

Source failures now return errors alongside usable results. Scheduled collection
persists partial results and marks provider-only partial coverage degraded; a
persistence failure remains a hard error. The runner keeps usable sentiment and
marks it partial. Analyst prompts identify contributing sources, flag incomplete
coverage, and describe post counts as a bounded sample rather than total platform
activity. Partial aggregates are not cached as complete coverage.

Reddit defaults are wallstreetbets, stocks, investing, options, ETFs, and dividends.
Ticker-boundary matching prefilters the shared corpus, duplicate URLs are removed,
and candidates are ordered by recency before the existing 30-post classifier cap.
The LLM still checks relevance. This improves which posts receive the fixed
classification budget; it does not claim representative coverage of Reddit or
comments. Feed and classifier failures remain visible, including cached failures
and provider cooldowns. A successful query with no matching posts remains empty.

Bluesky uses the documented public AppView host and reports denied access explicitly.
After HTTP 401/403, a 15-minute cooldown avoids repeating a rejected search for each
ticker. Changing public hosts did not repair live search. No authenticated session
support or credentials were added in this change. The official
[search lexicon](https://github.com/bluesky-social/atproto/blob/main/lexicons/app/bsky/feed/searchPosts.json)
allows implementations to require authentication. A future authenticated path
must use the account's PDS and normal session handling, followed by a real search
probe; an HTTP 403 alone does not prove authentication will solve every access rule.

## Expansion order using existing credentials and public APIs

1. Qualify the repaired StockTwits and six-feed Reddit path in a natural scheduled
   scan and an analyst run. Confirm observations survive the cutoff, partial
   coverage is reported, and source counts match saved rows.
2. Broaden the news input with already collected, triaged RSS articles and the
   existing Polygon ticker sentiment. The current `DataService.GetNews` uses a
   first-success provider chain, so multiple configured providers do not imply
   combined coverage. Merge by canonical article URL, preserve publisher and
   freshness, and keep news tone separate from retail post sentiment. Polygon's
   [news documentation](https://massive.com/docs/rest/stocks/overview) describes
   article-level ticker insights; the existing runtime key returned them.
3. Consider public GDELT for macro/sector narrative coverage only after measuring
   relevance and duplication. Its [DOC API tone](https://blog.gdeltproject.org/gdelt-doc-2-0-api-debuts/)
   describes document emotion, not a validated directional forecast for a ticker.
4. Resume Bluesky search once an existing account's normal authenticated access
   can be configured and tested. Do not add paid Finnhub social access or a new
   commercial source without a separate decision.

No new raw-post persistence, migration, app deployment, or production restart is
part of this source change. Existing bounded caches and snapshot persistence are
used. Source checks and HTTP probes are separate from runtime adoption and trading
qualification.

## Local verification

Verified in the installed `t3-dev --profile augr` environment on Kvant, from the
existing `t3code/add-position-awareness` checkout. Focused provider, automation,
analyst, and runner tests passed. The required backend build, vet, full short race
suite, CI-pinned golangci-lint 2.13.2, frontend lint/tests, and frontend build passed.
The image lacks Task and golangci-lint, so Task's commands were expanded and a
checksum-verified linter was copied temporarily into the isolated test snapshot.
An unused test parameter was corrected after the first lint run; the changed
Bluesky package's race tests and lint were rerun successfully. Temporary tooling
was removed. No preview was started and other threads' environments were untouched.

A read-only credential inventory found no configured Bluesky keys in the bounded
project/environment configuration searched on Kvant, NUC, Dozor, Almaz, and Soyuz,
or in inspected Linux container environments. Browser sessions, password-manager
vaults, encrypted application stores, and inaccessible paths were not searched.
Create a dedicated `augr-sentiment` app password through
[Bluesky settings](https://bsky.app/settings/app-passwords) if no existing entry is
available. Keep it in the secret store; adding it alone will not activate search
until authenticated PDS session support is implemented and verified.
