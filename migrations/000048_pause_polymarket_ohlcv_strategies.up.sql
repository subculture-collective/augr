-- Polymarket strategies need a prediction-market-native executor. Pause the
-- currently scheduled paper strategies so the stock OHLCV runner stops trying
-- to execute them while discovery/profile/resolution jobs continue normally.
UPDATE strategies
SET status = 'paused',
    schedule_cron = '',
    updated_at = NOW()
WHERE market_type = 'polymarket'
  AND is_paper = TRUE
  AND status = 'active'
  AND COALESCE(schedule_cron, '') <> '';
