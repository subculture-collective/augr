ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS market_type market_type NOT NULL DEFAULT 'stock';

CREATE INDEX IF NOT EXISTS idx_orders_market_type_created
    ON orders(market_type, created_at DESC);
