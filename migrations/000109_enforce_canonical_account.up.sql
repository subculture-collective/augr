-- Enforce the canonical account graph after the schema-108 compatibility
-- window. Existing legacy NULL rows remain readable, but every new or updated
-- operational row must be fully scoped and owned by one active account.
LOCK TABLE pipeline_runs, pipeline_run_snapshots, agent_decisions, agent_events,
    trade_decisions, orders, positions, trades, portfolio_opportunities,
    allocation_decisions, replay_events, financial_fill_idempotency,
    prediction_settlement_idempotency, execution_intents, execution_orders,
    copy_subscriptions, copy_trade_intents, copy_origin_rebalance_runs,
    copy_origin_rebalance_intents, copy_target_drift_runs, copy_target_drift_legs,
    conversations, conversation_messages, agent_memories, account_projection_outbox,
    option_settlement_idempotency, option_status_idempotency,
    option_broker_sync_retries IN ACCESS EXCLUSIVE MODE;

CREATE FUNCTION canonical_parent_owned(parent_table REGCLASS, parent_id UUID, owner_account_id UUID)
RETURNS BOOLEAN AS $$
DECLARE owned BOOLEAN;
BEGIN
    IF parent_id IS NULL THEN
        RETURN TRUE;
    END IF;
    EXECUTE format('SELECT EXISTS (SELECT 1 FROM %s WHERE id=$1 AND account_id=$2)', parent_table)
       INTO owned USING parent_id, owner_account_id;
    RETURN owned;
END;
$$ LANGUAGE plpgsql STABLE;

CREATE FUNCTION enforce_canonical_account_row() RETURNS TRIGGER AS $$
DECLARE
    row_data JSONB := to_jsonb(NEW);
    old_data JSONB;
    scoped_account_id UUID;
    scoped_environment TEXT;
    scoped_origin_type TEXT;
    scoped_origin_id TEXT;
    parent_intent RECORD;
BEGIN
    scoped_account_id := NULLIF(row_data->>'account_id','')::UUID;
    IF scoped_account_id IS NULL THEN
        RAISE EXCEPTION 'canonical operational row requires account_id';
    END IF;
    IF TG_OP='UPDATE' THEN
        old_data := to_jsonb(OLD);
        IF NULLIF(old_data->>'account_id','')::UUID IS DISTINCT FROM scoped_account_id THEN
            RAISE EXCEPTION 'canonical operational account_id is immutable';
        END IF;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM accounts WHERE id=scoped_account_id AND status='active') THEN
        RAISE EXCEPTION 'canonical operational row requires an active account';
    END IF;

    IF row_data ? 'environment' THEN
        scoped_environment := row_data->>'environment';
        IF scoped_environment IS NULL OR NOT EXISTS (
            SELECT 1 FROM accounts WHERE id=scoped_account_id AND environment=scoped_environment
        ) THEN
            RAISE EXCEPTION 'canonical operational environment does not match account';
        END IF;
    END IF;
    IF row_data ? 'origin_type' THEN
        scoped_origin_type := row_data->>'origin_type';
        scoped_origin_id := row_data->>'origin_id';
        IF scoped_origin_type IS NULL OR btrim(scoped_origin_type)='' OR
           scoped_origin_id IS NULL OR btrim(scoped_origin_id)='' THEN
            RAISE EXCEPTION 'canonical operational origin is required';
        END IF;
    END IF;

    -- Partitioned pipeline identity is always the exact composite key.
    IF row_data ? 'pipeline_run_id' AND row_data->>'pipeline_run_id' IS NOT NULL THEN
        IF NOT (row_data ? 'pipeline_run_trade_date') OR row_data->>'pipeline_run_trade_date' IS NULL OR NOT EXISTS (
            SELECT 1 FROM pipeline_runs AS run
            WHERE run.id=(row_data->>'pipeline_run_id')::UUID
              AND run.trade_date=(row_data->>'pipeline_run_trade_date')::DATE
              AND run.account_id=scoped_account_id
        ) THEN
            RAISE EXCEPTION 'scoped row does not match pipeline run identity';
        END IF;
    END IF;

    CASE TG_TABLE_NAME
    WHEN 'trade_decisions' THEN
        IF NOT canonical_parent_owned('orders'::REGCLASS,NULLIF(row_data->>'paper_order_id','')::UUID,scoped_account_id) OR
           NOT canonical_parent_owned('orders'::REGCLASS,NULLIF(row_data->>'live_order_id','')::UUID,scoped_account_id) THEN
            RAISE EXCEPTION 'trade decision order belongs to another account';
        END IF;
    WHEN 'orders' THEN
        IF NOT canonical_parent_owned('copy_trade_intents'::REGCLASS,NULLIF(row_data->>'copy_intent_id','')::UUID,scoped_account_id) OR
           NOT canonical_parent_owned('portfolio_opportunities'::REGCLASS,NULLIF(row_data->>'allocation_opportunity_id','')::UUID,scoped_account_id) THEN
            RAISE EXCEPTION 'order parent belongs to another account';
        END IF;
        IF scoped_origin_type='copy_subscription' THEN
            IF row_data->>'copy_origin_rebalance_run_id' IS NULL OR NOT EXISTS (
                SELECT 1 FROM copy_origin_rebalance_runs AS run
                WHERE run.id=(row_data->>'copy_origin_rebalance_run_id')::UUID
                  AND run.account_id=scoped_account_id
                  AND scoped_origin_id=run.subscription_id::TEXT
            ) THEN
                RAISE EXCEPTION 'copy order does not match its account-owned origin run';
            END IF;
        ELSIF row_data->>'copy_origin_rebalance_run_id' IS NOT NULL THEN
            RAISE EXCEPTION 'non-copy order cannot carry a copy origin run';
        END IF;
    WHEN 'positions' THEN
        IF NOT canonical_parent_owned('orders'::REGCLASS,NULLIF(row_data->>'close_reservation_order_id','')::UUID,scoped_account_id) THEN
            RAISE EXCEPTION 'position reservation order belongs to another account';
        END IF;
    WHEN 'trades' THEN
        IF NOT canonical_parent_owned('orders'::REGCLASS,NULLIF(row_data->>'order_id','')::UUID,scoped_account_id) OR
           NOT canonical_parent_owned('positions'::REGCLASS,NULLIF(row_data->>'position_id','')::UUID,scoped_account_id) THEN
            RAISE EXCEPTION 'trade parent belongs to another account';
        END IF;
    WHEN 'allocation_decisions' THEN
        IF NOT canonical_parent_owned('portfolio_opportunities'::REGCLASS,NULLIF(row_data->>'opportunity_id','')::UUID,scoped_account_id) OR
           NOT canonical_parent_owned('orders'::REGCLASS,NULLIF(row_data->>'created_order_id','')::UUID,scoped_account_id) THEN
            RAISE EXCEPTION 'allocation parent belongs to another account';
        END IF;
    WHEN 'replay_events' THEN
        IF NOT canonical_parent_owned('trade_decisions'::REGCLASS,(row_data->>'trade_decision_id')::UUID,scoped_account_id) THEN
            RAISE EXCEPTION 'replay decision belongs to another account';
        END IF;
    WHEN 'financial_fill_idempotency' THEN
        IF NOT canonical_parent_owned('orders'::REGCLASS,(row_data->>'order_id')::UUID,scoped_account_id) OR
           NOT canonical_parent_owned('positions'::REGCLASS,NULLIF(row_data->>'position_id','')::UUID,scoped_account_id) OR
           NOT canonical_parent_owned('trades'::REGCLASS,(row_data->>'trade_id')::UUID,scoped_account_id) THEN
            RAISE EXCEPTION 'fill idempotency parent belongs to another account';
        END IF;
    WHEN 'prediction_settlement_idempotency' THEN
        IF NOT canonical_parent_owned('trade_decisions'::REGCLASS,(row_data->>'decision_id')::UUID,scoped_account_id) OR
           NOT canonical_parent_owned('positions'::REGCLASS,NULLIF(row_data->>'position_id','')::UUID,scoped_account_id) OR
           NOT canonical_parent_owned('trades'::REGCLASS,(row_data->>'trade_id')::UUID,scoped_account_id) OR
           NOT canonical_parent_owned('replay_events'::REGCLASS,NULLIF(row_data->>'replay_event_id','')::UUID,scoped_account_id) THEN
            RAISE EXCEPTION 'prediction settlement parent belongs to another account';
        END IF;
    WHEN 'option_settlement_idempotency' THEN
        IF NOT canonical_parent_owned('positions'::REGCLASS,(row_data->>'position_id')::UUID,scoped_account_id) OR
           NOT canonical_parent_owned('trades'::REGCLASS,(row_data->>'trade_id')::UUID,scoped_account_id) THEN
            RAISE EXCEPTION 'option settlement parent belongs to another account';
        END IF;
    WHEN 'option_status_idempotency' THEN
        IF NOT canonical_parent_owned('orders'::REGCLASS,(row_data->>'order_id')::UUID,scoped_account_id) THEN
            RAISE EXCEPTION 'option status order belongs to another account';
        END IF;
    WHEN 'option_broker_sync_retries' THEN
        IF NOT canonical_parent_owned('positions'::REGCLASS,(row_data->>'position_id')::UUID,scoped_account_id) THEN
            RAISE EXCEPTION 'option retry position belongs to another account';
        END IF;
    WHEN 'copy_trade_intents' THEN
        IF NOT canonical_parent_owned('copy_subscriptions'::REGCLASS,(row_data->>'subscription_id')::UUID,scoped_account_id) THEN
            RAISE EXCEPTION 'copy intent subscription belongs to another account';
        END IF;
    WHEN 'copy_origin_rebalance_runs' THEN
        IF NOT canonical_parent_owned('copy_subscriptions'::REGCLASS,(row_data->>'subscription_id')::UUID,scoped_account_id) THEN
            RAISE EXCEPTION 'copy origin run subscription belongs to another account';
        END IF;
    WHEN 'copy_origin_rebalance_intents' THEN
        IF NOT canonical_parent_owned('copy_origin_rebalance_runs'::REGCLASS,(row_data->>'run_id')::UUID,scoped_account_id) OR
           NOT canonical_parent_owned('copy_trade_intents'::REGCLASS,(row_data->>'intent_id')::UUID,scoped_account_id) THEN
            RAISE EXCEPTION 'copy origin intent parent belongs to another account';
        END IF;
    WHEN 'copy_target_drift_runs' THEN
        IF NOT canonical_parent_owned('copy_subscriptions'::REGCLASS,(row_data->>'subscription_id')::UUID,scoped_account_id) THEN
            RAISE EXCEPTION 'copy drift run subscription belongs to another account';
        END IF;
    WHEN 'copy_target_drift_legs' THEN
        IF NOT canonical_parent_owned('copy_target_drift_runs'::REGCLASS,(row_data->>'run_id')::UUID,scoped_account_id) THEN
            RAISE EXCEPTION 'copy drift leg run belongs to another account';
        END IF;
    WHEN 'conversation_messages' THEN
        IF NOT canonical_parent_owned('conversations'::REGCLASS,(row_data->>'conversation_id')::UUID,scoped_account_id) THEN
            RAISE EXCEPTION 'conversation message parent belongs to another account';
        END IF;
    WHEN 'account_projection_outbox' THEN
        IF NOT canonical_parent_owned('ledger_transactions'::REGCLASS,(row_data->>'through_transaction_id')::UUID,scoped_account_id) THEN
            RAISE EXCEPTION 'projection frontier belongs to another account';
        END IF;
    WHEN 'execution_intents' THEN
        IF scoped_origin_type='copy_subscription' THEN
            IF row_data->>'copy_origin_rebalance_run_id' IS NULL OR NOT EXISTS (
                SELECT 1 FROM copy_origin_rebalance_runs AS run
                WHERE run.id=(row_data->>'copy_origin_rebalance_run_id')::UUID
                  AND run.account_id=scoped_account_id
                  AND scoped_origin_id=run.subscription_id::TEXT
            ) THEN
                RAISE EXCEPTION 'copy execution intent does not match its account-owned origin run';
            END IF;
        ELSIF row_data->>'copy_origin_rebalance_run_id' IS NOT NULL THEN
            RAISE EXCEPTION 'non-copy execution intent cannot carry a copy origin run';
        END IF;
    WHEN 'execution_orders' THEN
        SELECT account_id,copy_origin_rebalance_run_id INTO parent_intent
        FROM execution_intents WHERE id=(row_data->>'intent_id')::UUID FOR UPDATE;
        IF parent_intent.account_id IS NULL OR parent_intent.account_id<>scoped_account_id OR
           NULLIF(row_data->>'copy_origin_rebalance_run_id','')::UUID IS DISTINCT FROM parent_intent.copy_origin_rebalance_run_id THEN
            RAISE EXCEPTION 'execution order does not match parent intent account and copy origin';
        END IF;
    ELSE
        NULL;
    END CASE;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DO $triggers$
DECLARE table_name TEXT;
BEGIN
    FOREACH table_name IN ARRAY ARRAY[
        'pipeline_runs','pipeline_run_snapshots','agent_decisions','agent_events',
        'trade_decisions','orders','positions','trades','portfolio_opportunities',
        'allocation_decisions','replay_events','financial_fill_idempotency',
        'prediction_settlement_idempotency','execution_intents','execution_orders',
        'copy_subscriptions','copy_trade_intents','copy_origin_rebalance_runs',
        'copy_origin_rebalance_intents','copy_target_drift_runs','copy_target_drift_legs',
        'conversations','conversation_messages','agent_memories','account_projection_outbox',
        'option_settlement_idempotency','option_status_idempotency','option_broker_sync_retries'
    ] LOOP
        EXECUTE format('CREATE TRIGGER trg_109_canonical_%I BEFORE INSERT OR UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION enforce_canonical_account_row()',table_name,table_name);
    END LOOP;
END;
$triggers$;

DO $projection_privileges$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='augr_app_runtime') THEN
        GRANT SELECT,INSERT,UPDATE ON account_projection_outbox TO augr_app_runtime;
        GRANT INSERT ON mark_observations TO augr_app_runtime;
        REVOKE DELETE,TRUNCATE ON account_projection_outbox FROM augr_app_runtime;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='augr_projection_writer') THEN
        GRANT SELECT ON accounts,ledger_transactions,ledger_postings,economic_event_normalizations,
            venue_contracts,option_contract_terms,instruments,mark_observations,projection_checkpoints
            TO augr_projection_writer;
        GRANT EXECUTE ON FUNCTION persist_canonical_projection_checkpoint(BYTEA,TEXT,BYTEA)
            TO augr_projection_writer;
        REVOKE INSERT ON mark_observations FROM augr_projection_writer;
        REVOKE ALL PRIVILEGES ON account_projection_outbox FROM augr_projection_writer;
    END IF;
END;
$projection_privileges$;
