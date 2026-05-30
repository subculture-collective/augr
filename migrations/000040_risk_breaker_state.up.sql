CREATE TABLE IF NOT EXISTS risk_breaker_state (
    scope         TEXT PRIMARY KEY,
    tripped_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reason        TEXT NOT NULL,
    reset_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_risk_breaker_state_tripped_at
    ON risk_breaker_state (tripped_at DESC);
