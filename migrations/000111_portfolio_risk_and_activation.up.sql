-- Migration 111 begins the content-addressed promotion/risk boundary. The
-- portfolio-risk policy and normalized allocation relations are added by the
-- same migration so activation and sizing deploy as one schema boundary.
LOCK TABLE strategies, strategy_versions, strategy_deployments,
  promotion_retirement_decisions, statistical_robustness_assessments,
  paper_evaluation_scopes IN SHARE ROW EXCLUSIVE MODE;

CREATE TABLE portfolio_risk_policy_artifacts (
  id UUID PRIMARY KEY,
  schema_name TEXT NOT NULL CHECK(schema_name='portfolio-risk-policy-v1'),
  version TEXT NOT NULL CHECK(version<>''),
  sha256 TEXT NOT NULL UNIQUE CHECK(sha256 ~ '^[0-9a-f]{64}$'),
  canonical_bytes BYTEA NOT NULL,
  canonical_json JSONB NOT NULL CHECK(jsonb_typeof(canonical_json)='object'),
  created_at TIMESTAMPTZ NOT NULL CHECK(created_at=date_trunc('microseconds',created_at)),
  CHECK(sha256=encode(digest(canonical_bytes,'sha256'),'hex')),
  CHECK(canonical_json=convert_from(canonical_bytes,'UTF8')::JSONB),
  CHECK(id=economic_deterministic_uuid('portfolio-risk-policy',schema_name||'@sha256:'||sha256))
);

CREATE TABLE account_portfolio_risk_policy_bindings (
  id UUID PRIMARY KEY,
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
  capital_binding_id UUID NOT NULL REFERENCES account_capital_policy_bindings(id) ON DELETE RESTRICT,
  policy_id UUID NOT NULL REFERENCES portfolio_risk_policy_artifacts(id) ON DELETE RESTRICT,
  policy_sha256 TEXT NOT NULL CHECK(policy_sha256 ~ '^[0-9a-f]{64}$'),
  effective_at TIMESTAMPTZ NOT NULL CHECK(effective_at=date_trunc('microseconds',effective_at)),
  sha256 TEXT NOT NULL CHECK(sha256 ~ '^[0-9a-f]{64}$'),
  canonical_bytes BYTEA NOT NULL,
  canonical_json JSONB NOT NULL CHECK(jsonb_typeof(canonical_json)='object'),
  created_at TIMESTAMPTZ NOT NULL CHECK(created_at=date_trunc('microseconds',created_at)),
  CHECK(sha256=encode(digest(canonical_bytes,'sha256'),'hex')),
  CHECK(canonical_json=convert_from(canonical_bytes,'UTF8')::JSONB),
  CHECK(id=economic_deterministic_uuid('account-portfolio-risk-policy-binding','account-portfolio-risk-policy-binding-v1@sha256:'||sha256)),
  UNIQUE(account_id,capital_binding_id,policy_id,effective_at)
);

CREATE TABLE portfolio_account_snapshots (
  id UUID PRIMARY KEY,
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
  environment TEXT NOT NULL CHECK(environment='paper_scored'),
  external_account_id TEXT NOT NULL CHECK(external_account_id<>''),
  observed_at TIMESTAMPTZ NOT NULL CHECK(observed_at=date_trunc('microseconds',observed_at)),
  equity NUMERIC(20,8) NOT NULL CHECK(equity>0),
  buying_power NUMERIC(20,8) NOT NULL CHECK(buying_power>=0),
  options_buying_power NUMERIC(20,8) NOT NULL CHECK(options_buying_power>=0),
  fallback_used BOOLEAN NOT NULL CHECK(NOT fallback_used),
  sha256 TEXT NOT NULL CHECK(sha256 ~ '^[0-9a-f]{64}$'),
  canonical_bytes BYTEA NOT NULL,
  canonical_json JSONB NOT NULL CHECK(jsonb_typeof(canonical_json)='object'),
  created_at TIMESTAMPTZ NOT NULL CHECK(created_at=date_trunc('microseconds',created_at)),
  CHECK(sha256=encode(digest(canonical_bytes,'sha256'),'hex')),
  CHECK(canonical_json=convert_from(canonical_bytes,'UTF8')::JSONB),
  CHECK(id=economic_deterministic_uuid('portfolio-account-snapshot','portfolio-account-snapshot-v1@sha256:'||sha256))
);

CREATE TABLE generated_strategy_scenarios (
  id UUID PRIMARY KEY,
  schema_name TEXT NOT NULL CHECK(schema_name='typed-generative-strategy-scenario-v1'),
  state TEXT NOT NULL CHECK(state='derived'),
  spec_id UUID NOT NULL REFERENCES generated_strategy_specs(id) ON DELETE RESTRICT,
  spec_sha256 TEXT NOT NULL CHECK(spec_sha256 ~ '^[0-9a-f]{64}$'),
  manifest_id UUID NOT NULL REFERENCES dataset_manifests(id) ON DELETE RESTRICT,
  manifest_sha256 TEXT NOT NULL CHECK(manifest_sha256 ~ '^[0-9a-f]{64}$'),
  mode TEXT NOT NULL CHECK(mode IN ('paper_scored','paper_stress')),
  evaluation_start TIMESTAMPTZ NOT NULL CHECK(evaluation_start=date_trunc('microseconds',evaluation_start)),
  evaluation_end TIMESTAMPTZ NOT NULL CHECK(evaluation_end=date_trunc('microseconds',evaluation_end) AND evaluation_start<evaluation_end),
  frame_count INTEGER NOT NULL CHECK(frame_count>0 AND frame_count<=100000),
  sha256 TEXT NOT NULL UNIQUE CHECK(sha256 ~ '^[0-9a-f]{64}$'),
  canonical_bytes BYTEA NOT NULL,
  canonical_json JSONB NOT NULL CHECK(jsonb_typeof(canonical_json)='object'),
  created_at TIMESTAMPTZ NOT NULL CHECK(created_at=date_trunc('microseconds',created_at)),
  CHECK(sha256=encode(digest(canonical_bytes,'sha256'),'hex')),
  CHECK(canonical_json=convert_from(canonical_bytes,'UTF8')::JSONB),
  CHECK(id=economic_deterministic_uuid('typed-generative-strategy-scenario',schema_name||'@sha256:'||sha256))
);

CREATE TABLE generated_strategy_scenario_frames (
  scenario_id UUID NOT NULL REFERENCES generated_strategy_scenarios(id) ON DELETE RESTRICT,
  sequence INTEGER NOT NULL CHECK(sequence>=0),
  instrument_id UUID NOT NULL REFERENCES instruments(id) ON DELETE RESTRICT,
  venue_contract_id UUID NOT NULL REFERENCES venue_contracts(id) ON DELETE RESTRICT,
  decision_at TIMESTAMPTZ NOT NULL CHECK(decision_at=date_trunc('microseconds',decision_at)),
  route_at TIMESTAMPTZ NOT NULL CHECK(route_at=date_trunc('microseconds',route_at) AND route_at>=decision_at),
  action TEXT NOT NULL CHECK(action IN ('noop','buy','sell')),
  input_count INTEGER NOT NULL CHECK(input_count>0),
  canonical_frame JSONB NOT NULL CHECK(jsonb_typeof(canonical_frame)='object'),
  PRIMARY KEY(scenario_id,sequence)
);

CREATE TABLE generated_strategy_scenario_bindings (
  scenario_id UUID NOT NULL,
  frame_sequence INTEGER NOT NULL,
  input_sequence INTEGER NOT NULL CHECK(input_sequence>=0),
  input_name TEXT NOT NULL CHECK(input_name<>''),
  dataset_kind TEXT NOT NULL CHECK(dataset_kind<>''),
  field_name TEXT NOT NULL CHECK(field_name<>''),
  payload_id UUID NOT NULL REFERENCES dataset_market_payloads(id) ON DELETE RESTRICT,
  payload_sha256 TEXT NOT NULL CHECK(payload_sha256 ~ '^[0-9a-f]{64}$'),
  partition_content_sha256 TEXT NOT NULL CHECK(partition_content_sha256 ~ '^[0-9a-f]{64}$'),
  source_key TEXT NOT NULL CHECK(source_key<>''),
  available_at TIMESTAMPTZ NOT NULL CHECK(available_at=date_trunc('microseconds',available_at)),
  canonical_value TEXT NOT NULL,
  canonical_binding JSONB NOT NULL CHECK(jsonb_typeof(canonical_binding)='object'),
  PRIMARY KEY(scenario_id,frame_sequence,input_sequence),
  UNIQUE(scenario_id,frame_sequence,input_name),
  FOREIGN KEY(scenario_id,frame_sequence) REFERENCES generated_strategy_scenario_frames(scenario_id,sequence) ON DELETE RESTRICT
);

CREATE FUNCTION validate_generated_strategy_scenario_graph() RETURNS TRIGGER AS $$
DECLARE target UUID; scenario generated_strategy_scenarios%ROWTYPE;
BEGIN
  target:=COALESCE((to_jsonb(NEW)->>'scenario_id')::UUID,(to_jsonb(NEW)->>'id')::UUID);
  SELECT * INTO scenario FROM generated_strategy_scenarios WHERE id=target;
  IF NOT FOUND THEN RAISE EXCEPTION 'generated strategy scenario parent is missing'; END IF;
  IF scenario.spec_sha256<>(SELECT sha256 FROM generated_strategy_specs WHERE id=scenario.spec_id)
    OR scenario.manifest_sha256<>(SELECT sha256 FROM dataset_manifests WHERE id=scenario.manifest_id)
    OR scenario.frame_count<>(SELECT count(*) FROM generated_strategy_scenario_frames WHERE scenario_id=target)
    OR scenario.canonical_json->'frames'<>COALESCE((SELECT jsonb_agg(frame.canonical_frame ORDER BY frame.sequence) FROM generated_strategy_scenario_frames frame WHERE frame.scenario_id=target),'[]'::JSONB)
    OR EXISTS(SELECT 1 FROM generated_strategy_scenario_frames frame WHERE frame.scenario_id=target AND
      (frame.sequence<>((frame.canonical_frame->>'sequence')::INTEGER)
       OR frame.instrument_id<>((frame.canonical_frame->>'instrument_id')::UUID)
       OR frame.venue_contract_id<>((frame.canonical_frame->>'venue_contract_id')::UUID)
       OR frame.action<>(frame.canonical_frame->>'action')
       OR frame.input_count<>(SELECT count(*) FROM generated_strategy_scenario_bindings binding WHERE binding.scenario_id=target AND binding.frame_sequence=frame.sequence)
       OR frame.canonical_frame->'bindings'<>COALESCE((SELECT jsonb_agg(binding.canonical_binding ORDER BY binding.input_sequence) FROM generated_strategy_scenario_bindings binding WHERE binding.scenario_id=target AND binding.frame_sequence=frame.sequence),'[]'::JSONB)))
    OR EXISTS(SELECT 1 FROM generated_strategy_scenario_bindings binding
      LEFT JOIN dataset_market_payloads payload ON payload.id=binding.payload_id AND payload.sha256=binding.payload_sha256
      WHERE binding.scenario_id=target AND (payload.id IS NULL
        OR binding.input_name<>binding.canonical_binding->>'name'
        OR binding.dataset_kind<>binding.canonical_binding->>'dataset_kind'
        OR binding.field_name<>binding.canonical_binding->>'field'
        OR binding.payload_id<>((binding.canonical_binding->>'payload_id')::UUID)
        OR binding.payload_sha256<>binding.canonical_binding->>'payload_sha256'
        OR binding.partition_content_sha256<>binding.canonical_binding->>'partition_content_sha256'
        OR binding.source_key<>binding.canonical_binding->>'source_key'
        OR binding.canonical_value<>binding.canonical_binding->>'value'
        OR binding.available_at<>payload.available_at
        OR NOT EXISTS(SELECT 1 FROM dataset_manifest_partitions partition
          JOIN dataset_manifest_observations observation ON observation.manifest_id=partition.manifest_id AND observation.partition_sequence=partition.sequence
          WHERE partition.manifest_id=scenario.manifest_id AND partition.content_sha256=binding.partition_content_sha256
            AND observation.source_key=binding.source_key AND observation.content_sha256=binding.payload_sha256 AND observation.available_at=binding.available_at))) THEN
    RAISE EXCEPTION 'generated strategy scenario graph does not reconstruct';
  END IF;
  RETURN NULL;
END; $$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER trg_generated_strategy_scenario_parent
  AFTER INSERT ON generated_strategy_scenarios DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION validate_generated_strategy_scenario_graph();
CREATE CONSTRAINT TRIGGER trg_generated_strategy_scenario_frame
  AFTER INSERT ON generated_strategy_scenario_frames DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION validate_generated_strategy_scenario_graph();
CREATE CONSTRAINT TRIGGER trg_generated_strategy_scenario_binding
  AFTER INSERT ON generated_strategy_scenario_bindings DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION validate_generated_strategy_scenario_graph();

CREATE FUNCTION validate_portfolio_risk_binding() RETURNS TRIGGER AS $$
BEGIN
  PERFORM 1
  FROM account_portfolio_risk_policy_bindings binding
  JOIN account_capital_policy_bindings capital ON capital.id=binding.capital_binding_id
    AND capital.account_id=binding.account_id AND capital.environment='paper_scored'
  JOIN portfolio_risk_policy_artifacts policy ON policy.id=binding.policy_id
    AND policy.sha256=binding.policy_sha256
  WHERE binding.id=NEW.id
    AND binding.canonical_json=jsonb_build_object(
      'schema','account-portfolio-risk-policy-binding-v1',
      'account_id',binding.account_id::TEXT,
      'capital_binding_id',binding.capital_binding_id::TEXT,
      'policy_id',binding.policy_id::TEXT,
      'policy_sha256',binding.policy_sha256,
      'effective_at',to_char(binding.effective_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'));
  IF NOT FOUND THEN RAISE EXCEPTION 'portfolio risk binding graph does not reconstruct'; END IF;
  RETURN NEW;
END; $$ LANGUAGE plpgsql;
CREATE TRIGGER trg_account_portfolio_risk_policy_bindings_validate
  AFTER INSERT ON account_portfolio_risk_policy_bindings
  FOR EACH ROW EXECUTE FUNCTION validate_portfolio_risk_binding();

CREATE FUNCTION validate_portfolio_account_snapshot() RETURNS TRIGGER AS $$
BEGIN
  PERFORM 1
  FROM portfolio_account_snapshots snapshot
  JOIN accounts account ON account.id=snapshot.account_id
    AND account.environment=snapshot.environment
    AND account.external_account_id=snapshot.external_account_id
    AND account.status='active'
  WHERE snapshot.id=NEW.id
    AND snapshot.canonical_json=jsonb_build_object(
      'schema','portfolio-account-snapshot-v1',
      'account_id',snapshot.account_id::TEXT,
      'environment',snapshot.environment,
      'external_account_id',snapshot.external_account_id,
      'observed_at',to_char(snapshot.observed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
      'equity',to_char(snapshot.equity,'FM99999999999999999990.00000000'),
      'buying_power',to_char(snapshot.buying_power,'FM99999999999999999990.00000000'),
      'options_buying_power',to_char(snapshot.options_buying_power,'FM99999999999999999990.00000000'),
      'fallback_used',false);
  IF NOT FOUND THEN RAISE EXCEPTION 'portfolio account snapshot does not match the canonical account'; END IF;
  RETURN NEW;
END; $$ LANGUAGE plpgsql;
CREATE TRIGGER trg_portfolio_account_snapshots_validate
  AFTER INSERT ON portfolio_account_snapshots
  FOR EACH ROW EXECUTE FUNCTION validate_portfolio_account_snapshot();

ALTER TABLE pipeline_runs
  ADD COLUMN execution_version_id UUID REFERENCES strategy_versions(id) ON DELETE RESTRICT,
  ADD COLUMN evaluation_scope_id UUID REFERENCES paper_evaluation_scopes(id) ON DELETE RESTRICT,
  ADD COLUMN manifest_id UUID REFERENCES dataset_manifests(id) ON DELETE RESTRICT,
  ADD COLUMN quality_result_id UUID REFERENCES dataset_quality_results(id) ON DELETE RESTRICT,
  ADD COLUMN deployment_id UUID REFERENCES strategy_deployments(id) ON DELETE RESTRICT,
  ADD COLUMN promotion_decision_id UUID REFERENCES promotion_retirement_decisions(id) ON DELETE RESTRICT,
  ADD COLUMN capital_binding_id UUID REFERENCES account_capital_policy_bindings(id) ON DELETE RESTRICT,
  ADD COLUMN risk_policy_version TEXT,
  ADD CONSTRAINT pipeline_run_promotion_lineage CHECK(
    (execution_version_id IS NULL AND evaluation_scope_id IS NULL AND manifest_id IS NULL AND quality_result_id IS NULL AND
     deployment_id IS NULL AND promotion_decision_id IS NULL AND capital_binding_id IS NULL AND risk_policy_version IS NULL) OR
    (execution_version_id IS NOT NULL AND evaluation_scope_id IS NOT NULL AND manifest_id IS NOT NULL AND quality_result_id IS NOT NULL AND
     deployment_id IS NOT NULL AND promotion_decision_id IS NOT NULL AND capital_binding_id IS NOT NULL AND risk_policy_version IS NOT NULL));

CREATE FUNCTION validate_pipeline_run_promotion_lineage() RETURNS TRIGGER AS $$
BEGIN
  IF NEW.execution_version_id IS NULL THEN RETURN NEW; END IF;
  PERFORM 1
  FROM strategy_promotion_activations activation
  JOIN strategy_deployments deployment ON deployment.id=NEW.deployment_id
    AND deployment.id=activation.deployment_id AND deployment.account_id=NEW.account_id
    AND deployment.capital_binding_id=NEW.capital_binding_id AND deployment.risk_policy_version=NEW.risk_policy_version
  JOIN promotion_retirement_decisions decision ON decision.id=NEW.promotion_decision_id
    AND decision.id=activation.decision_id AND decision.deployment_id=deployment.id
    AND decision.outcome='approved' AND decision.next_state='shadow'
  JOIN paper_evaluation_scopes scope ON scope.id=NEW.evaluation_scope_id
    AND scope.id=activation.scope_id AND scope.account_id=NEW.account_id
    AND scope.capital_binding_id=NEW.capital_binding_id
  JOIN dataset_manifests manifest ON manifest.id=NEW.manifest_id AND manifest.sha256=scope.manifest_sha256
  JOIN dataset_quality_results quality ON quality.id=NEW.quality_result_id
    AND quality.manifest_id=manifest.id AND quality.sha256=scope.quality_sha256 AND NOT quality.quarantined
  WHERE activation.action='activate' AND activation.strategy_id=NEW.strategy_id
    AND activation.runtime_version_id=NEW.execution_version_id
    AND activation.account_id=NEW.account_id AND activation.capital_binding_id=NEW.capital_binding_id
    AND activation.risk_policy_version=NEW.risk_policy_version
    AND NEW.environment='paper_scored' AND NEW.origin_type='strategy_version'
    AND NEW.origin_id=NEW.execution_version_id::TEXT
    AND NOT EXISTS(SELECT 1 FROM promotion_retirement_decisions child WHERE child.prior_decision_id=decision.id)
    AND NOT EXISTS(SELECT 1 FROM strategy_promotion_activations child WHERE child.prior_activation_id=activation.id);
  IF NOT FOUND THEN RAISE EXCEPTION 'pipeline run promotion graph does not reconstruct'; END IF;
  RETURN NEW;
END; $$ LANGUAGE plpgsql;
CREATE TRIGGER trg_pipeline_run_promotion_lineage BEFORE INSERT ON pipeline_runs
  FOR EACH ROW EXECUTE FUNCTION validate_pipeline_run_promotion_lineage();

ALTER TABLE portfolio_opportunities
  ADD COLUMN execution_version_id UUID REFERENCES strategy_versions(id) ON DELETE RESTRICT,
  ADD COLUMN evaluation_scope_id UUID REFERENCES paper_evaluation_scopes(id) ON DELETE RESTRICT,
  ADD COLUMN manifest_id UUID REFERENCES dataset_manifests(id) ON DELETE RESTRICT,
  ADD COLUMN quality_result_id UUID REFERENCES dataset_quality_results(id) ON DELETE RESTRICT,
  ADD COLUMN deployment_id UUID REFERENCES strategy_deployments(id) ON DELETE RESTRICT,
  ADD COLUMN promotion_decision_id UUID REFERENCES promotion_retirement_decisions(id) ON DELETE RESTRICT,
  ADD COLUMN capital_binding_id UUID REFERENCES account_capital_policy_bindings(id) ON DELETE RESTRICT,
  ADD COLUMN risk_policy_id UUID REFERENCES portfolio_risk_policy_artifacts(id) ON DELETE RESTRICT,
  ADD COLUMN risk_policy_version TEXT,
  ADD COLUMN deployment_budget_usd NUMERIC(20,8),
  ADD COLUMN expected_loss_usd NUMERIC(20,8),
  ADD COLUMN max_loss_per_unit NUMERIC(20,8),
  ADD COLUMN required_capital_per_unit NUMERIC(20,8),
  ADD COLUMN quote_observed_at TIMESTAMPTZ,
  ADD COLUMN delta NUMERIC(20,8), ADD COLUMN gamma NUMERIC(20,8), ADD COLUMN theta NUMERIC(20,8), ADD COLUMN vega NUMERIC(20,8),
  ADD COLUMN intent_sha256 TEXT CHECK(intent_sha256 IS NULL OR intent_sha256 ~ '^[0-9a-f]{64}$'),
  ADD COLUMN intent_bytes BYTEA,
  ADD CONSTRAINT portfolio_opportunity_promotion_lineage CHECK(
    (execution_version_id IS NULL AND evaluation_scope_id IS NULL AND manifest_id IS NULL AND quality_result_id IS NULL AND
     deployment_id IS NULL AND promotion_decision_id IS NULL AND capital_binding_id IS NULL AND risk_policy_id IS NULL AND risk_policy_version IS NULL AND intent_sha256 IS NULL AND intent_bytes IS NULL) OR
    (execution_version_id IS NOT NULL AND evaluation_scope_id IS NOT NULL AND manifest_id IS NOT NULL AND quality_result_id IS NOT NULL AND
     deployment_id IS NOT NULL AND promotion_decision_id IS NOT NULL AND capital_binding_id IS NOT NULL AND risk_policy_id IS NOT NULL AND risk_policy_version IS NOT NULL AND intent_sha256 IS NOT NULL AND intent_bytes IS NOT NULL)),
  ADD CONSTRAINT portfolio_opportunity_intent_hash CHECK(intent_sha256 IS NULL OR intent_sha256=encode(digest(intent_bytes,'sha256'),'hex'));

CREATE FUNCTION validate_portfolio_opportunity_promotion_lineage() RETURNS TRIGGER AS $$
BEGIN
  IF NEW.execution_version_id IS NULL THEN RETURN NEW; END IF;
  PERFORM 1
  FROM pipeline_runs run
  JOIN paper_evaluation_scopes scope ON scope.id=NEW.evaluation_scope_id
    AND scope.account_id=NEW.account_id AND scope.capital_binding_id=NEW.capital_binding_id
  JOIN dataset_manifests manifest ON manifest.id=NEW.manifest_id AND manifest.sha256=scope.manifest_sha256
  JOIN dataset_quality_results quality ON quality.id=NEW.quality_result_id
    AND quality.manifest_id=manifest.id AND quality.sha256=scope.quality_sha256 AND NOT quality.quarantined
  JOIN strategy_deployments deployment ON deployment.id=NEW.deployment_id
    AND deployment.version_id=NEW.execution_version_id AND deployment.account_id=NEW.account_id
    AND deployment.capital_binding_id=NEW.capital_binding_id
  JOIN promotion_retirement_decisions decision ON decision.id=NEW.promotion_decision_id
    AND decision.deployment_id=deployment.id AND decision.outcome='approved' AND decision.next_state='shadow'
  JOIN strategy_promotion_activations activation ON activation.deployment_id=deployment.id
    AND activation.strategy_id=NEW.strategy_id AND activation.runtime_version_id=NEW.execution_version_id
    AND activation.action='activate'
  JOIN portfolio_risk_policy_artifacts risk ON risk.id=NEW.risk_policy_id
    AND risk.schema_name||'@sha256:'||risk.sha256=NEW.risk_policy_version
  WHERE run.id=NEW.pipeline_run_id AND run.trade_date=NEW.pipeline_run_trade_date
    AND run.account_id=NEW.account_id AND run.environment=NEW.environment
    AND run.origin_type=NEW.origin_type AND run.origin_id=NEW.origin_id
    AND run.strategy_id=NEW.strategy_id AND run.execution_version_id=NEW.execution_version_id
    AND run.evaluation_scope_id=NEW.evaluation_scope_id AND run.manifest_id=NEW.manifest_id
    AND run.quality_result_id=NEW.quality_result_id AND run.deployment_id=NEW.deployment_id
    AND run.promotion_decision_id=NEW.promotion_decision_id AND run.capital_binding_id=NEW.capital_binding_id
    AND run.risk_policy_version=NEW.risk_policy_version
    AND NOT EXISTS(SELECT 1 FROM promotion_retirement_decisions child WHERE child.prior_decision_id=decision.id)
    AND NOT EXISTS(SELECT 1 FROM strategy_promotion_activations child WHERE child.prior_activation_id=activation.id);
  IF NOT FOUND THEN RAISE EXCEPTION 'portfolio opportunity promotion graph does not reconstruct'; END IF;
  RETURN NEW;
END; $$ LANGUAGE plpgsql;
CREATE TRIGGER trg_portfolio_opportunity_promotion_lineage BEFORE INSERT ON portfolio_opportunities
  FOR EACH ROW EXECUTE FUNCTION validate_portfolio_opportunity_promotion_lineage();

CREATE TABLE portfolio_opportunity_option_legs (
  opportunity_id UUID NOT NULL REFERENCES portfolio_opportunities(id) ON DELETE RESTRICT,
  sequence INTEGER NOT NULL CHECK(sequence IN (0,1)),
  contract_id UUID NOT NULL REFERENCES instruments(id) ON DELETE RESTRICT,
  occ_symbol TEXT NOT NULL CHECK(occ_symbol<>''), underlying TEXT NOT NULL CHECK(underlying<>''),
  expiry TIMESTAMPTZ NOT NULL, option_type TEXT NOT NULL CHECK(option_type IN ('call','put')),
  strike NUMERIC(20,8) NOT NULL CHECK(strike>0), ratio INTEGER NOT NULL CHECK(ratio=1),
  side TEXT NOT NULL CHECK(side IN ('buy','sell')),
  position_intent TEXT NOT NULL CHECK(position_intent IN ('buy_to_open','sell_to_open')),
  bid NUMERIC(20,8) NOT NULL CHECK(bid>0), ask NUMERIC(20,8) NOT NULL CHECK(ask>=bid),
  multiplier INTEGER NOT NULL CHECK(multiplier=100),
  PRIMARY KEY(opportunity_id,sequence), UNIQUE(opportunity_id,contract_id), UNIQUE(opportunity_id,occ_symbol)
);

CREATE FUNCTION validate_portfolio_option_package() RETURNS TRIGGER AS $$
DECLARE target UUID;
BEGIN
  target:=COALESCE((to_jsonb(NEW)->>'opportunity_id')::UUID,(to_jsonb(NEW)->>'id')::UUID);
  PERFORM 1 FROM portfolio_opportunities opportunity
  WHERE opportunity.id=target AND (opportunity.intent_sha256 IS NULL OR opportunity.market_type<>'options' OR (
    opportunity.max_loss_per_unit>0 AND opportunity.required_capital_per_unit>0 AND opportunity.quote_observed_at IS NOT NULL
    AND opportunity.ticker=upper(opportunity.ticker)
    AND (SELECT count(*)=2 AND min(sequence)=0 AND max(sequence)=1
      AND count(*) FILTER(WHERE side='buy' AND position_intent='buy_to_open')=1
      AND count(*) FILTER(WHERE side='sell' AND position_intent='sell_to_open')=1
      AND count(DISTINCT underlying)=1 AND min(underlying)=opportunity.ticker
      AND count(DISTINCT expiry)=1 AND count(DISTINCT option_type)=1 AND count(DISTINCT strike)=2
      FROM portfolio_opportunity_option_legs WHERE opportunity_id=opportunity.id)
    AND NOT EXISTS(
      SELECT 1 FROM portfolio_opportunity_option_legs leg WHERE leg.opportunity_id=opportunity.id AND NOT EXISTS(
        SELECT 1 FROM dataset_manifest_payload_bindings binding
        JOIN dataset_market_payloads payload ON payload.id=binding.payload_id
        WHERE binding.manifest_id=opportunity.manifest_id AND payload.payload_kind='option_contract'
          AND payload.instrument_id=leg.contract_id AND payload.symbol=leg.occ_symbol AND payload.underlying_symbol=leg.underlying
          AND payload.canonical_json#>>'{contract,option_type}'=leg.option_type
          AND (payload.canonical_json#>>'{contract,strike}')::numeric=leg.strike
          AND payload.canonical_json#>>'{contract,expiry}'=to_char(leg.expiry AT TIME ZONE 'UTC','YYYY-MM-DD')
          AND (payload.canonical_json#>>'{contract,multiplier}')::numeric=leg.multiplier))
    AND NOT EXISTS(
      SELECT 1 FROM portfolio_opportunity_option_legs leg WHERE leg.opportunity_id=opportunity.id AND NOT EXISTS(
        SELECT 1 FROM dataset_manifest_payload_bindings binding
        JOIN dataset_market_payloads payload ON payload.id=binding.payload_id
        WHERE binding.manifest_id=opportunity.manifest_id AND payload.payload_kind='option_snapshot'
          AND payload.instrument_id=leg.contract_id AND payload.symbol=leg.occ_symbol
          AND payload.observed_at=opportunity.quote_observed_at
          AND (payload.canonical_json#>>'{snapshot,quote,bid_price}')::numeric=leg.bid
          AND (payload.canonical_json#>>'{snapshot,quote,ask_price}')::numeric=leg.ask))
    AND opportunity.delta=(SELECT sum(CASE side WHEN 'buy' THEN 1 ELSE -1 END*ratio*multiplier*(payload.canonical_json#>>'{snapshot,delta}')::numeric)
      FROM portfolio_opportunity_option_legs leg
      JOIN LATERAL (SELECT payload.canonical_json FROM dataset_manifest_payload_bindings binding
        JOIN dataset_market_payloads payload ON payload.id=binding.payload_id
        WHERE binding.manifest_id=opportunity.manifest_id AND payload.payload_kind='option_snapshot'
          AND payload.instrument_id=leg.contract_id AND payload.observed_at=opportunity.quote_observed_at
          AND (payload.canonical_json#>>'{snapshot,quote,bid_price}')::numeric=leg.bid
          AND (payload.canonical_json#>>'{snapshot,quote,ask_price}')::numeric=leg.ask LIMIT 1) payload ON true
      WHERE leg.opportunity_id=opportunity.id)
    AND opportunity.gamma=(SELECT sum(CASE side WHEN 'buy' THEN 1 ELSE -1 END*ratio*multiplier*(payload.canonical_json#>>'{snapshot,gamma}')::numeric)
      FROM portfolio_opportunity_option_legs leg JOIN LATERAL (SELECT payload.canonical_json FROM dataset_manifest_payload_bindings binding JOIN dataset_market_payloads payload ON payload.id=binding.payload_id
        WHERE binding.manifest_id=opportunity.manifest_id AND payload.payload_kind='option_snapshot' AND payload.instrument_id=leg.contract_id AND payload.observed_at=opportunity.quote_observed_at LIMIT 1) payload ON true WHERE leg.opportunity_id=opportunity.id)
    AND opportunity.theta=(SELECT sum(CASE side WHEN 'buy' THEN 1 ELSE -1 END*ratio*multiplier*(payload.canonical_json#>>'{snapshot,theta}')::numeric)
      FROM portfolio_opportunity_option_legs leg JOIN LATERAL (SELECT payload.canonical_json FROM dataset_manifest_payload_bindings binding JOIN dataset_market_payloads payload ON payload.id=binding.payload_id
        WHERE binding.manifest_id=opportunity.manifest_id AND payload.payload_kind='option_snapshot' AND payload.instrument_id=leg.contract_id AND payload.observed_at=opportunity.quote_observed_at LIMIT 1) payload ON true WHERE leg.opportunity_id=opportunity.id)
    AND opportunity.vega=(SELECT sum(CASE side WHEN 'buy' THEN 1 ELSE -1 END*ratio*multiplier*(payload.canonical_json#>>'{snapshot,vega}')::numeric)
      FROM portfolio_opportunity_option_legs leg JOIN LATERAL (SELECT payload.canonical_json FROM dataset_manifest_payload_bindings binding JOIN dataset_market_payloads payload ON payload.id=binding.payload_id
        WHERE binding.manifest_id=opportunity.manifest_id AND payload.payload_kind='option_snapshot' AND payload.instrument_id=leg.contract_id AND payload.observed_at=opportunity.quote_observed_at LIMIT 1) payload ON true WHERE leg.opportunity_id=opportunity.id)
  ));
  IF NOT FOUND THEN RAISE EXCEPTION 'portfolio option package does not reconstruct immutable manifest evidence'; END IF;
  RETURN NULL;
END; $$ LANGUAGE plpgsql;
CREATE CONSTRAINT TRIGGER trg_portfolio_option_package_parent
  AFTER INSERT ON portfolio_opportunities DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION validate_portfolio_option_package();
CREATE CONSTRAINT TRIGGER trg_portfolio_option_package_leg
  AFTER INSERT ON portfolio_opportunity_option_legs DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION validate_portfolio_option_package();

ALTER TABLE allocation_decisions
  ADD COLUMN risk_policy_id UUID REFERENCES portfolio_risk_policy_artifacts(id) ON DELETE RESTRICT,
  ADD COLUMN risk_policy_version TEXT,
  ADD COLUMN account_snapshot_id UUID REFERENCES portfolio_account_snapshots(id) ON DELETE RESTRICT,
  ADD COLUMN proposed_quantity NUMERIC(20,8), ADD COLUMN max_loss_per_unit NUMERIC(20,8),
  ADD COLUMN reserved_risk_usd NUMERIC(20,8), ADD COLUMN reserved_capital_usd NUMERIC(20,8),
  ADD COLUMN exposure_before_usd NUMERIC(20,8), ADD COLUMN exposure_after_usd NUMERIC(20,8),
  ADD COLUMN binding_constraint TEXT, ADD COLUMN execution_route TEXT;

ALTER TABLE allocation_decisions
  ADD COLUMN risk_state_sha256 TEXT CHECK(risk_state_sha256 IS NULL OR risk_state_sha256 ~ '^[0-9a-f]{64}$'),
  ADD COLUMN risk_state_bytes BYTEA,
  ADD CONSTRAINT allocation_decision_risk_state_pair CHECK((risk_state_sha256 IS NULL)=(risk_state_bytes IS NULL)),
  ADD CONSTRAINT allocation_decision_risk_state_hash CHECK(risk_state_sha256 IS NULL OR risk_state_sha256=encode(digest(risk_state_bytes,'sha256'),'hex'));

DROP INDEX orders_allocation_effect_once;
CREATE UNIQUE INDEX orders_stock_allocation_effect_once
  ON orders(account_id,allocation_opportunity_id)
  WHERE allocation_opportunity_id IS NOT NULL AND leg_group_id IS NULL;
CREATE UNIQUE INDEX orders_option_allocation_leg_once
  ON orders(account_id,allocation_opportunity_id,leg_group_id,ticker)
  WHERE allocation_opportunity_id IS NOT NULL AND leg_group_id IS NOT NULL;

CREATE TABLE allocation_risk_caps (
  decision_id UUID NOT NULL REFERENCES allocation_decisions(id) ON DELETE RESTRICT,
  sequence INTEGER NOT NULL CHECK(sequence>=0), name TEXT NOT NULL CHECK(name<>''),
  available_amount NUMERIC(20,8) NOT NULL, unit_amount NUMERIC(20,8) NOT NULL CHECK(unit_amount>0),
  quantity_cap NUMERIC(20,8) NOT NULL CHECK(quantity_cap>=0), binding BOOLEAN NOT NULL,
  PRIMARY KEY(decision_id,sequence), UNIQUE(decision_id,name)
);

CREATE FUNCTION reject_portfolio_evidence_mutation() RETURNS TRIGGER AS $$
BEGIN RAISE EXCEPTION 'portfolio evidence is append-only'; END; $$ LANGUAGE plpgsql;
DO $$ DECLARE name TEXT; BEGIN FOREACH name IN ARRAY ARRAY['portfolio_risk_policy_artifacts','account_portfolio_risk_policy_bindings','portfolio_account_snapshots','generated_strategy_scenarios','generated_strategy_scenario_frames','generated_strategy_scenario_bindings','portfolio_opportunity_option_legs','allocation_risk_caps'] LOOP
  EXECUTE format('CREATE TRIGGER trg_%s_immutable BEFORE UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION reject_portfolio_evidence_mutation()',name,name);
END LOOP; END $$;

CREATE FUNCTION preserve_portfolio_opportunity_intent() RETURNS TRIGGER AS $$
BEGIN
  IF OLD.intent_sha256 IS NOT NULL AND (NEW.intent_sha256<>OLD.intent_sha256 OR NEW.intent_bytes<>OLD.intent_bytes OR
    NEW.execution_version_id<>OLD.execution_version_id OR NEW.evaluation_scope_id<>OLD.evaluation_scope_id OR
    NEW.manifest_id<>OLD.manifest_id OR NEW.quality_result_id<>OLD.quality_result_id OR NEW.deployment_id<>OLD.deployment_id OR
    NEW.promotion_decision_id<>OLD.promotion_decision_id OR NEW.capital_binding_id<>OLD.capital_binding_id OR NEW.risk_policy_id<>OLD.risk_policy_id) THEN
    RAISE EXCEPTION 'portfolio opportunity execution intent is immutable';
  END IF;
  RETURN NEW;
END; $$ LANGUAGE plpgsql;
CREATE TRIGGER trg_portfolio_opportunity_preserve_intent BEFORE UPDATE ON portfolio_opportunities
  FOR EACH ROW EXECUTE FUNCTION preserve_portfolio_opportunity_intent();

CREATE FUNCTION preserve_allocation_risk_evidence() RETURNS TRIGGER AS $$
BEGIN
  IF NEW.risk_policy_id IS DISTINCT FROM OLD.risk_policy_id OR NEW.risk_policy_version IS DISTINCT FROM OLD.risk_policy_version OR
    NEW.account_snapshot_id IS DISTINCT FROM OLD.account_snapshot_id OR NEW.risk_state_sha256 IS DISTINCT FROM OLD.risk_state_sha256 OR
    NEW.risk_state_bytes IS DISTINCT FROM OLD.risk_state_bytes OR NEW.proposed_quantity IS DISTINCT FROM OLD.proposed_quantity OR
    NEW.max_loss_per_unit IS DISTINCT FROM OLD.max_loss_per_unit OR NEW.reserved_risk_usd IS DISTINCT FROM OLD.reserved_risk_usd OR
    NEW.reserved_capital_usd IS DISTINCT FROM OLD.reserved_capital_usd OR NEW.exposure_before_usd IS DISTINCT FROM OLD.exposure_before_usd OR
    NEW.exposure_after_usd IS DISTINCT FROM OLD.exposure_after_usd OR NEW.binding_constraint IS DISTINCT FROM OLD.binding_constraint OR
    NEW.execution_route IS DISTINCT FROM OLD.execution_route THEN
    RAISE EXCEPTION 'allocation risk evidence is immutable';
  END IF;
  RETURN NEW;
END; $$ LANGUAGE plpgsql;
CREATE TRIGGER trg_allocation_decision_preserve_risk BEFORE UPDATE ON allocation_decisions
  FOR EACH ROW EXECUTE FUNCTION preserve_allocation_risk_evidence();

CREATE TABLE strategy_promotion_activations (
  id UUID PRIMARY KEY,
  schema_name TEXT NOT NULL CHECK(schema_name='strategy-promotion-activation-v1'),
  action TEXT NOT NULL CHECK(action IN ('activate','suspend')),
  deployment_id UUID NOT NULL REFERENCES strategy_deployments(id) ON DELETE RESTRICT,
  deployment_sha256 TEXT NOT NULL CHECK(deployment_sha256 ~ '^[0-9a-f]{64}$'),
  decision_id UUID NOT NULL UNIQUE REFERENCES promotion_retirement_decisions(id) ON DELETE RESTRICT,
  decision_sha256 TEXT NOT NULL CHECK(decision_sha256 ~ '^[0-9a-f]{64}$'),
  strategy_id UUID NOT NULL REFERENCES strategies(id) ON DELETE RESTRICT,
  source_version_id UUID NOT NULL REFERENCES strategy_versions(id) ON DELETE RESTRICT,
  runtime_version_id UUID NOT NULL REFERENCES strategy_versions(id) ON DELETE RESTRICT,
  runtime_version_sha256 TEXT NOT NULL CHECK(runtime_version_sha256 ~ '^[0-9a-f]{64}$'),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
  scope_id UUID NOT NULL REFERENCES paper_evaluation_scopes(id) ON DELETE RESTRICT,
  capital_binding_id UUID NOT NULL REFERENCES account_capital_policy_bindings(id) ON DELETE RESTRICT,
  schedule_cron TEXT NOT NULL,
  timezone_name TEXT NOT NULL CHECK(timezone_name<>''),
  risk_policy_version TEXT NOT NULL CHECK(risk_policy_version<>''),
  prior_activation_id UUID REFERENCES strategy_promotion_activations(id) ON DELETE RESTRICT,
  prior_activation_sha256 TEXT NOT NULL DEFAULT '' CHECK(
    (prior_activation_id IS NULL AND prior_activation_sha256='') OR
    (prior_activation_id IS NOT NULL AND prior_activation_sha256 ~ '^[0-9a-f]{64}$')),
  sha256 TEXT NOT NULL CHECK(sha256 ~ '^[0-9a-f]{64}$'),
  canonical_bytes BYTEA NOT NULL,
  canonical_json JSONB NOT NULL CHECK(jsonb_typeof(canonical_json)='object'),
  created_at TIMESTAMPTZ NOT NULL CHECK(created_at=date_trunc('microseconds',created_at)),
  CHECK((action='activate' AND schedule_cron<>'') OR (action='suspend' AND schedule_cron='')),
  CHECK(sha256=encode(digest(canonical_bytes,'sha256'),'hex')),
  CHECK(canonical_json=convert_from(canonical_bytes,'UTF8')::JSONB),
  CHECK(id=economic_deterministic_uuid('strategy-promotion-activation',schema_name||'@sha256:'||sha256)),
  UNIQUE(deployment_id,prior_activation_id)
);

CREATE UNIQUE INDEX uq_strategy_promotion_initial_activation
  ON strategy_promotion_activations(deployment_id)
  WHERE prior_activation_id IS NULL;

CREATE FUNCTION validate_strategy_promotion_activation() RETURNS TRIGGER AS $$
DECLARE target UUID;
BEGIN
  target:=COALESCE((to_jsonb(NEW)->>'id')::UUID,(to_jsonb(NEW)->>'prior_activation_id')::UUID);
  PERFORM 1
  FROM strategy_promotion_activations activation
  JOIN strategy_deployments deployment ON deployment.id=activation.deployment_id
  JOIN promotion_retirement_decisions decision ON decision.id=activation.decision_id
  JOIN statistical_robustness_assessments assessment ON assessment.id=decision.assessment_id
  JOIN strategy_versions runtime_version ON runtime_version.id=activation.runtime_version_id
  JOIN paper_evaluation_scopes scope ON scope.id=activation.scope_id
  JOIN portfolio_risk_policy_artifacts risk_policy ON risk_policy.schema_name||'@sha256:'||risk_policy.sha256=activation.risk_policy_version
  JOIN account_portfolio_risk_policy_bindings risk_binding ON risk_binding.account_id=activation.account_id
    AND risk_binding.capital_binding_id=activation.capital_binding_id AND risk_binding.policy_id=risk_policy.id
  LEFT JOIN strategy_promotion_activations prior ON prior.id=activation.prior_activation_id
  WHERE activation.id=target
    AND activation.deployment_sha256=deployment.sha256
    AND activation.decision_sha256=decision.sha256
    AND decision.deployment_id=deployment.id
    AND decision.version_id=deployment.version_id
    AND activation.source_version_id=deployment.version_id
    AND activation.runtime_version_sha256=runtime_version.sha256
    AND activation.account_id=deployment.account_id
    AND activation.account_id=scope.account_id
    AND activation.capital_binding_id=deployment.capital_binding_id
    AND activation.capital_binding_id=scope.capital_binding_id
    AND assessment.scope_id=scope.id
    AND activation.schedule_cron=CASE WHEN activation.action='activate' THEN deployment.schedule_cron ELSE '' END
    AND activation.timezone_name=deployment.timezone_name
    AND activation.risk_policy_version=deployment.risk_policy_version
    AND ((activation.prior_activation_id IS NULL AND activation.prior_activation_sha256='') OR
         (prior.deployment_id=activation.deployment_id AND activation.prior_activation_sha256=prior.sha256))
    AND ((activation.action='activate' AND decision.outcome='approved' AND decision.next_state='shadow') OR
         (activation.action='suspend' AND decision.outcome IN ('held','retired')))
    AND NOT EXISTS(SELECT 1 FROM promotion_retirement_decisions child WHERE child.prior_decision_id=decision.id)
    AND activation.canonical_json=jsonb_build_object(
      'schema',activation.schema_name,'action',activation.action,
      'deployment_id',activation.deployment_id::TEXT,'deployment_sha256',activation.deployment_sha256,
      'decision_id',activation.decision_id::TEXT,'decision_sha256',activation.decision_sha256,
      'strategy_id',activation.strategy_id::TEXT,'source_version_id',activation.source_version_id::TEXT,
      'runtime_version_id',activation.runtime_version_id::TEXT,'runtime_version_sha256',activation.runtime_version_sha256,
      'account_id',activation.account_id::TEXT,'scope_id',activation.scope_id::TEXT,
      'capital_binding_id',activation.capital_binding_id::TEXT,'schedule_cron',activation.schedule_cron,
      'timezone',activation.timezone_name,'risk_policy_version',activation.risk_policy_version,
      'prior_activation_id',COALESCE(activation.prior_activation_id::TEXT,''),
      'prior_activation_sha256',activation.prior_activation_sha256)
    AND (EXISTS(SELECT 1 FROM strategy_promotion_activations child WHERE child.prior_activation_id=activation.id) OR
         EXISTS(SELECT 1 FROM strategies strategy WHERE strategy.id=activation.strategy_id
           AND strategy.execution_strategy_version_id=activation.runtime_version_id
           AND strategy.is_paper
           AND strategy.status=CASE WHEN activation.action='activate' THEN 'active' ELSE 'inactive' END
           AND strategy.schedule_cron=activation.schedule_cron));
  IF NOT FOUND THEN RAISE EXCEPTION 'strategy promotion activation graph does not reconstruct'; END IF;
  RETURN NULL;
END; $$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER trg_strategy_promotion_activation_graph
  AFTER INSERT ON strategy_promotion_activations DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION validate_strategy_promotion_activation();
CREATE CONSTRAINT TRIGGER trg_strategy_promotion_activation_prior_graph
  AFTER INSERT ON strategy_promotion_activations DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW WHEN(NEW.prior_activation_id IS NOT NULL) EXECUTE FUNCTION validate_strategy_promotion_activation();
CREATE TRIGGER trg_strategy_promotion_activation_immutable
  BEFORE UPDATE OR DELETE ON strategy_promotion_activations
  FOR EACH ROW EXECUTE FUNCTION reject_promotion_mutation();
