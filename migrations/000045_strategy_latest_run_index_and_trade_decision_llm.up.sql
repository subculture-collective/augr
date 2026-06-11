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
