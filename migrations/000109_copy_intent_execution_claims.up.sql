ALTER TABLE copy_trade_intents
    ADD COLUMN execution_claim_id UUID,
    ADD COLUMN execution_claimed_at TIMESTAMPTZ,
    ADD CONSTRAINT copy_intent_execution_claim_pair CHECK ((execution_claim_id IS NULL) = (execution_claimed_at IS NULL));

CREATE UNIQUE INDEX orders_copy_origin_effect_once
    ON orders (account_id, environment, origin_id, copy_origin_rebalance_run_id, ticker, side)
    WHERE origin_type = 'copy_subscription' AND copy_origin_rebalance_run_id IS NOT NULL;
