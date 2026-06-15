-- Rollback helper: restore paused Polymarket paper strategies to the prior
-- hourly default only if they still have no schedule. This does not resurrect
-- strategies that were manually rescheduled differently after the migration.
UPDATE strategies
SET status = 'active',
    schedule_cron = '0 * * * *',
    updated_at = NOW()
WHERE market_type = 'polymarket'
  AND is_paper = TRUE
  AND status = 'paused'
  AND COALESCE(schedule_cron, '') = '';
