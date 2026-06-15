-- Polymarket now has a native paper execution path. Reactivate paper
-- Polymarket strategies that were temporarily paused by migration 000048 while
-- the legacy OHLCV path was being blocked.
UPDATE strategies
SET status = 'active',
    is_active = TRUE,
    schedule_cron = '0 */6 * * *',
    updated_at = NOW()
WHERE market_type = 'polymarket'
  AND is_paper = TRUE
  AND status = 'paused'
  AND COALESCE(schedule_cron, '') = '';
