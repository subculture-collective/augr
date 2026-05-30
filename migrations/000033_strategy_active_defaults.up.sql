-- Keep the deprecated is_active column aligned with the lifecycle status
-- default so newly generated strategies that set status='active' but do not
-- explicitly write is_active are not demoted by the compatibility trigger.
ALTER TABLE strategies
    ALTER COLUMN is_active SET DEFAULT TRUE;

CREATE OR REPLACE FUNCTION sync_strategy_status_with_is_active() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.status = 'active' AND NEW.is_active = FALSE THEN
            NEW.status := 'inactive';
        ELSIF NEW.status IN ('active', 'inactive') THEN
            NEW.is_active := (NEW.status = 'active');
        ELSIF NEW.status <> 'active' THEN
            NEW.is_active := FALSE;
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.status IS NOT DISTINCT FROM OLD.status AND NEW.is_active IS DISTINCT FROM OLD.is_active THEN
        NEW.status := CASE
            WHEN NEW.is_active THEN 'active'
            ELSE 'inactive'
        END;
    ELSIF NEW.is_active IS NOT DISTINCT FROM OLD.is_active AND NEW.status IS DISTINCT FROM OLD.status AND NEW.status IN ('active', 'inactive') THEN
        NEW.is_active := (NEW.status = 'active');
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Repair strategies created by automated generation while the deprecated
-- is_active default was false. These code paths construct active paper
-- strategies, then the compatibility trigger stored them as inactive.
UPDATE strategies
SET status = 'active',
    is_active = TRUE
WHERE status = 'inactive'
  AND is_active = FALSE
  AND is_paper = TRUE
  AND (
      name LIKE 'discovery:%'
      OR name LIKE 'options:%'
      OR name LIKE 'paper stock:%'
      OR name LIKE 'paper options:%'
  );
