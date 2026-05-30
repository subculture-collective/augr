CREATE TABLE IF NOT EXISTS polymarket_resolved_markets (
    slug TEXT PRIMARY KEY,
    winning_side TEXT NOT NULL CHECK (winning_side IN ('YES','NO')),
    resolved_at TIMESTAMPTZ,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
