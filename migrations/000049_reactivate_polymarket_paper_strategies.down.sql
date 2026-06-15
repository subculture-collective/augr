-- Rollback helper for the reactivation migration. Only pause active paper
-- Polymarket strategies still on the default native paper schedule.
UPDATE strategies
SET status = 'paused',
    is_active = FALSE,
    schedule_cron = '',
    updated_at = NOW()
WHERE market_type = 'polymarket'
  AND is_paper = TRUE
  AND status = 'active'
  AND schedule_cron = '0 */6 * * *';
