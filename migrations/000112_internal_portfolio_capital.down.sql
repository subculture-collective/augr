LOCK TABLE internal_portfolio_capital_snapshots IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
  IF EXISTS(SELECT 1 FROM internal_portfolio_capital_snapshots) THEN
    RAISE EXCEPTION 'cannot remove immutable internal capital snapshot evidence';
  END IF;
END; $$;
DROP TABLE internal_portfolio_capital_snapshots;
DROP FUNCTION validate_internal_portfolio_capital_snapshot();
DROP FUNCTION internal_portfolio_capital_snapshot_bytes(JSONB);
