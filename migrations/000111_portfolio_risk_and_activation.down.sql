LOCK TABLE strategy_promotion_activations, portfolio_risk_policy_artifacts,
  account_portfolio_risk_policy_bindings, portfolio_account_snapshots,
  generated_strategy_scenario_bindings, generated_strategy_scenario_frames, generated_strategy_scenarios,
  portfolio_opportunity_option_legs, allocation_risk_caps,
  portfolio_opportunities, allocation_decisions IN ACCESS EXCLUSIVE MODE;
DO $$ DECLARE has_pipeline_evidence BOOLEAN := false; BEGIN
  IF EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='pipeline_runs' AND column_name='execution_version_id') THEN
    EXECUTE 'SELECT EXISTS(SELECT 1 FROM pipeline_runs WHERE execution_version_id IS NOT NULL)' INTO has_pipeline_evidence;
  END IF;
  IF EXISTS(SELECT 1 FROM strategy_promotion_activations)
     OR EXISTS(SELECT 1 FROM portfolio_risk_policy_artifacts)
     OR EXISTS(SELECT 1 FROM account_portfolio_risk_policy_bindings)
     OR EXISTS(SELECT 1 FROM portfolio_account_snapshots)
     OR EXISTS(SELECT 1 FROM generated_strategy_scenarios)
     OR EXISTS(SELECT 1 FROM portfolio_opportunity_option_legs)
     OR EXISTS(SELECT 1 FROM allocation_risk_caps)
	 OR has_pipeline_evidence
     OR EXISTS(SELECT 1 FROM portfolio_opportunities WHERE execution_version_id IS NOT NULL)
     OR EXISTS(SELECT 1 FROM allocation_decisions WHERE risk_policy_id IS NOT NULL OR account_snapshot_id IS NOT NULL) THEN
    RAISE EXCEPTION 'cannot roll back migration 111: portfolio risk, opportunity, or activation evidence exists';
  END IF;
END $$;
DROP TRIGGER IF EXISTS trg_pipeline_run_promotion_lineage ON pipeline_runs;
DROP FUNCTION IF EXISTS validate_pipeline_run_promotion_lineage();
ALTER TABLE pipeline_runs DROP CONSTRAINT IF EXISTS pipeline_run_promotion_lineage,DROP COLUMN IF EXISTS risk_policy_version,DROP COLUMN IF EXISTS capital_binding_id,DROP COLUMN IF EXISTS promotion_decision_id,DROP COLUMN IF EXISTS deployment_id,DROP COLUMN IF EXISTS quality_result_id,DROP COLUMN IF EXISTS manifest_id,DROP COLUMN IF EXISTS evaluation_scope_id,DROP COLUMN IF EXISTS execution_version_id;
DROP TABLE strategy_promotion_activations;
DROP FUNCTION validate_strategy_promotion_activation();
DROP TRIGGER trg_portfolio_opportunity_preserve_intent ON portfolio_opportunities;
DROP FUNCTION preserve_portfolio_opportunity_intent();
DROP TRIGGER IF EXISTS trg_portfolio_opportunity_promotion_lineage ON portfolio_opportunities;
DROP FUNCTION IF EXISTS validate_portfolio_opportunity_promotion_lineage();
DROP TRIGGER IF EXISTS trg_portfolio_option_package_parent ON portfolio_opportunities;
DROP TRIGGER IF EXISTS trg_portfolio_option_package_leg ON portfolio_opportunity_option_legs;
DROP FUNCTION IF EXISTS validate_portfolio_option_package();
DROP TRIGGER IF EXISTS trg_allocation_decision_preserve_risk ON allocation_decisions;
DROP FUNCTION IF EXISTS preserve_allocation_risk_evidence();
DROP TABLE allocation_risk_caps;
DROP INDEX orders_option_allocation_leg_once;
DROP INDEX orders_stock_allocation_effect_once;
CREATE UNIQUE INDEX orders_allocation_effect_once ON orders(account_id,allocation_opportunity_id) WHERE allocation_opportunity_id IS NOT NULL;
ALTER TABLE allocation_decisions DROP CONSTRAINT IF EXISTS allocation_decision_risk_state_hash,DROP CONSTRAINT IF EXISTS allocation_decision_risk_state_pair,DROP COLUMN IF EXISTS risk_state_bytes,DROP COLUMN IF EXISTS risk_state_sha256,DROP COLUMN execution_route,DROP COLUMN binding_constraint,DROP COLUMN exposure_after_usd,DROP COLUMN exposure_before_usd,DROP COLUMN reserved_capital_usd,DROP COLUMN reserved_risk_usd,DROP COLUMN max_loss_per_unit,DROP COLUMN proposed_quantity,DROP COLUMN account_snapshot_id,DROP COLUMN risk_policy_version,DROP COLUMN risk_policy_id;
DROP TABLE portfolio_opportunity_option_legs;
ALTER TABLE portfolio_opportunities DROP CONSTRAINT portfolio_opportunity_intent_hash,DROP CONSTRAINT portfolio_opportunity_promotion_lineage,DROP COLUMN intent_bytes,DROP COLUMN intent_sha256,DROP COLUMN vega,DROP COLUMN theta,DROP COLUMN gamma,DROP COLUMN delta,DROP COLUMN quote_observed_at,DROP COLUMN required_capital_per_unit,DROP COLUMN max_loss_per_unit,DROP COLUMN expected_loss_usd,DROP COLUMN deployment_budget_usd,DROP COLUMN risk_policy_version,DROP COLUMN risk_policy_id,DROP COLUMN IF EXISTS capital_binding_id,DROP COLUMN promotion_decision_id,DROP COLUMN deployment_id,DROP COLUMN quality_result_id,DROP COLUMN manifest_id,DROP COLUMN evaluation_scope_id,DROP COLUMN execution_version_id;
DROP TABLE portfolio_account_snapshots;
DROP TABLE generated_strategy_scenario_bindings;
DROP TABLE generated_strategy_scenario_frames;
DROP TABLE generated_strategy_scenarios;
DROP FUNCTION validate_generated_strategy_scenario_graph();
DROP TABLE account_portfolio_risk_policy_bindings;
DROP TABLE portfolio_risk_policy_artifacts;
DROP FUNCTION validate_portfolio_account_snapshot();
DROP FUNCTION validate_portfolio_risk_binding();
DROP FUNCTION reject_portfolio_evidence_mutation();
