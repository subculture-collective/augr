# Augr Follow-up Cleanups Implementation Plan

> **For agentic workers:** Execute this plan task-by-task. Recommended path:
> dispatch a fresh subagent per task, review each result with `review-quality`,
> then continue. For complex multi-agent splits, use
> `parallel-feature-development`, `team-composition-patterns`, and
> `team-communication-protocols`. Steps use checkbox (`- [ ]`) syntax for
> tracking.

**Goal:** Implement the four post-tranche follow-ups: latest-run DB index, active-only Polymarket job error semantics, queryable trade-decision prompt/LLM metadata, and non-stock order-path market-type cleanup.

**Architecture:** Keep this as four small, independently reviewable changes. Use additive schema migrations for the DB index, journal metadata columns, and order market-type persistence; preserve existing APIs as backward-compatible additions; and avoid inventing LLM provenance when metadata is absent. The order-path cleanup should propagate market type truthfully through orders and journal decisions without adding short-selling or changing broker behavior.

**Tech Stack:** Go 1.25, chi, pgx/PostgreSQL 17, golang-migrate migrations, React/Vite/TypeScript, TanStack Query, Vitest, Docker Compose production stack.

---

## Scope and ordering

Implement in this order:

1. **Schema/index foundation** — adds migration `000045` and bumps required schema version to `45`.
2. **Trade-decision prompt/LLM metadata** — persists and renders new nullable metadata fields.
3. **Polymarket active-failure semantics** — UI-only change to distinguish active from historical job errors.
4. **Non-stock order market-type cleanup** — add migration `000046`, bump required schema version to `46`, and propagate `plan.MarketType` through generic order creation and journal decisions.

Commit after each task if executing interactively, or after each reviewed subagent slice if using subagents.

## File map

- Create: `migrations/000045_strategy_latest_run_index_and_trade_decision_llm.up.sql`
- Create: `migrations/000045_strategy_latest_run_index_and_trade_decision_llm.down.sql`
- Create: `migrations/000046_orders_market_type.up.sql`
- Create: `migrations/000046_orders_market_type.down.sql`
- Modify: `internal/repository/postgres/schema_version.go`
- Modify: `internal/domain/trade_decision.go`
- Modify: `internal/repository/postgres/trade_decision_journal.go`
- Modify: `internal/repository/postgres/trade_decision_journal_test.go`
- Modify: `internal/execution/order_manager.go`
- Modify: `internal/execution/order_manager_test.go`
- Modify: `cmd/tradingagent/prod_strategy_runner.go`
- Modify: `cmd/tradingagent/runtime.go`
- Modify: `cmd/tradingagent/runtime_test.go`
- Modify: `internal/repository/postgres/order.go`
- Modify: `internal/repository/postgres/order_test.go`
- Modify: `internal/repository/interfaces.go`
- Modify: `internal/api/order_handlers.go`
- Modify: `web/src/lib/api/types.ts`
- Modify: `web/src/pages/decision-journal-page.tsx`
- Modify: `web/src/pages/decision-journal-page.test.tsx`
- Modify: `web/src/pages/polymarket-page.tsx`
- Modify: `web/src/pages/polymarket-page.test.tsx`
- Optional if API/client tests already cover order filters: `web/src/lib/api/client.test.ts`

---

### Task 1: Add schema migration for latest-run index and journal LLM columns

**Files:**
- Create: `migrations/000045_strategy_latest_run_index_and_trade_decision_llm.up.sql`
- Create: `migrations/000045_strategy_latest_run_index_and_trade_decision_llm.down.sql`
- Modify: `internal/repository/postgres/schema_version.go`
- Test: `cmd/tradingagent/schema_version_sync_test.go`

- [ ] **Step 1: Create the migration up file**

Write `migrations/000045_strategy_latest_run_index_and_trade_decision_llm.up.sql` with exactly these statements:

```sql
-- Speeds up StrategyRepo.List latest-run lateral lookup:
-- LEFT JOIN LATERAL pipeline_runs WHERE strategy_id = s.id ORDER BY started_at DESC, id DESC LIMIT 1.
CREATE INDEX IF NOT EXISTS idx_pipeline_runs_strategy_started_id
    ON pipeline_runs(strategy_id, started_at DESC, id DESC);

-- Queryable prompt/LLM metadata for the trade decision journal.
-- All fields are nullable because deterministic/rules/blocked decisions may not be LLM-backed.
ALTER TABLE trade_decisions
    ADD COLUMN IF NOT EXISTS prompt_text TEXT,
    ADD COLUMN IF NOT EXISTS llm_provider TEXT,
    ADD COLUMN IF NOT EXISTS llm_model TEXT,
    ADD COLUMN IF NOT EXISTS prompt_tokens INT,
    ADD COLUMN IF NOT EXISTS completion_tokens INT,
    ADD COLUMN IF NOT EXISTS latency_ms INT,
    ADD COLUMN IF NOT EXISTS cost_usd NUMERIC(20, 8);
```

- [ ] **Step 2: Create the migration down file**

Write `migrations/000045_strategy_latest_run_index_and_trade_decision_llm.down.sql` with exactly these statements:

```sql
ALTER TABLE trade_decisions
    DROP COLUMN IF EXISTS cost_usd,
    DROP COLUMN IF EXISTS latency_ms,
    DROP COLUMN IF EXISTS completion_tokens,
    DROP COLUMN IF EXISTS prompt_tokens,
    DROP COLUMN IF EXISTS llm_model,
    DROP COLUMN IF EXISTS llm_provider,
    DROP COLUMN IF EXISTS prompt_text;

DROP INDEX IF EXISTS idx_pipeline_runs_strategy_started_id;
```

- [ ] **Step 3: Bump required schema version**

In `internal/repository/postgres/schema_version.go`, change:

```go
const RequiredSchemaVersion = 44
```

to:

```go
const RequiredSchemaVersion = 45
```

- [ ] **Step 4: Run the schema sync test**

Run:

```bash
rtk go test ./cmd/tradingagent -run TestSchemaVersionSync
```

Expected: PASS. This proves latest `.up.sql` migration number equals `RequiredSchemaVersion`.

- [ ] **Step 5: Run migration filename/checksum sanity**

Run:

```bash
rtk git diff --check
rtk go test ./cmd/tradingagent -run 'SchemaVersion|ProductionDockerCompose|ProductionBuildVerification'
```

Expected: PASS. No whitespace errors and production verification tests still understand the migration sequence.

- [ ] **Step 6: Commit Task 1**

Run:

```bash
rtk git status --short
rtk git diff --check
git add migrations/000045_strategy_latest_run_index_and_trade_decision_llm.up.sql migrations/000045_strategy_latest_run_index_and_trade_decision_llm.down.sql internal/repository/postgres/schema_version.go
rtk git diff --cached --check
git commit -m "chore(db): add follow-up cleanup migration"
```

Expected: commit succeeds with only the migration/schema-version files staged.

---

### Task 2: Persist queryable prompt/LLM metadata in trade decisions

**Files:**
- Modify: `internal/domain/trade_decision.go`
- Modify: `internal/repository/postgres/trade_decision_journal.go`
- Modify: `internal/repository/postgres/trade_decision_journal_test.go`
- Modify: `internal/execution/order_manager.go`
- Modify: `cmd/tradingagent/prod_strategy_runner.go`
- Modify: `cmd/tradingagent/runtime.go`
- Modify: `internal/execution/order_manager_test.go`
- Modify: `web/src/lib/api/types.ts`
- Modify: `web/src/pages/decision-journal-page.tsx`
- Modify: `web/src/pages/decision-journal-page.test.tsx`

- [ ] **Step 1: Add failing repository scan coverage**

In `internal/repository/postgres/trade_decision_journal_test.go`, extend `TestScanTradeDecision_RoundTrip` so `fakeTradeDecisionScanner.values` includes the seven new fields immediately after `regimeTags` and before `paperOrderID`:

```go
promptText := "system: trade carefully\nuser: evaluate AAPL"
llmProvider := "openai"
llmModel := "gpt-4.1"
promptTokens := 123
completionTokens := 45
latencyMS := 678
costUSD := 0.0123
```

Add these values in the scanner fixture:

```go
&promptText,
&llmProvider,
&llmModel,
&promptTokens,
&completionTokens,
&latencyMS,
&costUSD,
```

Add assertions after the existing JSON/array checks:

```go
if got.PromptText != promptText || got.LLMProvider != llmProvider || got.LLMModel != llmModel {
    t.Fatalf("unexpected LLM string metadata: %+v", got)
}
if got.PromptTokens == nil || *got.PromptTokens != promptTokens {
    t.Fatalf("PromptTokens = %v, want %d", got.PromptTokens, promptTokens)
}
if got.CompletionTokens == nil || *got.CompletionTokens != completionTokens {
    t.Fatalf("CompletionTokens = %v, want %d", got.CompletionTokens, completionTokens)
}
if got.LatencyMS == nil || *got.LatencyMS != latencyMS {
    t.Fatalf("LatencyMS = %v, want %d", got.LatencyMS, latencyMS)
}
if got.CostUSD == nil || *got.CostUSD != costUSD {
    t.Fatalf("CostUSD = %v, want %f", got.CostUSD, costUSD)
}
```

Run:

```bash
rtk go test ./internal/repository/postgres -run TestScanTradeDecision_RoundTrip
```

Expected before implementation: FAIL with scan arity mismatch or missing fields on `domain.TradeDecision`.

- [ ] **Step 2: Add nullable metadata fields to the domain model**

In `internal/domain/trade_decision.go`, extend `TradeDecision` after `RegimeTags`:

```go
PromptText       string   `json:"prompt_text,omitempty"`
LLMProvider      string   `json:"llm_provider,omitempty"`
LLMModel         string   `json:"llm_model,omitempty"`
PromptTokens     *int     `json:"prompt_tokens,omitempty"`
CompletionTokens *int     `json:"completion_tokens,omitempty"`
LatencyMS        *int     `json:"latency_ms,omitempty"`
CostUSD          *float64 `json:"cost_usd,omitempty"`
```

Use pointers for numeric metadata so missing values are not confused with real zero.

- [ ] **Step 3: Update repository SELECT/INSERT/scan**

In `internal/repository/postgres/trade_decision_journal.go`, update `tradeDecisionSelectSQL` to include the new columns after `regime_tags`:

```go
const tradeDecisionSelectSQL = `SELECT id, strategy_id, pipeline_run_id, market_type, instrument_key,
        external_market_id, side, outcome, fair_value::double precision,
        executable_price::double precision, spread::double precision,
        depth::double precision, gross_ev::double precision, net_ev::double precision,
        kelly_fraction::double precision, proposed_size::double precision,
        approved_size::double precision, risk_status, risk_reasons, evidence,
        features, regime_tags, prompt_text, llm_provider, llm_model,
        prompt_tokens, completion_tokens, latency_ms, cost_usd::double precision,
        paper_order_id, live_order_id, status, created_at, updated_at
     FROM trade_decisions`
```

Update the `INSERT` column list and values so it writes the new columns:

```go
`INSERT INTO trade_decisions (
    strategy_id, pipeline_run_id, market_type, instrument_key, external_market_id,
    side, outcome, fair_value, executable_price, spread, depth, gross_ev,
    net_ev, kelly_fraction, proposed_size, approved_size, risk_status,
    risk_reasons, evidence, features, regime_tags, prompt_text, llm_provider,
    llm_model, prompt_tokens, completion_tokens, latency_ms, cost_usd,
    paper_order_id, live_order_id, status
)
 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
         $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28,
         $29, $30, $31)
 RETURNING id, created_at, updated_at`
```

Pass the new arguments between `RegimeTags` and order IDs:

```go
nullString(decision.PromptText),
nullString(decision.LLMProvider),
nullString(decision.LLMModel),
nullableInt(decision.PromptTokens),
nullableInt(decision.CompletionTokens),
nullableInt(decision.LatencyMS),
nullableFloat(decision.CostUSD),
```

Add helpers near the existing helper functions:

```go
func nullableInt(value *int) any {
    if value == nil {
        return nil
    }
    return *value
}

func nullableFloat(value *float64) any {
    if value == nil {
        return nil
    }
    return *value
}
```

Update `scanTradeDecision` with nullable locals:

```go
var (
    promptText       *string
    llmProvider      *string
    llmModel         *string
    promptTokens     *int
    completionTokens *int
    latencyMS        *int
    costUSD          *float64
)
```

Scan those values after `&regimeTags` and before `&paperOrderID`. Then assign:

```go
if promptText != nil {
    decision.PromptText = *promptText
}
if llmProvider != nil {
    decision.LLMProvider = *llmProvider
}
if llmModel != nil {
    decision.LLMModel = *llmModel
}
decision.PromptTokens = promptTokens
decision.CompletionTokens = completionTokens
decision.LatencyMS = latencyMS
decision.CostUSD = costUSD
```

- [ ] **Step 4: Add execution decision metadata type and copy into journal decisions**

In `internal/execution/order_manager.go`, add this type near `TradingPlan`:

```go
type DecisionMetadata struct {
    PromptText       string   `json:"prompt_text,omitempty"`
    LLMProvider      string   `json:"llm_provider,omitempty"`
    LLMModel         string   `json:"llm_model,omitempty"`
    PromptTokens     *int     `json:"prompt_tokens,omitempty"`
    CompletionTokens *int     `json:"completion_tokens,omitempty"`
    LatencyMS        *int     `json:"latency_ms,omitempty"`
    CostUSD          *float64 `json:"cost_usd,omitempty"`
}
```

Add it to `TradingPlan`:

```go
DecisionMetadata *DecisionMetadata `json:"decision_metadata,omitempty"`
```

In `newTradeDecision`, after constructing `decision`, copy metadata only when present:

```go
if plan.DecisionMetadata != nil {
    decision.PromptText = strings.TrimSpace(plan.DecisionMetadata.PromptText)
    decision.LLMProvider = strings.TrimSpace(plan.DecisionMetadata.LLMProvider)
    decision.LLMModel = strings.TrimSpace(plan.DecisionMetadata.LLMModel)
    decision.PromptTokens = plan.DecisionMetadata.PromptTokens
    decision.CompletionTokens = plan.DecisionMetadata.CompletionTokens
    decision.LatencyMS = plan.DecisionMetadata.LatencyMS
    decision.CostUSD = plan.DecisionMetadata.CostUSD
}
```

Add a small helper for pointer cloning if tests mutate source metadata:

```go
func cloneIntPtr(value *int) *int {
    if value == nil {
        return nil
    }
    cloned := *value
    return &cloned
}

func cloneFloatPtr(value *float64) *float64 {
    if value == nil {
        return nil
    }
    cloned := *value
    return &cloned
}
```

If using clone helpers, assign cloned pointers in `newTradeDecision`.

- [ ] **Step 5: Populate execution metadata from persisted trader agent decision**

In `cmd/tradingagent/prod_strategy_runner.go`, add a helper method on `realStrategyRunner`:

```go
func (r *realStrategyRunner) executionDecisionMetadata(ctx context.Context, runID uuid.UUID) *execution.DecisionMetadata {
    if r == nil || r.decisionRepo == nil || runID == uuid.Nil {
        return nil
    }

    decisions, err := r.decisionRepo.GetByRun(ctx, runID, repository.AgentDecisionFilter{
        AgentRole: domain.AgentRoleTrader,
        Phase:     domain.PhaseTrading,
    }, 1, 0)
    if err != nil || len(decisions) == 0 {
        if err != nil {
            r.logger.WarnContext(ctx, "load trader decision metadata", "error", err, "run_id", runID)
        }
        return nil
    }

    decision := decisions[0]
    metadata := &execution.DecisionMetadata{
        PromptText:  decision.PromptText,
        LLMProvider: decision.LLMProvider,
        LLMModel:    decision.LLMModel,
    }
    if decision.PromptTokens > 0 {
        value := decision.PromptTokens
        metadata.PromptTokens = &value
    }
    if decision.CompletionTokens > 0 {
        value := decision.CompletionTokens
        metadata.CompletionTokens = &value
    }
    if decision.LatencyMS > 0 {
        value := decision.LatencyMS
        metadata.LatencyMS = &value
    }
    if decision.CostUSD > 0 {
        value := decision.CostUSD
        metadata.CostUSD = &value
    }

    if strings.TrimSpace(metadata.PromptText) == "" &&
        strings.TrimSpace(metadata.LLMProvider) == "" &&
        strings.TrimSpace(metadata.LLMModel) == "" &&
        metadata.PromptTokens == nil && metadata.CompletionTokens == nil &&
        metadata.LatencyMS == nil && metadata.CostUSD == nil {
        return nil
    }
    return metadata
}
```

Use it before calling `ProcessSignal`:

```go
decisionMetadata := r.executionDecisionMetadata(ctx, run.ID)
```

Set the new field in the `execution.TradingPlan` literal:

```go
DecisionMetadata: decisionMetadata,
```

For `cmd/tradingagent/runtime.go`, set no metadata in the smoke runner unless a matching decision repository is available there. Missing metadata must remain absent, not faked.

- [ ] **Step 6: Add backend tests for journal metadata propagation**

In `internal/execution/order_manager_test.go`, add a test using `mockDecisionRecorder`:

```go
func TestProcessSignal_RecordsTradeDecisionWithLLMMetadata(t *testing.T) {
    broker := &mockBroker{}
    riskEng := &mockRiskEngine{}
    orderRepo := &mockOrderRepo{}
    positionRepo := &mockPositionRepo{}
    tradeRepo := &mockTradeRepo{}
    auditRepo := &mockAuditLogRepo{}
    recorder := &mockDecisionRecorder{}
    mgr := newTestOrderManager(broker, riskEng, orderRepo, positionRepo, tradeRepo, auditRepo).WithDecisionRecorder(recorder)

    promptTokens := 123
    completionTokens := 45
    latencyMS := 678
    costUSD := 0.0123
    plan := defaultPlan()
    plan.DecisionMetadata = &execution.DecisionMetadata{
        PromptText:       "system: trade carefully",
        LLMProvider:      "openai",
        LLMModel:         "gpt-4.1",
        PromptTokens:     &promptTokens,
        CompletionTokens: &completionTokens,
        LatencyMS:        &latencyMS,
        CostUSD:          &costUSD,
    }

    if err := mgr.ProcessSignal(context.Background(), defaultSignal(), plan, uuid.New(), uuid.New()); err != nil {
        t.Fatalf("ProcessSignal() error = %v", err)
    }
    if len(recorder.decisions) == 0 {
        t.Fatal("expected recorded trade decision")
    }
    decision := recorder.decisions[0]
    if decision.PromptText != plan.DecisionMetadata.PromptText || decision.LLMProvider != "openai" || decision.LLMModel != "gpt-4.1" {
        t.Fatalf("unexpected LLM strings: %+v", decision)
    }
    if decision.PromptTokens == nil || *decision.PromptTokens != promptTokens {
        t.Fatalf("PromptTokens = %v, want %d", decision.PromptTokens, promptTokens)
    }
    if decision.LatencyMS == nil || *decision.LatencyMS != latencyMS {
        t.Fatalf("LatencyMS = %v, want %d", decision.LatencyMS, latencyMS)
    }
}
```

Run:

```bash
rtk go test ./internal/execution ./internal/repository/postgres
```

Expected: PASS.

- [ ] **Step 7: Add frontend type fields**

In `web/src/lib/api/types.ts`, extend `TradeDecision` after `regime_tags`:

```ts
prompt_text?: string;
llm_provider?: string;
llm_model?: string;
prompt_tokens?: number;
completion_tokens?: number;
latency_ms?: number;
cost_usd?: number;
```

- [ ] **Step 8: Render metadata in Decision Journal rows/cards**

In `web/src/pages/decision-journal-page.tsx`, add helpers near `safeMoneyLabel`:

```ts
function safeNumberLabel(value?: number | null) {
  return typeof value === 'number' && Number.isFinite(value) ? value.toLocaleString() : 'n/a'
}

function safeLatencyLabel(value?: number | null) {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? `${value}ms` : 'n/a'
}

function safeCostLabel(value?: number | null) {
  return typeof value === 'number' && Number.isFinite(value)
    ? new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', minimumFractionDigits: 4, maximumFractionDigits: 4 }).format(value)
    : 'n/a'
}
```

Add a small component:

```tsx
function DecisionLLMMetadata({ decision }: { decision: TradeDecision }) {
  const provider = decision.llm_provider?.trim() || 'n/a'
  const model = decision.llm_model?.trim() || 'n/a'
  const prompt = decision.prompt_text?.trim()

  return (
    <div data-testid="decision-llm-metadata" className="mt-3 border border-border bg-muted/20 p-3 text-xs text-muted-foreground">
      <div className="mb-2 text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Prompt / LLM</div>
      <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
        <div><span className="uppercase tracking-[0.16em]">Provider</span><div className="font-medium text-foreground">{provider}</div></div>
        <div><span className="uppercase tracking-[0.16em]">Model</span><div className="font-medium text-foreground">{model}</div></div>
        <div><span className="uppercase tracking-[0.16em]">Tokens</span><div className="font-medium text-foreground">{safeNumberLabel(decision.prompt_tokens)} / {safeNumberLabel(decision.completion_tokens)}</div></div>
        <div><span className="uppercase tracking-[0.16em]">Latency / Cost</span><div className="font-medium text-foreground">{safeLatencyLabel(decision.latency_ms)} · {safeCostLabel(decision.cost_usd)}</div></div>
      </div>
      {prompt ? <pre className="mt-3 max-h-32 overflow-auto whitespace-pre-wrap border border-border bg-background/60 p-2 font-mono text-[11px]">{prompt}</pre> : <p className="mt-3">No prompt text recorded for this decision.</p>}
    </div>
  )
}
```

Render `<DecisionLLMMetadata decision={decision} />` inside both `DecisionRow` and `DecisionCard`, near the risk reasons/order links. Keep the table readable by placing metadata in the action/status cell or adding a second row immediately after each decision row if the row becomes too wide.

- [ ] **Step 9: Add journal UI tests**

In `web/src/pages/decision-journal-page.test.tsx`, add metadata to `decisionFixture`:

```ts
prompt_text: 'system: trade carefully',
llm_provider: 'openai',
llm_model: 'gpt-4.1',
prompt_tokens: 123,
completion_tokens: 45,
latency_ms: 678,
cost_usd: 0.0123,
```

Add a test:

```tsx
it('renders prompt and LLM metadata when recorded', async () => {
  apiClientMock.getRiskStatus.mockResolvedValue(riskStatus)
  apiClientMock.listTradeDecisions.mockResolvedValue({ data: [decisionFixture] })

  render(<DecisionJournalPage />, { wrapper: Wrapper })

  const metadata = await screen.findAllByTestId('decision-llm-metadata')
  expect(metadata[0]).toHaveTextContent('openai')
  expect(metadata[0]).toHaveTextContent('gpt-4.1')
  expect(metadata[0]).toHaveTextContent('123 / 45')
  expect(metadata[0]).toHaveTextContent('678ms')
  expect(metadata[0]).toHaveTextContent('system: trade carefully')
})
```

Add a second fixture by spreading `decisionFixture` and deleting metadata fields, then assert it shows `No prompt text recorded for this decision.` and `n/a`.

- [ ] **Step 10: Validate and commit Task 2**

Run:

```bash
rtk go test ./internal/execution ./internal/repository/postgres ./cmd/tradingagent
cd web && rtk npm test -- --run src/pages/decision-journal-page.test.tsx && rtk lint src/pages/decision-journal-page.tsx src/pages/decision-journal-page.test.tsx src/lib/api/types.ts && rtk npm run build
rtk git diff --check
```

Expected: all pass; Vite may still print the known chunk-size warning.

Commit:

```bash
git add internal/domain/trade_decision.go internal/repository/postgres/trade_decision_journal.go internal/repository/postgres/trade_decision_journal_test.go internal/execution/order_manager.go internal/execution/order_manager_test.go cmd/tradingagent/prod_strategy_runner.go cmd/tradingagent/runtime.go web/src/lib/api/types.ts web/src/pages/decision-journal-page.tsx web/src/pages/decision-journal-page.test.tsx
rtk git diff --cached --check
git commit -m "feat(journal): persist trade decision llm metadata"
```

---

### Task 3: Align Polymarket job badges with active-failure semantics

**Files:**
- Modify: `web/src/pages/polymarket-page.tsx`
- Modify: `web/src/pages/polymarket-page.test.tsx`

- [ ] **Step 1: Add failing UI coverage for historical errors without active failure**

In `web/src/pages/polymarket-page.test.tsx`, add a test case after `job state details render`:

```tsx
it('does not mark historical job errors as active failures', async () => {
  mockBaseResponses()
  apiMocks.getPolymarketJobsStatus.mockResolvedValue([
    {
      name: 'polymarket_profiles',
      description: 'Refresh tracked wallet profiles',
      schedule: 'Every 20 minutes',
      last_run: '2025-01-02T00:00:00Z',
      last_result: 'success',
      run_count: 12,
      error_count: 3,
      consecutive_failures: 0,
      running: false,
      enabled: true,
    },
  ])

  render(<PolymarketPage />, { wrapper: Wrapper })

  const jobCard = await screen.findByTestId('polymarket-job-polymarket_profiles')
  expect(within(jobCard).queryByText('error')).not.toBeInTheDocument()
  expect(within(jobCard).getByText('stable')).toBeInTheDocument()
  expect(within(jobCard).getByText(/Historical errors: 3/i)).toBeInTheDocument()
})
```

Run:

```bash
cd web && rtk npm test -- --run src/pages/polymarket-page.test.tsx
```

Expected before implementation: FAIL because the card still renders the `error` badge when `error_count > 0`.

- [ ] **Step 2: Implement active vs historical helper functions**

In `web/src/pages/polymarket-page.tsx`, add helpers near `formatScheduleDisplay`:

```ts
function hasActiveJobFailure(job: JobStatus) {
  return Boolean(job.last_error?.trim()) || (job.consecutive_failures ?? 0) > 0
}

function hasHistoricalJobFailures(job: JobStatus) {
  return job.error_count > 0
}
```

In the job-card map, replace:

```ts
const hasError = Boolean(job.last_error) || job.error_count > 0
const stateTone: 'success' | 'outline' | 'warning' | 'secondary' = job.running ? 'success' : !job.enabled ? 'outline' : hasError ? 'warning' : 'secondary'
```

with:

```ts
const hasActiveFailure = hasActiveJobFailure(job)
const hasHistoricalFailures = hasHistoricalJobFailures(job)
const stateTone: 'success' | 'outline' | 'warning' | 'secondary' = job.running ? 'success' : !job.enabled ? 'outline' : hasActiveFailure ? 'warning' : 'secondary'
```

Replace the badge rendering:

```tsx
{hasError ? <Badge variant="warning">error</Badge> : <Badge variant="secondary">stable</Badge>}
```

with:

```tsx
{hasActiveFailure ? <Badge variant="warning">active failure</Badge> : <Badge variant="secondary">stable</Badge>}
```

Below the error-count field, add truthful historical copy:

```tsx
{hasHistoricalFailures && !hasActiveFailure ? (
  <div className="col-span-2 text-muted-foreground">Historical errors: {job.error_count}. Latest state is not failing.</div>
) : null}
```

Keep `last_error` visible as an active failure detail.

- [ ] **Step 3: Update existing job-state test expectations**

In the existing `job state details render` test fixture, keep `last_error` and `consecutive_failures: 1`. Change the assertion from:

```ts
expect(within(jobCard).getByText('error')).toBeInTheDocument()
```

to:

```ts
expect(within(jobCard).getByText('active failure')).toBeInTheDocument()
```

- [ ] **Step 4: Validate and commit Task 3**

Run:

```bash
cd web && rtk npm test -- --run src/pages/polymarket-page.test.tsx
cd web && rtk lint src/pages/polymarket-page.tsx src/pages/polymarket-page.test.tsx
cd web && rtk npm run build
rtk git diff --check
```

Expected: all pass; Vite may still print the known chunk-size warning.

Commit:

```bash
git add web/src/pages/polymarket-page.tsx web/src/pages/polymarket-page.test.tsx
rtk git diff --cached --check
git commit -m "fix(ui): clarify polymarket job failure state"
```

---

### Task 4: Propagate market type through the generic order path

**Files:**
- Create: `migrations/000046_orders_market_type.up.sql`
- Create: `migrations/000046_orders_market_type.down.sql`
- Modify: `internal/repository/postgres/schema_version.go`
- Modify: `internal/domain/order.go`
- Modify: `internal/repository/postgres/order.go`
- Modify: `internal/repository/postgres/order_test.go`
- Modify: `internal/repository/interfaces.go`
- Modify: `internal/api/order_handlers.go`
- Modify: `internal/execution/order_manager.go`
- Modify: `internal/execution/order_manager_test.go`
- Modify: `web/src/lib/api/types.ts`
- Optional: `web/src/lib/api/client.test.ts`

> This task intentionally does **not** implement short-selling, new broker semantics, or market-specific fill accounting. It fixes the lying `MarketTypeStock` defaults in the generic order/journal path so non-stock orders and decisions carry their declared market type.

- [ ] **Step 1: Create the order market-type migration**

Write `migrations/000046_orders_market_type.up.sql` with exactly these statements:

```sql
-- Persist the market type already present on the domain.Order model.
-- Existing rows are stock by default because old order storage was stock-centric.
ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS market_type market_type NOT NULL DEFAULT 'stock';

CREATE INDEX IF NOT EXISTS idx_orders_market_type_created
    ON orders(market_type, created_at DESC);
```

Write `migrations/000046_orders_market_type.down.sql` with exactly these statements:

```sql
DROP INDEX IF EXISTS idx_orders_market_type_created;

ALTER TABLE orders
    DROP COLUMN IF EXISTS market_type;
```

In `internal/repository/postgres/schema_version.go`, change:

```go
const RequiredSchemaVersion = 45
```

to:

```go
const RequiredSchemaVersion = 46
```

Run:

```bash
rtk go test ./cmd/tradingagent -run TestSchemaVersionSync
```

Expected: PASS. This proves latest migration number equals `RequiredSchemaVersion` after adding the order schema change.

- [ ] **Step 2: Add failing repository tests for order market type**

In `internal/repository/postgres/order_test.go`, add or update scanner/list query tests so they expect `market_type` in `orderSelectSQL` and the scan roundtrip. Add a test like:

```go
func TestBuildOrderListQuery_MarketTypeFilter(t *testing.T) {
    query, args := buildOrderListQuery(repository.OrderFilter{MarketType: domain.MarketTypeCrypto}, 25, 0)
    assertContains(t, query, "market_type = $1")
    assertContains(t, query, "LIMIT $2")
    if len(args) != 2 || args[0] != domain.MarketTypeCrypto || args[1] != 25 {
        t.Fatalf("unexpected args: %#v", args)
    }
}
```

Run:

```bash
rtk go test ./internal/repository/postgres -run 'Order|BuildOrder'
```

Expected before implementation: FAIL because `OrderFilter` lacks `MarketType` and order SQL omits it.

- [ ] **Step 3: Update repository/API order contract**

In `internal/repository/interfaces.go`, add to `OrderFilter`:

```go
MarketType domain.MarketType
```

In `internal/repository/postgres/order.go`:

1. Insert `market_type` after `ticker` in `Create` and pass `order.MarketType.Normalize()`.
2. Update `UPDATE orders SET` to include `market_type`.
3. Update `orderSelectSQL` to select `market_type` after `ticker`.
4. Update `scanOrder` to scan into `&order.MarketType` and default blank to stock for defensive compatibility:

```go
if order.MarketType == "" {
    order.MarketType = domain.MarketTypeStock
}
```

5. Update order list filter builder to include:

```go
if filter.MarketType != "" {
    conditions = append(conditions, "market_type = "+nextArg(filter.MarketType))
}
```

In `internal/api/order_handlers.go`, parse `market_type`:

```go
if !ParseEnumParam(w, q, "market_type", &filter.MarketType) {
    return
}
```

In `web/src/lib/api/types.ts`, add to `Order`:

```ts
market_type?: MarketType;
```

If `OrderListParams` exists in `web/src/lib/api/types.ts`, add:

```ts
market_type?: MarketType;
```

- [ ] **Step 4: Stop hard-coding stock in OrderManager**

In `internal/execution/order_manager.go`, compute market type once after the stock SELL guard:

```go
marketType := planMarketType(plan)
```

Replace every `domain.MarketTypeStock` inside `ProcessSignal` after that point with `marketType` where the value represents the current plan:

```go
m.recordTradeDecision(ctx, m.newTradeDecision(
    strategyID,
    runID,
    plan,
    marketType,
    strings.ToUpper(strings.TrimSpace(plan.Side)),
    0,
    0,
    domain.RiskDecisionRejected,
    []string{denial.Code + ": " + denial.Message},
    domain.TradeDecisionStatusRejected,
))
```

For order creation, replace:

```go
MarketType:     domain.MarketTypeStock,
```

with:

```go
MarketType:     marketType,
```

For risk-rejection decisions before order creation, pass `marketType`. For decisions after an order exists, continue passing `order.MarketType`.

- [ ] **Step 5: Add execution tests for non-stock market type propagation**

In `internal/execution/order_manager_test.go`, extend `TestProcessSignal_NonStockSellWithoutOpenLongIsNotStockGuarded` assertions:

```go
if got := orderRepo.orders[0].MarketType; got != domain.MarketTypeCrypto {
    t.Fatalf("order MarketType = %q, want %q", got, domain.MarketTypeCrypto)
}
if len(recorder.decisions) == 0 {
    t.Fatal("expected trade decision")
}
if got := recorder.decisions[0].MarketType; got != domain.MarketTypeCrypto {
    t.Fatalf("decision MarketType = %q, want %q", got, domain.MarketTypeCrypto)
}
```

Add one live-gate denial test for crypto if existing helpers make it easy:

```go
func TestProcessSignal_LiveGateDenialPreservesPlanMarketType(t *testing.T) {
    broker := &mockBroker{}
    riskEng := &mockRiskEngine{}
    orderRepo := &mockOrderRepo{}
    positionRepo := &mockPositionRepo{}
    tradeRepo := &mockTradeRepo{}
    auditRepo := &mockAuditLogRepo{}
    recorder := &mockDecisionRecorder{}
    mgr := newTestOrderManager(broker, riskEng, orderRepo, positionRepo, tradeRepo, auditRepo).
        WithDecisionRecorder(recorder).
        WithLiveTrading(true).
        WithLiveGate(execution.LiveGateConfig{EnableLiveTrading: true})

    plan := defaultPlan()
    plan.MarketType = domain.MarketTypeCrypto
    plan.Ticker = "BTCUSD"

    err := mgr.ProcessSignal(context.Background(), defaultSignal(), plan, uuid.New(), uuid.New())
    if err == nil {
        t.Fatal("ProcessSignal() error = nil, want live gate denial")
    }
    if len(recorder.decisions) != 1 {
        t.Fatalf("recorded decisions = %d, want 1", len(recorder.decisions))
    }
    if recorder.decisions[0].MarketType != domain.MarketTypeCrypto {
        t.Fatalf("decision MarketType = %q, want crypto", recorder.decisions[0].MarketType)
    }
}
```

- [ ] **Step 6: Validate and commit Task 4**

Run:

```bash
rtk go test ./internal/execution ./internal/repository/postgres ./internal/api ./cmd/tradingagent
cd web && rtk npm test -- --run src/lib/api/client.test.ts && rtk npm run build
rtk git diff --check
```

Expected: all pass; Vite may still print the known chunk-size warning.

Commit:

```bash
git add migrations/000046_orders_market_type.up.sql migrations/000046_orders_market_type.down.sql internal/repository/postgres/schema_version.go internal/domain/order.go internal/repository/postgres/order.go internal/repository/postgres/order_test.go internal/repository/interfaces.go internal/api/order_handlers.go internal/execution/order_manager.go internal/execution/order_manager_test.go web/src/lib/api/types.ts web/src/lib/api/client.test.ts
rtk git diff --cached --check
git commit -m "fix(execution): preserve order market types"
```

---

### Task 5: Full validation and deployment handoff

**Files:**
- No source edits expected.

- [ ] **Step 1: Run full backend tests**

Run:

```bash
rtk go test ./...
```

Expected: PASS.

- [ ] **Step 2: Run full frontend checks**

Run from repo root:

```bash
cd web && rtk lint && rtk npm run build && rtk npm test -- --run
```

Expected: PASS. The known Vite chunk-size warning and known jsdom navigation warning are acceptable if no tests fail.

- [ ] **Step 3: Run final git checks**

Run:

```bash
rtk git status --short
rtk git diff --check
rtk git log --oneline -10
```

Expected: clean status after all commits; no whitespace errors; latest commits correspond to this plan.

- [ ] **Step 4: Push when explicitly requested**

Run only after user asks to push:

```bash
rtk git push origin main
```

Expected: push succeeds.

- [ ] **Step 5: Deploy when explicitly requested**

Run only after user asks to deploy:

```bash
docker compose --project-name augr-prod -f docker-compose.prod.yml up -d --build app
rtk docker run --rm --env-file .env --network augr-prod_backend -v "/srv/server/projects/augr/migrations:/migrations:ro" --entrypoint /bin/sh migrate/migrate -c 'migrate -path=/migrations -database "postgres://${POSTGRES_USER:-postgres}:${POSTGRES_PASSWORD}@postgres:5432/${POSTGRES_DB:-tradingagent}?sslmode=disable" up'
docker compose --project-name augr-prod -f docker-compose.prod.yml restart app
curl -fsS http://127.0.0.1:8080/healthz
docker compose --project-name augr-prod -f docker-compose.prod.yml exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc "SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1;"'
```

Expected health body:

```json
{"status":"ok","db":"ok","redis":"ok"}
```

Expected schema version: `46`.

---

## Self-review checklist

- [x] Covers all 4 requested follow-ups.
- [x] Keeps no-fake-LLM rule by storing nullable metadata only when known.
- [x] Keeps Polymarket job errors truthful by separating active and historical failures.
- [x] Keeps order cleanup bounded to market-type propagation, not short-selling or broker rewrite.
- [x] Includes migration, schema-version, backend, frontend, tests, validation, commit, push, and deploy steps.
