DROP INDEX IF EXISTS orders_copy_origin_effect_once;
ALTER TABLE copy_trade_intents
    DROP CONSTRAINT IF EXISTS copy_intent_execution_claim_pair,
    DROP COLUMN IF EXISTS execution_claimed_at,
    DROP COLUMN IF EXISTS execution_claim_id;
