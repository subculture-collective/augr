LOCK TABLE portfolio_account_snapshots IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
  IF EXISTS(SELECT 1 FROM portfolio_account_snapshots WHERE internal_capital_snapshot_id IS NOT NULL) THEN
    RAISE EXCEPTION 'cannot remove immutable internal portfolio account snapshot evidence';
  END IF;
END; $$;
DROP TRIGGER trg_portfolio_internal_snapshot_binding ON portfolio_account_snapshots;
DROP FUNCTION validate_portfolio_internal_snapshot_binding();
DROP TRIGGER trg_portfolio_account_snapshots_validate ON portfolio_account_snapshots;
ALTER TABLE portfolio_account_snapshots
  DROP CONSTRAINT portfolio_account_snapshots_source_shape,
  DROP CONSTRAINT portfolio_account_snapshots_versioned_identity,
  DROP COLUMN internal_capital_snapshot_id,
  ALTER COLUMN external_account_id SET NOT NULL,
  ADD CONSTRAINT portfolio_account_snapshots_v1_identity CHECK(id=economic_deterministic_uuid('portfolio-account-snapshot','portfolio-account-snapshot-v1@sha256:'||sha256));
CREATE TRIGGER trg_portfolio_account_snapshots_validate
  AFTER INSERT ON portfolio_account_snapshots FOR EACH ROW EXECUTE FUNCTION validate_portfolio_account_snapshot();
