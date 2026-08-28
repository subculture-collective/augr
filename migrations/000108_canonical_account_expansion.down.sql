LOCK TABLE pipeline_runs, pipeline_run_snapshots, agent_decisions, agent_events,
    trade_decisions, orders, positions, trades, portfolio_opportunities,
    allocation_decisions, replay_events, financial_fill_idempotency,
    prediction_settlement_idempotency, execution_intents, execution_orders,
    copy_subscriptions, copy_trade_intents, copy_origin_rebalance_runs,
    copy_origin_rebalance_intents, copy_target_drift_runs, copy_target_drift_legs,
    strategies, projection_checkpoints, ledger_transactions, account_projection_outbox,
    account_capital_policy_bindings, capital_margin_policy_artifacts, conversations,
    conversation_messages, agent_memories IN ACCESS EXCLUSIVE MODE;

DO $rollback$
DECLARE
    reviewed_json CONSTANT JSONB := $policy${"schema":"capital-margin-policy-v1","currency":"USD","scale":12,"tiers":["500","5000","25000","100000","1000000","5000000"],"profiles":[{"name":"cash","initial_long":"1","initial_short":"0","maintenance_long":"1","maintenance_short":"0","maximum_gross":"1","cash_reserve":"0","allow_short":false,"unlimited":false},{"name":"portfolio","initial_long":"0.15","initial_short":"0.3","maintenance_long":"0.15","maintenance_short":"0.3","maximum_gross":"6","cash_reserve":"0","allow_short":true,"unlimited":false},{"name":"reg_t","initial_long":"0.5","initial_short":"1.5","maintenance_long":"0.25","maintenance_short":"0.3","maximum_gross":"2","cash_reserve":"0","allow_short":true,"unlimited":false},{"name":"stress_unlimited","initial_long":"0","initial_short":"0","maintenance_long":"0","maintenance_short":"0","maximum_gross":"0","cash_reserve":"0","allow_short":true,"unlimited":true}]}$policy$::JSONB;
    seed_bytes BYTEA;
    seed_sha TEXT;
    seed_version TEXT;
    seed_artifact_id UUID;
    seed_binding_id UUID;
    affected BIGINT;
BEGIN
    seed_bytes := capital_margin_policy_v1_canonical_bytes(reviewed_json);
    seed_sha := encode(digest(seed_bytes,'sha256'),'hex');
    seed_version := 'capital-margin-policy-v1@sha256:' || seed_sha;
    seed_artifact_id := economic_deterministic_uuid('capital-margin-policy-artifact',seed_version);
    seed_binding_id := economic_deterministic_uuid('capital-policy-binding','00000000-0000-4000-8000-000000000064',seed_version);

    IF (SELECT count(*) FROM capital_margin_policy_artifacts
        WHERE id=seed_artifact_id AND schema_name='capital-margin-policy-v1' AND policy_version=seed_version
          AND sha256=seed_sha AND canonical_bytes=seed_bytes AND canonical_json=reviewed_json)<>1 THEN
        RAISE EXCEPTION 'cannot roll back migration 108: seeded capital policy artifact is missing or modified';
    END IF;
    IF (SELECT count(*) FROM account_capital_policy_bindings
        WHERE id=seed_binding_id AND account_id='00000000-0000-4000-8000-000000000064'::UUID
          AND policy_artifact_id=seed_artifact_id AND policy_version=seed_version
          AND tier=100000 AND margin_profile='reg_t' AND environment='paper_scored'
          AND starting_capital=100000 AND buying_power_multiplier=2
          AND evidence_class='promotion_evidence' AND storage_namespace='paper_scored/default' AND currency='USD')<>1 THEN
        RAISE EXCEPTION 'cannot roll back migration 108: seeded capital policy binding is missing or modified';
    END IF;
    IF EXISTS(SELECT 1 FROM account_capital_policy_bindings WHERE policy_artifact_id=seed_artifact_id AND id<>seed_binding_id) THEN
        RAISE EXCEPTION 'cannot roll back migration 108: another binding references the seeded artifact';
    END IF;
    IF EXISTS(SELECT 1 FROM account_projection_outbox) THEN
        RAISE EXCEPTION 'cannot roll back migration 108 while projection outbox rows exist';
    END IF;
    IF EXISTS(
        SELECT 1 FROM projection_checkpoints checkpoint
        WHERE checkpoint.projection_version IS NOT NULL
          AND checkpoint.through_transaction_id IS DISTINCT FROM (
              SELECT transaction.id FROM ledger_transactions transaction
              WHERE transaction.account_id=checkpoint.account_id
                AND transaction.effective_at<=checkpoint.as_of AND transaction.observed_at<=checkpoint.as_of
              ORDER BY transaction.effective_at DESC,transaction.observed_at DESC,transaction.id DESC LIMIT 1
          )
    ) THEN
        RAISE EXCEPTION 'cannot roll back migration 108 while a checkpoint uses a migration-108 frontier';
    END IF;
    IF EXISTS(SELECT 1 FROM strategies WHERE execution_strategy_version_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM pipeline_runs WHERE account_id IS NOT NULL OR environment IS NOT NULL OR origin_type IS NOT NULL OR origin_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM pipeline_run_snapshots WHERE account_id IS NOT NULL OR environment IS NOT NULL OR origin_type IS NOT NULL OR origin_id IS NOT NULL OR pipeline_run_trade_date IS NOT NULL)
       OR EXISTS(SELECT 1 FROM agent_decisions WHERE account_id IS NOT NULL OR environment IS NOT NULL OR origin_type IS NOT NULL OR origin_id IS NOT NULL OR pipeline_run_trade_date IS NOT NULL)
       OR EXISTS(SELECT 1 FROM agent_events WHERE account_id IS NOT NULL OR environment IS NOT NULL OR origin_type IS NOT NULL OR origin_id IS NOT NULL OR pipeline_run_trade_date IS NOT NULL)
       OR EXISTS(SELECT 1 FROM trade_decisions WHERE account_id IS NOT NULL OR environment IS NOT NULL OR origin_type IS NOT NULL OR origin_id IS NOT NULL OR pipeline_run_trade_date IS NOT NULL)
       OR EXISTS(SELECT 1 FROM orders WHERE account_id IS NOT NULL OR environment IS NOT NULL OR origin_type IS NOT NULL OR origin_id IS NOT NULL OR pipeline_run_trade_date IS NOT NULL OR copy_origin_rebalance_run_id IS NOT NULL OR allocation_opportunity_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM positions WHERE account_id IS NOT NULL OR environment IS NOT NULL OR origin_type IS NOT NULL OR origin_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM trades WHERE account_id IS NOT NULL OR environment IS NOT NULL OR origin_type IS NOT NULL OR origin_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM portfolio_opportunities WHERE account_id IS NOT NULL OR environment IS NOT NULL OR origin_type IS NOT NULL OR origin_id IS NOT NULL OR pipeline_run_trade_date IS NOT NULL OR allocation_claim_id IS NOT NULL OR allocation_claimed_at IS NOT NULL OR allocation_claim_expires_at IS NOT NULL)
       OR EXISTS(SELECT 1 FROM allocation_decisions WHERE account_id IS NOT NULL OR environment IS NOT NULL OR origin_type IS NOT NULL OR origin_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM replay_events WHERE account_id IS NOT NULL OR environment IS NOT NULL OR origin_type IS NOT NULL OR origin_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM financial_fill_idempotency WHERE account_id IS NOT NULL OR environment IS NOT NULL OR origin_type IS NOT NULL OR origin_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM prediction_settlement_idempotency WHERE account_id IS NOT NULL OR environment IS NOT NULL OR origin_type IS NOT NULL OR origin_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM copy_subscriptions WHERE account_id IS NOT NULL OR environment IS NOT NULL)
       OR EXISTS(SELECT 1 FROM copy_trade_intents WHERE account_id IS NOT NULL OR environment IS NOT NULL OR pipeline_run_trade_date IS NOT NULL OR execution_claim_id IS NOT NULL OR execution_claimed_at IS NOT NULL)
       OR EXISTS(SELECT 1 FROM copy_origin_rebalance_runs WHERE account_id IS NOT NULL OR environment IS NOT NULL)
       OR EXISTS(SELECT 1 FROM copy_origin_rebalance_intents WHERE account_id IS NOT NULL OR environment IS NOT NULL OR origin_type IS NOT NULL OR origin_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM copy_target_drift_runs WHERE account_id IS NOT NULL OR environment IS NOT NULL)
       OR EXISTS(SELECT 1 FROM copy_target_drift_legs WHERE account_id IS NOT NULL OR environment IS NOT NULL OR origin_type IS NOT NULL OR origin_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM execution_intents WHERE copy_origin_rebalance_run_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM execution_orders WHERE copy_origin_rebalance_run_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM conversations WHERE account_id IS NOT NULL OR environment IS NOT NULL OR pipeline_run_trade_date IS NOT NULL)
       OR EXISTS(SELECT 1 FROM conversation_messages WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM agent_memories WHERE account_id IS NOT NULL OR environment IS NOT NULL OR pipeline_run_trade_date IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot roll back migration 108 while expansion columns are populated';
    END IF;

    ALTER TABLE account_capital_policy_bindings DISABLE TRIGGER trg_account_capital_policy_bindings_immutable;
    ALTER TABLE capital_margin_policy_artifacts DISABLE TRIGGER trg_capital_margin_policy_artifacts_immutable;
    DELETE FROM account_capital_policy_bindings WHERE id=seed_binding_id;
    GET DIAGNOSTICS affected=ROW_COUNT;
    IF affected<>1 THEN RAISE EXCEPTION 'failed to delete exact migration-108 binding'; END IF;
    DELETE FROM capital_margin_policy_artifacts WHERE id=seed_artifact_id;
    GET DIAGNOSTICS affected=ROW_COUNT;
    IF affected<>1 THEN RAISE EXCEPTION 'failed to delete exact migration-108 artifact'; END IF;
    ALTER TABLE account_capital_policy_bindings ENABLE TRIGGER trg_account_capital_policy_bindings_immutable;
    ALTER TABLE capital_margin_policy_artifacts ENABLE TRIGGER trg_capital_margin_policy_artifacts_immutable;
END;
$rollback$;

DROP TRIGGER trg_validate_account_projection_outbox_row ON account_projection_outbox;
DROP FUNCTION validate_account_projection_outbox_row();
DROP INDEX idx_account_projection_outbox_claimable;
DROP INDEX uq_account_projection_outbox_request;
DROP TABLE account_projection_outbox;

CREATE OR REPLACE FUNCTION validate_canonical_projection_checkpoint() RETURNS TRIGGER AS $$
DECLARE
    expected_account_currency TEXT;
    expected_transaction_count INTEGER;
    expected_through_transaction_id UUID;
    expected_id UUID;
    expected_as_of_text TEXT;
    decoded_payload JSONB;
    signing_secret BYTEA;
    expected_attestation_hmac BYTEA;
BEGIN
    IF NEW.projection_version IS NULL OR NEW.as_of IS NULL OR NEW.fifo_method IS NULL OR
       NEW.base_currency IS NULL OR NEW.mark_source IS NULL OR NEW.mark_namespace IS NULL OR
       NEW.max_mark_age_microseconds IS NULL OR NEW.transaction_count IS NULL OR
       NEW.mark_count IS NULL OR NEW.lot_count IS NULL OR NEW.match_count IS NULL OR
       NEW.position_count IS NULL OR NEW.input_checksum IS NULL OR NEW.payload_bytes IS NULL OR
       NEW.attestation_key_id IS NULL OR NEW.attestation_hmac IS NULL THEN
        RAISE EXCEPTION 'new projection checkpoints must use the schema-69 canonical shape';
    END IF;
    IF NEW.projection_type <> 'portfolio' OR NEW.projection_version <> 'ledger_fifo_v1' OR
       NEW.fifo_method <> 'fifo' THEN
        RAISE EXCEPTION 'unsupported canonical projection contract';
    END IF;
    IF NEW.mark_source = '' OR NEW.mark_source <> lower(btrim(NEW.mark_source)) OR
       NEW.mark_namespace = '' OR NEW.mark_namespace <> btrim(NEW.mark_namespace) THEN
        RAISE EXCEPTION 'canonical checkpoint mark policy is not normalized';
    END IF;
    IF NEW.max_mark_age_microseconds <= 0 OR NEW.transaction_count <= 0 OR
       NEW.mark_count < 0 OR NEW.lot_count < 0 OR NEW.match_count < 0 OR NEW.position_count < 0 THEN
        RAISE EXCEPTION 'canonical checkpoint counts or mark age are invalid';
    END IF;
    IF NEW.input_checksum !~ '^[0-9a-f]{64}$' OR octet_length(NEW.payload_bytes) = 0 THEN
        RAISE EXCEPTION 'canonical checkpoint input checksum or payload bytes are invalid';
    END IF;
    IF NEW.attestation_key_id !~ '^[a-z0-9][a-z0-9._-]{0,127}$' OR
       octet_length(NEW.attestation_hmac) <> 32 THEN
        RAISE EXCEPTION 'canonical checkpoint attestation shape is invalid';
    END IF;

    SELECT signing_key.signing_secret INTO signing_secret
    FROM projection_checkpoint_signing_keys AS signing_key
    WHERE signing_key.key_id = NEW.attestation_key_id
      AND NOT EXISTS (
          SELECT 1
          FROM projection_checkpoint_signing_key_revocations AS revocation
          WHERE revocation.key_id = signing_key.key_id
      );
    IF signing_secret IS NULL THEN
        RAISE EXCEPTION 'canonical checkpoint attestation key is unknown or revoked';
    END IF;
    expected_attestation_hmac := hmac(
        convert_to('augr-projection-checkpoint-hmac-v1', 'UTF8') ||
        decode('00', 'hex') ||
        convert_to(NEW.attestation_key_id, 'UTF8') ||
        decode('00', 'hex') ||
        NEW.payload_bytes,
        signing_secret,
        'sha256'
    );
    IF NEW.attestation_hmac IS DISTINCT FROM expected_attestation_hmac THEN
        RAISE EXCEPTION 'canonical checkpoint attestation HMAC does not match exact payload bytes';
    END IF;

    SELECT base_currency INTO expected_account_currency
    FROM accounts
    WHERE id = NEW.account_id;
    IF expected_account_currency IS NULL OR NEW.base_currency <> expected_account_currency THEN
        RAISE EXCEPTION 'canonical checkpoint currency must equal account currency';
    END IF;

    BEGIN
        decoded_payload := convert_from(NEW.payload_bytes, 'UTF8')::JSONB;
    EXCEPTION WHEN OTHERS THEN
        RAISE EXCEPTION 'canonical checkpoint payload bytes must contain UTF-8 JSON';
    END;
    IF NEW.payload <> decoded_payload THEN
        RAISE EXCEPTION 'canonical checkpoint JSONB does not match payload bytes';
    END IF;
    IF NEW.checksum <> encode(digest(NEW.payload_bytes, 'sha256'), 'hex') THEN
        RAISE EXCEPTION 'canonical checkpoint checksum does not match payload bytes';
    END IF;

    SELECT COUNT(*)::INTEGER INTO expected_transaction_count
    FROM ledger_transactions
    WHERE account_id = NEW.account_id
      AND effective_at <= NEW.as_of
      AND observed_at <= NEW.as_of;
    IF expected_transaction_count = 0 THEN
        RAISE EXCEPTION 'canonical checkpoint requires at least one eligible ledger transaction';
    END IF;
    SELECT id INTO expected_through_transaction_id
    FROM ledger_transactions
    WHERE account_id = NEW.account_id
      AND effective_at <= NEW.as_of
      AND observed_at <= NEW.as_of
    ORDER BY effective_at DESC, observed_at DESC, id DESC
    LIMIT 1;
    IF NEW.transaction_count <> expected_transaction_count OR
       NEW.through_transaction_id <> expected_through_transaction_id THEN
        RAISE EXCEPTION 'canonical checkpoint ledger boundary does not match its bitemporal snapshot';
    END IF;

    expected_as_of_text := to_char(
        NEW.as_of AT TIME ZONE 'UTC',
        'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'
    );
    expected_id := economic_deterministic_uuid(
        'portfolio-projection-checkpoint',
        NEW.account_id::TEXT,
        NEW.projection_type,
        NEW.projection_version,
        expected_as_of_text,
        NEW.input_checksum
    );
    IF NEW.id <> expected_id THEN
        RAISE EXCEPTION 'canonical checkpoint ID does not match input identity';
    END IF;

    IF NEW.payload->>'checkpoint_id' IS DISTINCT FROM NEW.id::TEXT OR
       NEW.payload->>'projection_type' IS DISTINCT FROM NEW.projection_type OR
       NEW.payload->>'version' IS DISTINCT FROM NEW.projection_version OR
       NEW.payload->>'fifo' IS DISTINCT FROM NEW.fifo_method OR
       NEW.payload->>'account_id' IS DISTINCT FROM NEW.account_id::TEXT OR
       NEW.payload->>'base_currency' IS DISTINCT FROM NEW.base_currency OR
       NEW.payload->>'as_of' IS DISTINCT FROM expected_as_of_text OR
       NEW.payload->>'mark_source' IS DISTINCT FROM NEW.mark_source OR
       NEW.payload->>'mark_namespace' IS DISTINCT FROM NEW.mark_namespace OR
       (NEW.payload->>'max_mark_age_microseconds')::BIGINT IS DISTINCT FROM NEW.max_mark_age_microseconds OR
       NEW.payload->>'through_transaction_id' IS DISTINCT FROM NEW.through_transaction_id::TEXT OR
       (NEW.payload->>'transaction_count')::INTEGER IS DISTINCT FROM NEW.transaction_count OR
       NEW.payload->>'input_checksum' IS DISTINCT FROM NEW.input_checksum THEN
        RAISE EXCEPTION 'canonical checkpoint payload header does not match relational evidence';
    END IF;
    IF jsonb_typeof(NEW.payload->'marks') IS DISTINCT FROM 'array' OR
       jsonb_typeof(NEW.payload->'lots') IS DISTINCT FROM 'array' OR
       jsonb_typeof(NEW.payload->'matches') IS DISTINCT FROM 'array' OR
       jsonb_typeof(NEW.payload->'positions') IS DISTINCT FROM 'array' OR
       jsonb_typeof(NEW.payload->'totals') IS DISTINCT FROM 'object' OR
       jsonb_array_length(NEW.payload->'marks') IS DISTINCT FROM NEW.mark_count OR
       jsonb_array_length(NEW.payload->'lots') IS DISTINCT FROM NEW.lot_count OR
       jsonb_array_length(NEW.payload->'matches') IS DISTINCT FROM NEW.match_count OR
       jsonb_array_length(NEW.payload->'positions') IS DISTINCT FROM NEW.position_count THEN
        RAISE EXCEPTION 'canonical checkpoint payload counts do not match relational evidence';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP INDEX idx_pipeline_runs_account_trade_date;
DROP INDEX idx_pipeline_run_snapshots_account_run;
DROP INDEX idx_agent_decisions_account_run;
DROP INDEX idx_agent_events_account_run;
DROP INDEX idx_trade_decisions_account_created;
DROP INDEX idx_orders_account_created;
DROP INDEX orders_allocation_effect_once;
DROP INDEX orders_copy_origin_effect_once;
DROP INDEX idx_positions_account_opened;
DROP INDEX idx_trades_account_executed;
DROP INDEX idx_portfolio_opportunities_account_created;
DROP INDEX idx_portfolio_opportunities_allocation_claim;
DROP INDEX idx_allocation_decisions_account_created;
DROP INDEX uq_allocation_decisions_opportunity;
DROP INDEX idx_replay_events_account_occurred;
DROP INDEX uq_replay_events_initial;
DROP INDEX idx_financial_fill_idempotency_account;
DROP INDEX idx_prediction_settlement_idempotency_account;
DROP INDEX idx_copy_subscriptions_account_status;
DROP INDEX idx_copy_trade_intents_account_created;
DROP INDEX idx_copy_origin_rebalance_runs_account_created;
DROP INDEX idx_copy_origin_rebalance_intents_account_run;
DROP INDEX idx_copy_target_drift_runs_account_created;
DROP INDEX idx_copy_target_drift_legs_account_run;
DROP INDEX idx_conversations_account_run;
DROP INDEX idx_conversation_messages_account_conversation;
DROP INDEX idx_agent_memories_account_run;
DROP INDEX uq_strategies_paper_event_market_ticker;

ALTER TABLE agent_memories DROP COLUMN pipeline_run_trade_date,DROP COLUMN environment,DROP COLUMN account_id;
ALTER TABLE conversation_messages DROP COLUMN account_id;
ALTER TABLE conversations DROP COLUMN pipeline_run_trade_date,DROP COLUMN environment,DROP COLUMN account_id;
ALTER TABLE execution_orders DROP COLUMN copy_origin_rebalance_run_id;
ALTER TABLE execution_intents DROP COLUMN copy_origin_rebalance_run_id;
ALTER TABLE copy_target_drift_legs DROP COLUMN origin_id,DROP COLUMN origin_type,DROP COLUMN environment,DROP COLUMN account_id;
ALTER TABLE copy_target_drift_runs DROP COLUMN environment,DROP COLUMN account_id;
ALTER TABLE copy_origin_rebalance_intents DROP COLUMN origin_id,DROP COLUMN origin_type,DROP COLUMN environment,DROP COLUMN account_id;
ALTER TABLE copy_origin_rebalance_runs DROP COLUMN environment,DROP COLUMN account_id;
ALTER TABLE copy_trade_intents DROP CONSTRAINT copy_intent_execution_claim_pair,DROP COLUMN execution_claimed_at,DROP COLUMN execution_claim_id,DROP COLUMN pipeline_run_trade_date,DROP COLUMN environment,DROP COLUMN account_id;
ALTER TABLE copy_subscriptions DROP COLUMN environment,DROP COLUMN account_id;
ALTER TABLE prediction_settlement_idempotency DROP COLUMN origin_id,DROP COLUMN origin_type,DROP COLUMN environment,DROP COLUMN account_id;
ALTER TABLE financial_fill_idempotency DROP COLUMN origin_id,DROP COLUMN origin_type,DROP COLUMN environment,DROP COLUMN account_id;
ALTER TABLE replay_events DROP COLUMN origin_id,DROP COLUMN origin_type,DROP COLUMN environment,DROP COLUMN account_id;
ALTER TABLE allocation_decisions DROP COLUMN origin_id,DROP COLUMN origin_type,DROP COLUMN environment,DROP COLUMN account_id;
ALTER TABLE portfolio_opportunities DROP CONSTRAINT portfolio_opportunities_allocation_claim_tuple,DROP COLUMN allocation_claim_expires_at,DROP COLUMN allocation_claimed_at,DROP COLUMN allocation_claim_id,DROP COLUMN pipeline_run_trade_date,DROP COLUMN origin_id,DROP COLUMN origin_type,DROP COLUMN environment,DROP COLUMN account_id;
ALTER TABLE trades DROP COLUMN origin_id,DROP COLUMN origin_type,DROP COLUMN environment,DROP COLUMN account_id;
ALTER TABLE positions DROP COLUMN origin_id,DROP COLUMN origin_type,DROP COLUMN environment,DROP COLUMN account_id;
ALTER TABLE orders DROP COLUMN allocation_opportunity_id,DROP COLUMN copy_origin_rebalance_run_id,DROP COLUMN pipeline_run_trade_date,DROP COLUMN origin_id,DROP COLUMN origin_type,DROP COLUMN environment,DROP COLUMN account_id;
ALTER TABLE trade_decisions DROP COLUMN pipeline_run_trade_date,DROP COLUMN origin_id,DROP COLUMN origin_type,DROP COLUMN environment,DROP COLUMN account_id;
ALTER TABLE agent_events DROP COLUMN pipeline_run_trade_date,DROP COLUMN origin_id,DROP COLUMN origin_type,DROP COLUMN environment,DROP COLUMN account_id;
ALTER TABLE agent_decisions DROP COLUMN pipeline_run_trade_date,DROP COLUMN origin_id,DROP COLUMN origin_type,DROP COLUMN environment,DROP COLUMN account_id;
ALTER TABLE pipeline_run_snapshots DROP COLUMN pipeline_run_trade_date,DROP COLUMN origin_id,DROP COLUMN origin_type,DROP COLUMN environment,DROP COLUMN account_id;
ALTER TABLE pipeline_runs DROP COLUMN origin_id,DROP COLUMN origin_type,DROP COLUMN environment,DROP COLUMN account_id;
ALTER TABLE strategies DROP COLUMN execution_strategy_version_id;

CREATE OR REPLACE FUNCTION strategy_legacy_snapshot_sha(target UUID) RETURNS TEXT AS $$
    SELECT encode(digest(convert_to(to_jsonb(s)::TEXT,'UTF8'),'sha256'),'hex') FROM strategies s WHERE s.id=target;
$$ LANGUAGE sql STABLE STRICT;
