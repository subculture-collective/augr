CREATE TABLE IF NOT EXISTS overnight_backtest_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    status TEXT NOT NULL CHECK (status IN ('running', 'completed', 'failed')),
    phase TEXT NOT NULL CHECK (phase IN ('screen', 'generate', 'sweep_validate_deploy', 'done')),
    candidate_index INTEGER NOT NULL DEFAULT 0 CHECK (candidate_index >= 0),
    candidates JSONB NOT NULL DEFAULT '[]'::jsonb,
    generated JSONB NOT NULL DEFAULT '[]'::jsonb,
    errors JSONB NOT NULL DEFAULT '[]'::jsonb,
    summary JSONB NOT NULL DEFAULT '{}'::jsonb,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_overnight_backtest_runs_active
    ON overnight_backtest_runs (status, updated_at DESC)
    WHERE status = 'running';

CREATE INDEX IF NOT EXISTS idx_overnight_backtest_runs_started_at
    ON overnight_backtest_runs (started_at DESC);
