LOCK TABLE pipeline_runs, pipeline_run_snapshots, agent_decisions, agent_events, trade_decisions, orders, positions, trades, portfolio_opportunities, allocation_decisions, replay_events, financial_fill_idempotency, prediction_settlement_idempotency, execution_intents, execution_orders, copy_subscriptions, copy_trade_intents, copy_origin_rebalance_runs, copy_origin_rebalance_intents, copy_target_drift_runs, copy_target_drift_legs, conversations, conversation_messages, agent_memories, account_projection_outbox IN ACCESS EXCLUSIVE MODE;

LOCK TABLE option_settlement_idempotency, option_status_idempotency,
    option_broker_sync_retries IN ACCESS EXCLUSIVE MODE;

DO $rollback$
BEGIN
    IF EXISTS(SELECT 1 FROM pipeline_runs WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM pipeline_run_snapshots WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM agent_decisions WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM agent_events WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM trade_decisions WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM orders WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM positions WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM trades WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM portfolio_opportunities WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM allocation_decisions WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM replay_events WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM financial_fill_idempotency WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM prediction_settlement_idempotency WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM execution_intents)
       OR EXISTS(SELECT 1 FROM execution_orders)
       OR EXISTS(SELECT 1 FROM copy_subscriptions WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM copy_trade_intents WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM copy_origin_rebalance_runs WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM copy_origin_rebalance_intents WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM copy_target_drift_runs WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM copy_target_drift_legs WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM conversations WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM conversation_messages WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM agent_memories WHERE account_id IS NOT NULL)
       OR EXISTS(SELECT 1 FROM account_projection_outbox)
       OR EXISTS(SELECT 1 FROM option_settlement_idempotency)
       OR EXISTS(SELECT 1 FROM option_status_idempotency)
       OR EXISTS(SELECT 1 FROM option_broker_sync_retries) THEN
        RAISE EXCEPTION 'cannot roll back migration 109 while canonical scoped rows exist';
    END IF;
END;
$rollback$;

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
        EXECUTE format('DROP TRIGGER trg_109_canonical_%I ON %I',table_name,table_name);
    END LOOP;
END;
$triggers$;

DROP FUNCTION enforce_canonical_account_row();
DROP FUNCTION canonical_parent_owned(REGCLASS,UUID,UUID);
