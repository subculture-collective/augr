UPDATE strategies
SET status = 'inactive',
    is_active = FALSE
WHERE status = 'active'
  AND is_active = TRUE
  AND is_paper = TRUE
  AND (
      name LIKE 'discovery:%'
      OR name LIKE 'options:%'
      OR name LIKE 'paper stock:%'
      OR name LIKE 'paper options:%'
  );

CREATE OR REPLACE FUNCTION sync_strategy_status_with_is_active() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.status = 'active' AND NEW.is_active = FALSE THEN
            NEW.status := 'inactive';
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

ALTER TABLE strategies
    ALTER COLUMN is_active SET DEFAULT FALSE;
