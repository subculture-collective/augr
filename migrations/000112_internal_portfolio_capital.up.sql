-- Internal capital observations are a distinct evidence class, not broker
-- snapshots or venue reconciliations. No account identity or capital is changed.
CREATE FUNCTION internal_portfolio_capital_snapshot_bytes(value JSONB) RETURNS BYTEA AS $$
  SELECT convert_to('{"schema":"internal-portfolio-capital-snapshot-v1","account_id":'||dataset_json_string(value->>'account_id')||
    ',"capital_binding_id":'||dataset_json_string(value->>'capital_binding_id')||
    ',"projection_checkpoint_id":'||dataset_json_string(value->>'projection_checkpoint_id')||
    ',"through_transaction_id":'||dataset_json_string(value->>'through_transaction_id')||
    ',"observed_at":'||dataset_json_string(value->>'observed_at')||
    ',"equity":'||dataset_json_string(value->>'equity')||
    ',"long_buying_power":'||dataset_json_string(value->>'long_buying_power')||
    ',"options_margin_supported":false}', 'UTF8');
$$ LANGUAGE SQL IMMUTABLE STRICT;

CREATE TABLE internal_portfolio_capital_snapshots (
  id UUID PRIMARY KEY,
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
  capital_binding_id UUID NOT NULL REFERENCES account_capital_policy_bindings(id) ON DELETE RESTRICT,
  projection_checkpoint_id UUID NOT NULL REFERENCES projection_checkpoints(id) ON DELETE RESTRICT,
  through_transaction_id UUID NOT NULL REFERENCES ledger_transactions(id) ON DELETE RESTRICT,
  observed_at TIMESTAMPTZ NOT NULL CHECK(observed_at=date_trunc('microseconds',observed_at)),
  equity NUMERIC NOT NULL CHECK(equity>0 AND equity=round(equity,12)),
  long_buying_power NUMERIC NOT NULL CHECK(long_buying_power>=0 AND long_buying_power=round(long_buying_power,12)),
  sha256 TEXT NOT NULL CHECK(sha256 ~ '^[0-9a-f]{64}$'),
  canonical_bytes BYTEA NOT NULL,
  canonical_json JSONB NOT NULL,
  CHECK(sha256=encode(digest(canonical_bytes,'sha256'),'hex')),
  CHECK(canonical_json=convert_from(canonical_bytes,'UTF8')::JSONB),
  CHECK(canonical_bytes=internal_portfolio_capital_snapshot_bytes(canonical_json)),
  CHECK(id=economic_deterministic_uuid('internal-portfolio-capital-snapshot',sha256))
);

CREATE FUNCTION validate_internal_portfolio_capital_snapshot() RETURNS TRIGGER AS $$
DECLARE
  checkpoint projection_checkpoints%ROWTYPE;
  binding account_capital_policy_bindings%ROWTYPE;
  profile JSONB;
  cash_value NUMERIC;
  equity_value NUMERIC;
  long_value NUMERIC;
  short_value NUMERIC;
  initial_value NUMERIC;
  maintenance_value NUMERIC;
  capacity NUMERIC;
BEGIN
  SELECT * INTO checkpoint FROM projection_checkpoints WHERE id=NEW.projection_checkpoint_id;
  SELECT * INTO binding FROM account_capital_policy_bindings WHERE id=NEW.capital_binding_id;
  IF NOT EXISTS(SELECT 1 FROM accounts WHERE id=NEW.account_id AND venue='internal'
      AND external_account_id IS NULL AND status='active' AND environment='paper_scored')
    OR binding.account_id IS DISTINCT FROM NEW.account_id
    OR checkpoint.account_id IS DISTINCT FROM NEW.account_id
    OR checkpoint.through_transaction_id IS DISTINCT FROM NEW.through_transaction_id
    OR checkpoint.as_of IS DISTINCT FROM NEW.observed_at
    OR checkpoint.as_of>clock_timestamp() OR checkpoint.as_of<clock_timestamp()-interval '5 minutes'
    OR checkpoint.projection_version IS DISTINCT FROM 'ledger_fifo_v1'
    OR checkpoint.projection_type IS DISTINCT FROM 'portfolio'
    OR checkpoint.fifo_method IS DISTINCT FROM 'fifo'
  THEN RAISE EXCEPTION 'internal capital snapshot account or checkpoint does not reconstruct'; END IF;

  SELECT value INTO profile FROM capital_margin_policy_artifacts artifact,
    jsonb_array_elements(artifact.canonical_json->'profiles') AS profiles(value)
    WHERE artifact.id=binding.policy_artifact_id AND value->>'name'=binding.margin_profile;
  IF profile IS NULL OR (profile->>'unlimited')::BOOLEAN OR (profile->>'initial_long')::NUMERIC<=0
    THEN RAISE EXCEPTION 'internal capital snapshot requires finite reviewed policy'; END IF;
  IF EXISTS(SELECT 1 FROM jsonb_array_elements(checkpoint.payload->'positions') position
      WHERE (position->>'open')::BOOLEAN AND NOT EXISTS(SELECT 1 FROM instruments instrument
        WHERE instrument.id=(position->>'instrument_id')::UUID
        AND instrument.asset_class IN ('equity','etf') AND instrument.status='active'
        AND instrument.currency=checkpoint.base_currency))
    THEN RAISE EXCEPTION 'internal capital snapshot has unsupported positions'; END IF;
  SELECT COALESCE(sum(greatest((position->>'market_value')::NUMERIC,0)),0),
    COALESCE(sum(greatest(-(position->>'market_value')::NUMERIC,0)),0)
    INTO long_value,short_value FROM jsonb_array_elements(checkpoint.payload->'positions') position;
  cash_value := (checkpoint.payload->'totals'->>'cash')::NUMERIC;
  equity_value := (checkpoint.payload->'totals'->>'equity')::NUMERIC;
  IF equity_value IS NULL OR cash_value IS NULL OR equity_value<>cash_value+long_value-short_value
    THEN RAISE EXCEPTION 'internal capital snapshot accounting totals do not reconstruct'; END IF;
  initial_value := ceil((long_value*(profile->>'initial_long')::NUMERIC+
    short_value*(profile->>'initial_short')::NUMERIC)*1e12)/1e12;
  maintenance_value := ceil((long_value*(profile->>'maintenance_long')::NUMERIC+
    short_value*(profile->>'maintenance_short')::NUMERIC)*1e12)/1e12;
  capacity := least(greatest(equity_value*(profile->>'maximum_gross')::NUMERIC-long_value-short_value,0),
    floor(greatest(equity_value-initial_value,0)/(profile->>'initial_long')::NUMERIC*1e12)/1e12);
  IF binding.margin_profile='cash' THEN
    capacity := least(capacity,greatest(cash_value-binding.tier*(profile->>'cash_reserve')::NUMERIC,0));
  END IF;
  IF maintenance_value>equity_value THEN capacity := 0; END IF;
  IF NEW.equity<>equity_value OR NEW.long_buying_power<>capacity
    OR NEW.canonical_json<>jsonb_build_object(
      'schema','internal-portfolio-capital-snapshot-v1','account_id',NEW.account_id::TEXT,
      'capital_binding_id',NEW.capital_binding_id::TEXT,'projection_checkpoint_id',NEW.projection_checkpoint_id::TEXT,
      'through_transaction_id',NEW.through_transaction_id::TEXT,
      'observed_at',to_char(NEW.observed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
      'equity',trim_scale(NEW.equity)::TEXT,'long_buying_power',trim_scale(NEW.long_buying_power)::TEXT,
      'options_margin_supported',false)
    THEN RAISE EXCEPTION 'internal capital snapshot payload or balances do not reconstruct'; END IF;
  RETURN NEW;
END; $$ LANGUAGE plpgsql;

CREATE TRIGGER trg_internal_portfolio_capital_snapshots_validate
  BEFORE INSERT ON internal_portfolio_capital_snapshots
  FOR EACH ROW EXECUTE FUNCTION validate_internal_portfolio_capital_snapshot();
CREATE TRIGGER trg_internal_portfolio_capital_snapshots_immutable
  BEFORE UPDATE OR DELETE ON internal_portfolio_capital_snapshots
  FOR EACH ROW EXECUTE FUNCTION reject_capital_policy_fact_mutation();
