CREATE TABLE IF NOT EXISTS polymarket_discovery_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    status TEXT NOT NULL CHECK (status IN ('running', 'completed', 'failed')),
    phase TEXT NOT NULL CHECK (phase IN ('screen', 'propose', 'deploy', 'done')),
    candidate_index INTEGER NOT NULL DEFAULT 0 CHECK (candidate_index >= 0),
    candidates JSONB NOT NULL DEFAULT '[]'::jsonb,
    accepted JSONB NOT NULL DEFAULT '[]'::jsonb,
    deployed JSONB NOT NULL DEFAULT '[]'::jsonb,
    errors JSONB NOT NULL DEFAULT '[]'::jsonb,
    summary JSONB NOT NULL DEFAULT '{}'::jsonb,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_polymarket_discovery_runs_active
    ON polymarket_discovery_runs (status, updated_at DESC)
    WHERE status = 'running';

CREATE INDEX IF NOT EXISTS idx_polymarket_discovery_runs_started_at
    ON polymarket_discovery_runs (started_at DESC);
