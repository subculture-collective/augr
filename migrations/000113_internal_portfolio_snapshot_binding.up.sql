-- Extend the common allocator snapshot reference without treating internal
-- accounting evidence as a broker identity. Existing v1 bytes remain unchanged.
ALTER TABLE portfolio_account_snapshots
  ALTER COLUMN external_account_id DROP NOT NULL,
  ADD COLUMN internal_capital_snapshot_id UUID REFERENCES internal_portfolio_capital_snapshots(id) ON DELETE RESTRICT;
DO $$ DECLARE constraint_name TEXT; BEGIN
  SELECT conname INTO STRICT constraint_name FROM pg_constraint
    WHERE conrelid='portfolio_account_snapshots'::regclass AND contype='c'
      AND pg_get_constraintdef(oid) LIKE '%economic_deterministic_uuid%';
  EXECUTE format('ALTER TABLE portfolio_account_snapshots DROP CONSTRAINT %I',constraint_name);
END; $$;
ALTER TABLE portfolio_account_snapshots
  ADD CONSTRAINT portfolio_account_snapshots_source_shape CHECK(
    (internal_capital_snapshot_id IS NULL AND external_account_id IS NOT NULL AND external_account_id<>'') OR
    (internal_capital_snapshot_id IS NOT NULL AND external_account_id IS NULL AND options_buying_power=0)),
  ADD CONSTRAINT portfolio_account_snapshots_versioned_identity CHECK(
    id=economic_deterministic_uuid('portfolio-account-snapshot',
      CASE WHEN internal_capital_snapshot_id IS NULL THEN 'portfolio-account-snapshot-v1' ELSE 'portfolio-account-snapshot-internal-v1' END||'@sha256:'||sha256));
DROP TRIGGER trg_portfolio_account_snapshots_validate ON portfolio_account_snapshots;
CREATE TRIGGER trg_portfolio_account_snapshots_validate
  AFTER INSERT ON portfolio_account_snapshots FOR EACH ROW
  WHEN (NEW.internal_capital_snapshot_id IS NULL) EXECUTE FUNCTION validate_portfolio_account_snapshot();

CREATE FUNCTION validate_portfolio_internal_snapshot_binding() RETURNS TRIGGER AS $$
BEGIN
  IF NOT EXISTS(SELECT 1 FROM internal_portfolio_capital_snapshots capital
    JOIN accounts account ON account.id=capital.account_id
    WHERE capital.id=NEW.internal_capital_snapshot_id AND capital.account_id=NEW.account_id
      AND account.venue='internal' AND account.external_account_id IS NULL AND account.status='active'
      AND account.environment=NEW.environment AND NEW.observed_at=capital.observed_at
      AND NEW.equity=capital.equity AND NEW.buying_power=trunc(capital.long_buying_power,8)
      AND NEW.canonical_json=jsonb_build_object('schema','portfolio-account-snapshot-internal-v1',
        'account_id',NEW.account_id::TEXT,'internal_capital_snapshot_id',capital.id::TEXT,
        'observed_at',to_char(NEW.observed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
        'equity',to_char(NEW.equity,'FM99999999999999999990.00000000'),
        'buying_power',to_char(NEW.buying_power,'FM99999999999999999990.00000000'),
        'options_margin_supported',false))
  THEN RAISE EXCEPTION 'internal portfolio account snapshot does not match capital evidence'; END IF;
  RETURN NEW;
END; $$ LANGUAGE plpgsql;
CREATE TRIGGER trg_portfolio_internal_snapshot_binding
  AFTER INSERT ON portfolio_account_snapshots FOR EACH ROW
  WHEN (NEW.internal_capital_snapshot_id IS NOT NULL) EXECUTE FUNCTION validate_portfolio_internal_snapshot_binding();
