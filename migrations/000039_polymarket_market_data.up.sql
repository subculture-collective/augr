CREATE EXTENSION IF NOT EXISTS timescaledb;

CREATE TABLE polymarket_ticks (
    slug text NOT NULL,
    side text NOT NULL,
    price double precision NOT NULL,
    size double precision NOT NULL,
    received_at timestamptz NOT NULL,
    seq_hint bigint,
    conn_id int
);

CREATE TABLE polymarket_book_snapshots (
    slug text NOT NULL,
    best_bid double precision,
    best_ask double precision,
    bids jsonb NOT NULL,
    asks jsonb NOT NULL,
    received_at timestamptz NOT NULL,
    conn_id int
);

SELECT create_hypertable('polymarket_ticks', 'received_at', chunk_time_interval => interval '6 hours');
SELECT create_hypertable('polymarket_book_snapshots', 'received_at', chunk_time_interval => interval '6 hours');

CREATE INDEX polymarket_ticks_slug_received_at_idx ON polymarket_ticks (slug, received_at DESC);
CREATE INDEX polymarket_book_snapshots_slug_received_at_idx ON polymarket_book_snapshots (slug, received_at DESC);

ALTER TABLE polymarket_ticks SET (timescaledb.compress, timescaledb.compress_segmentby='slug');
ALTER TABLE polymarket_book_snapshots SET (timescaledb.compress, timescaledb.compress_segmentby='slug');

SELECT add_compression_policy('polymarket_ticks', interval '7 days');
SELECT add_compression_policy('polymarket_book_snapshots', interval '7 days');

SELECT add_retention_policy('polymarket_ticks', interval '540 days');
SELECT add_retention_policy('polymarket_book_snapshots', interval '540 days');
