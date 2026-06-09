CREATE TABLE IF NOT EXISTS replay_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trade_decision_id UUID NOT NULL REFERENCES trade_decisions(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN (
        'decision_created',
        'risk_reviewed',
        'paper_ordered',
        'live_ordered',
        'fill_observed',
        'position_updated',
        'outcome_resolved'
    )),
    source TEXT NOT NULL DEFAULT 'system',
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_replay_events_trade_decision_occurred
    ON replay_events(trade_decision_id, occurred_at);
