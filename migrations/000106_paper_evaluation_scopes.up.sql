LOCK TABLE accounts,account_capital_policy_bindings,dataset_manifests,dataset_quality_results,simulation_policy_artifacts,
  capital_margin_policy_artifacts,backtest_configs,backtest_runs,report_artifacts IN SHARE ROW EXCLUSIVE MODE;

CREATE FUNCTION paper_evaluation_scope_canonical_bytes(
  account_value UUID,binding_value UUID,manifest_value TEXT,quality_value TEXT,
  simulation_value TEXT,capital_value TEXT,start_value TIMESTAMPTZ,end_value TIMESTAMPTZ
) RETURNS BYTEA AS $$
  SELECT convert_to(
    '{"schema":"paper-evaluation-scope-v1","account_id":'||to_json(account_value::TEXT)::TEXT||
    ',"capital_binding_id":'||to_json(binding_value::TEXT)::TEXT||
    ',"manifest_sha256":'||to_json(manifest_value)::TEXT||
    ',"quality_sha256":'||to_json(quality_value)::TEXT||
    ',"simulation_policy_sha256":'||to_json(simulation_value)::TEXT||
    ',"capital_policy_sha256":'||to_json(capital_value)::TEXT||
    ',"evaluation_start":'||to_json(to_char(start_value AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'))::TEXT||
    ',"evaluation_end":'||to_json(to_char(end_value AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'))::TEXT||'}','UTF8');
$$ LANGUAGE SQL IMMUTABLE STRICT;

CREATE TABLE paper_evaluation_scopes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
  capital_binding_id UUID NOT NULL REFERENCES account_capital_policy_bindings(id) ON DELETE RESTRICT,
  manifest_sha256 TEXT NOT NULL CHECK (manifest_sha256 ~ '^[0-9a-f]{64}$'),
  quality_sha256 TEXT NOT NULL CHECK (quality_sha256 ~ '^[0-9a-f]{64}$'),
  simulation_policy_sha256 TEXT NOT NULL CHECK (simulation_policy_sha256 ~ '^[0-9a-f]{64}$'),
  capital_policy_sha256 TEXT NOT NULL CHECK (capital_policy_sha256 ~ '^[0-9a-f]{64}$'),
  evaluation_start TIMESTAMPTZ NOT NULL CHECK (evaluation_start=date_trunc('microseconds',evaluation_start)),
  evaluation_end TIMESTAMPTZ NOT NULL CHECK (evaluation_end > evaluation_start AND evaluation_end=date_trunc('microseconds',evaluation_end)),
  canonical_bytes BYTEA NOT NULL,
  canonical_sha256 TEXT NOT NULL UNIQUE CHECK (canonical_sha256 ~ '^[0-9a-f]{64}$'),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (canonical_sha256 = encode(digest(canonical_bytes, 'sha256'), 'hex')),
  CHECK (canonical_bytes=paper_evaluation_scope_canonical_bytes(account_id,capital_binding_id,manifest_sha256,
    quality_sha256,simulation_policy_sha256,capital_policy_sha256,evaluation_start,evaluation_end))
);

CREATE FUNCTION validate_paper_evaluation_scope() RETURNS TRIGGER AS $$
BEGIN
  PERFORM 1
    FROM dataset_quality_results q
    JOIN dataset_manifests m ON m.id = q.manifest_id
    JOIN account_capital_policy_bindings b ON b.id = NEW.capital_binding_id AND b.account_id = NEW.account_id
    JOIN capital_margin_policy_artifacts cp ON cp.id = b.policy_artifact_id AND cp.policy_version = b.policy_version
    WHERE m.sha256 = NEW.manifest_sha256 AND q.sha256 = NEW.quality_sha256
      AND NOT q.quarantined AND m.decision_cutoff >= NEW.evaluation_end
      AND EXISTS (SELECT 1 FROM simulation_policy_artifacts p WHERE p.sha256 = NEW.simulation_policy_sha256)
      AND cp.sha256 = NEW.capital_policy_sha256;
  IF NOT FOUND THEN RAISE EXCEPTION 'paper evaluation scope evidence does not match'; END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE FUNCTION reject_paper_evaluation_scope_mutation() RETURNS TRIGGER AS $$
BEGIN RAISE EXCEPTION 'paper evaluation scope is immutable'; END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_paper_evaluation_scopes_validate BEFORE INSERT ON paper_evaluation_scopes
  FOR EACH ROW EXECUTE FUNCTION validate_paper_evaluation_scope();
CREATE TRIGGER trg_paper_evaluation_scopes_immutable BEFORE UPDATE OR DELETE ON paper_evaluation_scopes
  FOR EACH ROW EXECUTE FUNCTION reject_paper_evaluation_scope_mutation();

ALTER TABLE backtest_configs ADD COLUMN scope_id UUID REFERENCES paper_evaluation_scopes(id) ON DELETE RESTRICT;
ALTER TABLE backtest_runs ADD COLUMN scope_id UUID REFERENCES paper_evaluation_scopes(id) ON DELETE RESTRICT;
ALTER TABLE report_artifacts ADD COLUMN scope_id UUID REFERENCES paper_evaluation_scopes(id) ON DELETE RESTRICT;
ALTER TABLE report_artifacts ADD COLUMN backtest_run_id UUID REFERENCES backtest_runs(id) ON DELETE RESTRICT;
ALTER TABLE report_artifacts ADD COLUMN report_bytes BYTEA;
ALTER TABLE report_artifacts ADD COLUMN report_sha256 TEXT CHECK (report_sha256 IS NULL OR report_sha256 ~ '^[0-9a-f]{64}$');

CREATE FUNCTION reject_new_unscoped_paper_evidence() RETURNS TRIGGER AS $$
BEGIN
  IF NEW.scope_id IS NULL THEN RAISE EXCEPTION 'new paper evidence requires scope_id'; END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trg_backtest_configs_require_scope BEFORE INSERT ON backtest_configs
  FOR EACH ROW EXECUTE FUNCTION reject_new_unscoped_paper_evidence();

CREATE FUNCTION validate_backtest_config_scope() RETURNS TRIGGER AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM paper_evaluation_scopes s
    JOIN account_capital_policy_bindings b ON b.id=s.capital_binding_id AND b.account_id=s.account_id
    JOIN dataset_manifests m ON m.sha256=s.manifest_sha256
    JOIN dataset_quality_results q ON q.manifest_id=m.id AND q.sha256=s.quality_sha256
    WHERE s.id=NEW.scope_id
      AND NEW.start_date=s.evaluation_start AND NEW.end_date=s.evaluation_end
      AND (NEW.simulation_params->>'initial_capital')::NUMERIC=b.starting_capital
      AND NOT q.quarantined AND m.decision_cutoff >= NEW.end_date
      AND (SELECT min(p.effective_start) FROM dataset_manifest_partitions p WHERE p.manifest_id=m.id) <= NEW.start_date
      AND (SELECT max(p.effective_end) FROM dataset_manifest_partitions p WHERE p.manifest_id=m.id) >= NEW.end_date
      AND NOT EXISTS (SELECT 1 FROM dataset_manifest_partitions p WHERE p.manifest_id=m.id AND p.available_end>m.decision_cutoff)
  ) THEN
    RAISE EXCEPTION 'backtest config date range, capital, or available dataset facts do not match scope';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trg_backtest_configs_validate_scope BEFORE INSERT OR UPDATE OF start_date,end_date,simulation_params,scope_id ON backtest_configs
  FOR EACH ROW WHEN (NEW.scope_id IS NOT NULL) EXECUTE FUNCTION validate_backtest_config_scope();
CREATE TRIGGER trg_backtest_runs_require_scope BEFORE INSERT ON backtest_runs
  FOR EACH ROW EXECUTE FUNCTION reject_new_unscoped_paper_evidence();
CREATE TRIGGER trg_report_artifacts_require_scope BEFORE INSERT ON report_artifacts
  FOR EACH ROW EXECUTE FUNCTION reject_new_unscoped_paper_evidence();

ALTER TABLE report_artifacts DROP CONSTRAINT report_artifacts_strategy_id_report_type_time_bucket_key;
CREATE UNIQUE INDEX uq_report_artifacts_legacy ON report_artifacts(strategy_id,report_type,time_bucket) WHERE scope_id IS NULL;
CREATE UNIQUE INDEX uq_report_artifacts_scoped ON report_artifacts(scope_id,strategy_id,report_type,time_bucket) WHERE scope_id IS NOT NULL;

CREATE FUNCTION validate_backtest_run_scope() RETURNS TRIGGER AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM backtest_configs c WHERE c.id=NEW.backtest_config_id AND c.scope_id IS NOT DISTINCT FROM NEW.scope_id) THEN
    RAISE EXCEPTION 'backtest run scope does not match config';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trg_backtest_runs_scope BEFORE INSERT OR UPDATE OF backtest_config_id,scope_id ON backtest_runs
  FOR EACH ROW EXECUTE FUNCTION validate_backtest_run_scope();

CREATE FUNCTION protect_backtest_config_identity() RETURNS TRIGGER AS $$
BEGIN
  IF OLD.scope_id IS NULL AND NEW.scope_id IS NOT NULL THEN
    RAISE EXCEPTION 'legacy backtest config cannot be relabeled as scoped';
  END IF;
  IF OLD.scope_id IS NOT NULL AND
     (NEW.strategy_id IS DISTINCT FROM OLD.strategy_id OR NEW.scope_id IS DISTINCT FROM OLD.scope_id) THEN
    RAISE EXCEPTION 'scoped backtest config strategy and scope are immutable';
  END IF;
  IF NEW.strategy_id IS DISTINCT FROM OLD.strategy_id AND EXISTS (SELECT 1 FROM backtest_runs r WHERE r.backtest_config_id=OLD.id) THEN
    RAISE EXCEPTION 'backtest config strategy is immutable after evidence exists';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trg_backtest_configs_identity BEFORE UPDATE OF strategy_id,scope_id ON backtest_configs
  FOR EACH ROW EXECUTE FUNCTION protect_backtest_config_identity();

CREATE FUNCTION protect_scoped_backtest_run_identity() RETURNS TRIGGER AS $$
BEGIN
	IF OLD.scope_id IS NOT NULL THEN
	  RAISE EXCEPTION 'scoped backtest run evidence is immutable';
	END IF;
	IF TG_OP='DELETE' THEN RETURN OLD; END IF;
	RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trg_backtest_runs_identity BEFORE UPDATE OR DELETE ON backtest_runs
  FOR EACH ROW EXECUTE FUNCTION protect_scoped_backtest_run_identity();

CREATE FUNCTION validate_report_artifact_scope() RETURNS TRIGGER AS $$
BEGIN
  IF NEW.scope_id IS NULL THEN
    IF NEW.backtest_run_id IS NOT NULL THEN RAISE EXCEPTION 'legacy report cannot reference a scoped run'; END IF;
    RETURN NEW;
  END IF;
  IF NEW.status='completed' AND NEW.backtest_run_id IS NULL THEN
    RAISE EXCEPTION 'completed scoped report requires backtest run';
  END IF;
  IF NEW.status='completed' AND (NEW.report_bytes IS NULL OR NEW.report_sha256 IS NULL OR
      NEW.report_sha256 IS DISTINCT FROM encode(digest(NEW.report_bytes,'sha256'),'hex') OR
      NEW.report_json IS DISTINCT FROM convert_from(NEW.report_bytes,'UTF8')::JSONB) THEN
    RAISE EXCEPTION 'completed scoped report content hash does not match';
  END IF;
  IF NEW.backtest_run_id IS NOT NULL AND NOT EXISTS (
    SELECT 1 FROM backtest_runs r JOIN backtest_configs c ON c.id=r.backtest_config_id
    WHERE r.id=NEW.backtest_run_id AND r.scope_id=NEW.scope_id AND c.scope_id=NEW.scope_id AND c.strategy_id=NEW.strategy_id
  ) THEN RAISE EXCEPTION 'report artifact scope does not match run'; END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE FUNCTION protect_completed_report_artifact() RETURNS TRIGGER AS $$
BEGIN
  IF TG_OP='DELETE' OR OLD.status='completed' AND NEW IS DISTINCT FROM OLD THEN
    RAISE EXCEPTION 'completed report artifact is immutable';
  END IF;
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trg_report_artifacts_scope BEFORE INSERT OR UPDATE OF strategy_id,scope_id,backtest_run_id,status,report_json,report_bytes,report_sha256 ON report_artifacts
  FOR EACH ROW EXECUTE FUNCTION validate_report_artifact_scope();
CREATE TRIGGER trg_report_artifacts_completed_immutable BEFORE UPDATE OR DELETE ON report_artifacts
  FOR EACH ROW EXECUTE FUNCTION protect_completed_report_artifact();

CREATE INDEX idx_backtest_configs_scope ON backtest_configs(scope_id) WHERE scope_id IS NOT NULL;
CREATE UNIQUE INDEX uq_backtest_configs_strategy_scope ON backtest_configs(strategy_id,scope_id) WHERE scope_id IS NOT NULL;
CREATE INDEX idx_backtest_runs_scope ON backtest_runs(scope_id) WHERE scope_id IS NOT NULL;
CREATE INDEX idx_report_artifacts_scope ON report_artifacts(scope_id,time_bucket DESC) WHERE scope_id IS NOT NULL;
