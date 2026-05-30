ALTER TABLE polymarket_resolved_markets
    DROP CONSTRAINT IF EXISTS polymarket_resolved_markets_winning_side_check;

ALTER TABLE polymarket_resolved_markets
    ADD CONSTRAINT polymarket_resolved_markets_winning_side_check
    CHECK (winning_side IN ('YES','NO','Up','Down','Over','Under'));
