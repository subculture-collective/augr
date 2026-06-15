-- Keep one active paper Polymarket strategy per market slug. Generated strategy
-- names used to vary by LLM wording, which allowed duplicate active schedules
-- for the same slug. Preserve the oldest active row and quarantine duplicates.
WITH ranked AS (
    SELECT
        id,
        ROW_NUMBER() OVER (PARTITION BY ticker ORDER BY created_at ASC, id ASC) AS rn
    FROM strategies
    WHERE market_type = 'polymarket'
      AND is_paper = TRUE
      AND status = 'active'
)
UPDATE strategies s
SET status = 'inactive',
    is_active = FALSE,
    updated_at = NOW()
FROM ranked r
WHERE s.id = r.id
  AND r.rn > 1;

CREATE UNIQUE INDEX IF NOT EXISTS idx_strategies_polymarket_paper_active_slug
    ON strategies (ticker)
    WHERE market_type = 'polymarket'
      AND is_paper = TRUE
      AND status = 'active';
