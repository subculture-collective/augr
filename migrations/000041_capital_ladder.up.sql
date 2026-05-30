CREATE TABLE IF NOT EXISTS capital_ladder (
    strategy_id           TEXT PRIMARY KEY,
    step_pct              DOUBLE PRECISION NOT NULL DEFAULT 0.10,
    fill_rate             DOUBLE PRECISION NOT NULL DEFAULT 0,
    win_rate              DOUBLE PRECISION NOT NULL DEFAULT 0,
    drawdown_pct          DOUBLE PRECISION NOT NULL DEFAULT 0,
    baseline_fill_rate    DOUBLE PRECISION NOT NULL DEFAULT 0,
    baseline_win_rate     DOUBLE PRECISION NOT NULL DEFAULT 0,
    advanced_at           TIMESTAMPTZ,
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_capital_ladder_updated_at ON capital_ladder (updated_at DESC);
