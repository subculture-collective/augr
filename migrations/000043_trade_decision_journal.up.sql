CREATE TABLE IF NOT EXISTS trade_decisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    strategy_id UUID REFERENCES strategies(id) ON DELETE SET NULL,
    pipeline_run_id UUID,
    market_type TEXT NOT NULL,
    instrument_key TEXT NOT NULL,
    external_market_id TEXT,
    side TEXT NOT NULL,
    outcome TEXT,
    fair_value DOUBLE PRECISION NOT NULL DEFAULT 0,
    executable_price DOUBLE PRECISION NOT NULL DEFAULT 0,
    spread DOUBLE PRECISION NOT NULL DEFAULT 0,
    depth DOUBLE PRECISION NOT NULL DEFAULT 0,
    gross_ev DOUBLE PRECISION NOT NULL DEFAULT 0,
    net_ev DOUBLE PRECISION NOT NULL DEFAULT 0,
    kelly_fraction DOUBLE PRECISION NOT NULL DEFAULT 0,
    proposed_size DOUBLE PRECISION NOT NULL DEFAULT 0,
    approved_size DOUBLE PRECISION NOT NULL DEFAULT 0,
    risk_status TEXT NOT NULL CHECK (risk_status IN ('approved', 'rejected')),
    risk_reasons TEXT[] NOT NULL DEFAULT '{}',
    evidence JSONB NOT NULL DEFAULT '{}'::jsonb,
    features JSONB NOT NULL DEFAULT '{}'::jsonb,
    regime_tags TEXT[] NOT NULL DEFAULT '{}',
    paper_order_id UUID REFERENCES orders(id) ON DELETE SET NULL,
    live_order_id UUID REFERENCES orders(id) ON DELETE SET NULL,
    status TEXT NOT NULL CHECK (status IN ('candidate', 'rejected', 'paper_ordered', 'live_ordered', 'closed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_trade_decisions_strategy_created
    ON trade_decisions(strategy_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_trade_decisions_market_created
    ON trade_decisions(market_type, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_trade_decisions_status_created
    ON trade_decisions(status, created_at DESC);
