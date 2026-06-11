ALTER TABLE trade_decisions
    DROP COLUMN IF EXISTS cost_usd,
    DROP COLUMN IF EXISTS latency_ms,
    DROP COLUMN IF EXISTS completion_tokens,
    DROP COLUMN IF EXISTS prompt_tokens,
    DROP COLUMN IF EXISTS llm_model,
    DROP COLUMN IF EXISTS llm_provider,
    DROP COLUMN IF EXISTS prompt_text;

DROP INDEX IF EXISTS idx_pipeline_runs_strategy_started_id;
