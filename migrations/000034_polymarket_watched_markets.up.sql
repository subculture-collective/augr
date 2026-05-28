CREATE TABLE IF NOT EXISTS polymarket_watched_markets (
    slug TEXT PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT true,
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    added_by TEXT,
    note TEXT
);

CREATE INDEX IF NOT EXISTS idx_pm_watched_enabled ON polymarket_watched_markets(enabled) WHERE enabled = true;
