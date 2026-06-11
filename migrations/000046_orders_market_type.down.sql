DROP INDEX IF EXISTS idx_orders_market_type_created;

ALTER TABLE orders
    DROP COLUMN IF EXISTS market_type;
