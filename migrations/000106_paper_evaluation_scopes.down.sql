LOCK TABLE report_artifacts,backtest_runs,backtest_configs,paper_evaluation_scopes IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
  IF EXISTS(SELECT 1 FROM paper_evaluation_scopes) OR
     EXISTS(SELECT 1 FROM backtest_configs WHERE scope_id IS NOT NULL) OR
     EXISTS(SELECT 1 FROM backtest_runs WHERE scope_id IS NOT NULL) OR
     EXISTS(SELECT 1 FROM report_artifacts WHERE scope_id IS NOT NULL) THEN
    RAISE EXCEPTION 'cannot roll back migration 106 while scoped paper evidence exists';
  END IF;
END $$;
DROP TRIGGER trg_report_artifacts_completed_immutable ON report_artifacts;
DROP TRIGGER trg_report_artifacts_scope ON report_artifacts;
DROP TRIGGER trg_backtest_runs_scope ON backtest_runs;
DROP TRIGGER trg_backtest_runs_identity ON backtest_runs;
DROP TRIGGER trg_backtest_configs_identity ON backtest_configs;
DROP TRIGGER trg_backtest_configs_validate_scope ON backtest_configs;
DROP TRIGGER trg_report_artifacts_require_scope ON report_artifacts;
DROP TRIGGER trg_backtest_runs_require_scope ON backtest_runs;
DROP TRIGGER trg_backtest_configs_require_scope ON backtest_configs;
DROP FUNCTION protect_backtest_config_identity();
DROP FUNCTION protect_scoped_backtest_run_identity();
DROP FUNCTION validate_backtest_config_scope();
DROP FUNCTION reject_new_unscoped_paper_evidence();
DROP FUNCTION protect_completed_report_artifact();
DROP FUNCTION validate_report_artifact_scope();
DROP FUNCTION validate_backtest_run_scope();
DROP INDEX idx_report_artifacts_scope;
DROP INDEX idx_backtest_runs_scope;
DROP INDEX idx_backtest_configs_scope;
DROP INDEX uq_backtest_configs_strategy_scope;
DROP INDEX uq_report_artifacts_scoped;
DROP INDEX uq_report_artifacts_legacy;
ALTER TABLE report_artifacts DROP COLUMN backtest_run_id;
ALTER TABLE report_artifacts DROP COLUMN report_sha256;
ALTER TABLE report_artifacts DROP COLUMN report_bytes;
ALTER TABLE report_artifacts DROP COLUMN scope_id;
ALTER TABLE backtest_runs DROP COLUMN scope_id;
ALTER TABLE backtest_configs DROP COLUMN scope_id;
ALTER TABLE report_artifacts ADD CONSTRAINT report_artifacts_strategy_id_report_type_time_bucket_key UNIQUE(strategy_id,report_type,time_bucket);
DROP TABLE paper_evaluation_scopes;
DROP FUNCTION reject_paper_evaluation_scope_mutation();
DROP FUNCTION validate_paper_evaluation_scope();
DROP FUNCTION paper_evaluation_scope_canonical_bytes(UUID,UUID,TEXT,TEXT,TEXT,TEXT,TIMESTAMPTZ,TIMESTAMPTZ);
