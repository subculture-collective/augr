"""Schema-114 read-only queries. No provider bodies, prompts or error strings."""

IDENTITY = """
SELECT json_build_object('database',current_database(), 'schema',version,
 'dirty',dirty,'read_only',current_setting('transaction_read_only')) FROM schema_migrations
"""
COHORT = """
SELECT id,ticker,market_type,schedule_cron,execution_strategy_version_id,skip_next_run
FROM strategies WHERE is_paper AND status='active' ORDER BY id
"""
CONTROLS = "SELECT job_name,enabled,updated_at FROM automation_job_controls ORDER BY job_name"

COUNTERS = (
    'tickers selected positions strategies watchlist updated cache_revalidated '
    'provider_requests provider_attempt_failures provider_recoveries provider_failures '
    'fresh_bars empty stale failed batches universe optionable price_fetch_failed '
    'price_empty price_stale daily_fallback_attempted daily_fallback_accepted '
    'daily_fallback_rejected daily_fallback_provider_pages daily_fallback_cache_hits '
    'chains chain_insufficient setups fetch_failed persist_failed supported swept '
    'candidates generated validated deployed created reused invalid_scores '
    'completed skipped running total missing coverage_bps all_unqualified base_unqualified '
    'config_failed sweep_failed insufficient empty_results missing_base '
    'optionable_coverage_bps chain_coverage_bps'
).split()
COUNTER_SQL = """COALESCE((SELECT jsonb_object_agg(key,value) FROM jsonb_each(
 CASE WHEN jsonb_typeof(result)='object' THEN result ELSE '{}'::jsonb END)
 WHERE key IN (%s) AND jsonb_typeof(value)='number'), '{}'::jsonb) AS counters""" % ','.join("'"+k+"'" for k in COUNTERS)
RUN_COLUMNS = "id,job_name,status,started_at,completed_at,consecutive_failures,(error IS NOT NULL AND error <> '') AS error_present," + COUNTER_SQL
PIPELINE_COLUMNS = "id,strategy_id,execution_version_id,status,signal,started_at,completed_at,environment, (error_message IS NOT NULL AND error_message <> '') AS error_present"


def sections(since):
    # since is parsed and regenerated as ISO-8601 before reaching this function.
    ts = "TIMESTAMPTZ '" + since + "'"
    return {
        'controls': CONTROLS,
        'automation': f"SELECT {RUN_COLUMNS} FROM automation_job_runs WHERE started_at >= {ts} ORDER BY started_at DESC LIMIT 501",
        'latest_jobs': f"SELECT DISTINCT ON (job_name) {RUN_COLUMNS} FROM automation_job_runs ORDER BY job_name,started_at DESC",
        'pipelines': f"SELECT {PIPELINE_COLUMNS} FROM pipeline_runs WHERE started_at >= {ts} ORDER BY started_at DESC LIMIT 501",
        'queues': """SELECT (SELECT count(*) FROM automation_job_runs WHERE completed_at IS NULL) AS automation_running,
          (SELECT min(started_at) FROM automation_job_runs WHERE completed_at IS NULL) AS oldest_automation,
          (SELECT count(*) FROM pipeline_runs WHERE completed_at IS NULL) AS pipelines_running,
          (SELECT min(started_at) FROM pipeline_runs WHERE completed_at IS NULL) AS oldest_pipeline""",
        'financial_counts': """SELECT (SELECT count(*) FROM orders) AS orders,
          (SELECT count(*) FROM trades) AS trades,(SELECT count(*) FROM positions) AS positions,
          (SELECT count(*) FROM positions WHERE closed_at IS NULL) AS open_positions""",
        'decisions': f"""SELECT td.id,td.strategy_id,td.pipeline_run_id,td.status,td.risk_status,td.paper_order_id,
          td.live_order_id,td.created_at,td.llm_provider,td.llm_model,td.latency_ms,td.prompt_tokens,td.completion_tokens,td.cost_usd,
          (td.evidence='{{}}'::jsonb) AS missing_evidence,
          (SELECT count(*) FROM replay_events r WHERE r.trade_decision_id=td.id) AS replay_events,
          (SELECT count(*) FROM orders o WHERE o.id=td.paper_order_id) AS linked_orders
          FROM trade_decisions td WHERE td.created_at >= {ts} ORDER BY td.created_at DESC LIMIT 501""",
        'orders': f"SELECT id,strategy_id,status,quantity,filled_quantity,created_at FROM orders WHERE created_at >= {ts} ORDER BY created_at DESC LIMIT 501",
        'trades': f"SELECT id,order_id,position_id,quantity,price,fee,executed_at FROM trades WHERE executed_at >= {ts} ORDER BY executed_at DESC LIMIT 501",
        'positions': "SELECT id,strategy_id,asset_class,quantity,opened_at,closed_at,realized_pnl,unrealized_pnl FROM positions WHERE closed_at IS NULL ORDER BY id LIMIT 501",
        'reports': f"SELECT id,strategy_id,status,provider,model,prompt_tokens,completion_tokens,latency_ms,created_at,completed_at FROM report_artifacts WHERE created_at >= {ts} ORDER BY created_at DESC LIMIT 501",
        'coverage': f"SELECT ticker,provider,timeframe,range_from,range_to,fetched_at FROM historical_ohlcv_coverage WHERE fetched_at >= {ts} ORDER BY fetched_at DESC LIMIT 501",
        'manual_strategy_runs': f"SELECT entity_id,created_at FROM audit_log WHERE event_type='strategy.manual_run' AND created_at >= {ts} ORDER BY created_at LIMIT 501",
        'preparation_rejections': f"SELECT {PREPARATION_COLUMNS} FROM agent_events WHERE event_kind='strategy.preparation_rejected' AND created_at >= {ts} ORDER BY created_at DESC,id DESC LIMIT 501",
    }


# Keep durable rejection reasons, never arbitrary event metadata or summaries.
PREPARATION_COLUMNS = """id,strategy_id,origin_id AS execution_version_id,created_at,
 CASE WHEN metadata->>'reason_code' IN ('news_coverage_insufficient','news_stale',
 'fundamentals_incomplete','fundamentals_invalid','market_data_stale',
 'market_data_unavailable','social_data_invalid','llm_provider_unavailable','preparation_failed')
 THEN metadata->>'reason_code' ELSE 'unclassified' END AS reason_code"""


def preparation_rejections(target, since, until):
    return f"""SELECT {PREPARATION_COLUMNS} FROM agent_events
 WHERE event_kind='strategy.preparation_rejected' AND strategy_id='{target}'::uuid
 AND created_at >= TIMESTAMPTZ '{since}' AND created_at <= TIMESTAMPTZ '{until}'
 ORDER BY created_at,id LIMIT 3"""


def observed_runs(kind, target, since, until):
    time_filter = f"started_at >= TIMESTAMPTZ '{since}' AND started_at < TIMESTAMPTZ '{until}'"
    if kind == 'strategy':
        return f"SELECT {PIPELINE_COLUMNS} FROM pipeline_runs WHERE strategy_id='{target}'::uuid AND {time_filter} ORDER BY started_at LIMIT 3"
    return f"SELECT {RUN_COLUMNS} FROM automation_job_runs WHERE job_name='{target}' AND {time_filter} ORDER BY started_at LIMIT 3"
