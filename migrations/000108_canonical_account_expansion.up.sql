-- Nullable account expansion. This bridge changes no existing row and installs
-- no account enforcement for legacy writers.
LOCK TABLE pipeline_runs, pipeline_run_snapshots, agent_decisions, agent_events,
    trade_decisions, orders, positions, trades, portfolio_opportunities,
    allocation_decisions, replay_events, financial_fill_idempotency,
    prediction_settlement_idempotency, execution_intents, execution_orders,
    copy_subscriptions, copy_trade_intents, copy_origin_rebalance_runs,
    copy_origin_rebalance_intents, copy_target_drift_runs, copy_target_drift_legs,
    strategies, projection_checkpoints, account_capital_policy_bindings,
    capital_margin_policy_artifacts, conversations, conversation_messages,
    agent_memories IN SHARE ROW EXCLUSIVE MODE;

CREATE OR REPLACE FUNCTION strategy_legacy_snapshot_sha(target UUID) RETURNS TEXT AS $$
    SELECT encode(digest(convert_to(jsonb_build_object(
        'id', s.id,
        'name', s.name,
        'description', s.description,
        'ticker', s.ticker,
        'market_type', s.market_type,
        'schedule_cron', s.schedule_cron,
        'config', s.config,
        'is_active', s.is_active,
        'is_paper', s.is_paper,
        'created_at', s.created_at,
        'updated_at', s.updated_at,
        'status', s.status,
        'skip_next_run', s.skip_next_run,
        'active_thesis', s.active_thesis
    )::TEXT, 'UTF8'), 'sha256'), 'hex')
    FROM strategies AS s WHERE s.id = target;
$$ LANGUAGE sql STABLE STRICT;

ALTER TABLE strategies
    ADD COLUMN execution_strategy_version_id UUID REFERENCES strategy_versions(id) ON DELETE RESTRICT;

CREATE UNIQUE INDEX uq_strategies_paper_event_market_ticker
    ON strategies(ticker,market_type)
    WHERE is_paper=true AND market_type IN ('kalshi','polymarket')
      AND execution_strategy_version_id IS NOT NULL;

ALTER TABLE pipeline_runs
    ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
    ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live')),
    ADD COLUMN origin_type TEXT CHECK (origin_type IN ('strategy_version','copy_subscription','portfolio_rebalance','risk_reduction','operator','settlement','reconciliation')),
    ADD COLUMN origin_id TEXT;
ALTER TABLE pipeline_run_snapshots
    ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
    ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live')),
    ADD COLUMN origin_type TEXT CHECK (origin_type IN ('strategy_version','copy_subscription','portfolio_rebalance','risk_reduction','operator','settlement','reconciliation')),
    ADD COLUMN origin_id TEXT,
    ADD COLUMN pipeline_run_trade_date DATE;
ALTER TABLE agent_decisions
    ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
    ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live')),
    ADD COLUMN origin_type TEXT CHECK (origin_type IN ('strategy_version','copy_subscription','portfolio_rebalance','risk_reduction','operator','settlement','reconciliation')),
    ADD COLUMN origin_id TEXT,
    ADD COLUMN pipeline_run_trade_date DATE;
ALTER TABLE agent_events
    ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
    ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live')),
    ADD COLUMN origin_type TEXT CHECK (origin_type IN ('strategy_version','copy_subscription','portfolio_rebalance','risk_reduction','operator','settlement','reconciliation')),
    ADD COLUMN origin_id TEXT,
    ADD COLUMN pipeline_run_trade_date DATE;
ALTER TABLE trade_decisions
    ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
    ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live')),
    ADD COLUMN origin_type TEXT CHECK (origin_type IN ('strategy_version','copy_subscription','portfolio_rebalance','risk_reduction','operator','settlement','reconciliation')),
    ADD COLUMN origin_id TEXT,
    ADD COLUMN pipeline_run_trade_date DATE;
ALTER TABLE orders
    ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
    ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live')),
    ADD COLUMN origin_type TEXT CHECK (origin_type IN ('strategy_version','copy_subscription','portfolio_rebalance','risk_reduction','operator','settlement','reconciliation')),
    ADD COLUMN origin_id TEXT,
    ADD COLUMN pipeline_run_trade_date DATE,
    ADD COLUMN copy_origin_rebalance_run_id UUID REFERENCES copy_origin_rebalance_runs(id) ON DELETE RESTRICT,
    ADD COLUMN copy_intent_id UUID REFERENCES copy_trade_intents(id) ON DELETE RESTRICT,
    ADD COLUMN copy_execution_claim_id UUID,
    ADD COLUMN allocation_opportunity_id UUID REFERENCES portfolio_opportunities(id) ON DELETE RESTRICT,
    ADD COLUMN client_order_id TEXT,
    ADD COLUMN spread_max_risk NUMERIC(20,8),
    ADD COLUMN spread_max_reward NUMERIC(20,8),
    ADD CONSTRAINT orders_copy_execution_claim_pair CHECK ((copy_intent_id IS NULL) = (copy_execution_claim_id IS NULL));
ALTER TABLE positions
    ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
    ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live')),
    ADD COLUMN origin_type TEXT CHECK (origin_type IN ('strategy_version','copy_subscription','portfolio_rebalance','risk_reduction','operator','settlement','reconciliation')),
    ADD COLUMN origin_id TEXT,
    ADD COLUMN close_reservation_order_id UUID REFERENCES orders(id) ON DELETE RESTRICT;

CREATE INDEX idx_positions_close_reservation_order ON positions(close_reservation_order_id) WHERE close_reservation_order_id IS NOT NULL;
ALTER TABLE trades
    ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
    ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live')),
    ADD COLUMN origin_type TEXT CHECK (origin_type IN ('strategy_version','copy_subscription','portfolio_rebalance','risk_reduction','operator','settlement','reconciliation')),
    ADD COLUMN origin_id TEXT;
ALTER TABLE portfolio_opportunities
    ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
    ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live')),
    ADD COLUMN origin_type TEXT CHECK (origin_type IN ('strategy_version','copy_subscription','portfolio_rebalance','risk_reduction','operator','settlement','reconciliation')),
    ADD COLUMN origin_id TEXT,
    ADD COLUMN pipeline_run_trade_date DATE,
    ADD COLUMN allocation_claim_id UUID,
    ADD COLUMN allocation_claimed_at TIMESTAMPTZ,
    ADD COLUMN allocation_claim_expires_at TIMESTAMPTZ,
    ADD CONSTRAINT portfolio_opportunities_allocation_claim_tuple CHECK (
        (allocation_claim_id IS NULL AND allocation_claimed_at IS NULL AND allocation_claim_expires_at IS NULL)
        OR (allocation_claim_id IS NOT NULL AND allocation_claimed_at IS NOT NULL AND allocation_claim_expires_at IS NOT NULL AND allocation_claim_expires_at > allocation_claimed_at)
    );
ALTER TABLE allocation_decisions
    ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
    ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live')),
    ADD COLUMN origin_type TEXT CHECK (origin_type IN ('strategy_version','copy_subscription','portfolio_rebalance','risk_reduction','operator','settlement','reconciliation')),
    ADD COLUMN origin_id TEXT,
    ADD COLUMN pipeline_run_id UUID,
    ADD COLUMN pipeline_run_trade_date DATE;
ALTER TABLE replay_events
    ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
    ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live')),
    ADD COLUMN origin_type TEXT CHECK (origin_type IN ('strategy_version','copy_subscription','portfolio_rebalance','risk_reduction','operator','settlement','reconciliation')),
    ADD COLUMN origin_id TEXT;
ALTER TABLE financial_fill_idempotency
    ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
    ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live')),
    ADD COLUMN origin_type TEXT CHECK (origin_type IN ('strategy_version','copy_subscription','portfolio_rebalance','risk_reduction','operator','settlement','reconciliation')),
    ADD COLUMN origin_id TEXT,
    ADD COLUMN cumulative_fee NUMERIC(20,8),
    ADD COLUMN cumulative_premium NUMERIC(20,8),
    ADD COLUMN cumulative_filled_at TIMESTAMPTZ,
    ADD COLUMN cumulative_status TEXT CHECK (cumulative_status IN ('partial','filled','cancelled','rejected')),
    ADD COLUMN cumulative_exit_reason TEXT;
ALTER TABLE prediction_settlement_idempotency
    ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
    ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live')),
    ADD COLUMN origin_type TEXT CHECK (origin_type IN ('strategy_version','copy_subscription','portfolio_rebalance','risk_reduction','operator','settlement','reconciliation')),
    ADD COLUMN origin_id TEXT;

CREATE TABLE option_settlement_idempotency (
    idempotency_key TEXT PRIMARY KEY,
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    environment TEXT NOT NULL CHECK (environment IN ('paper_scored','paper_stress','shadow','live')),
    origin_type TEXT NOT NULL CHECK (origin_type IN ('strategy_version','copy_subscription','portfolio_rebalance','risk_reduction','operator','settlement','reconciliation')),
    origin_id TEXT NOT NULL,
    position_id UUID NOT NULL UNIQUE REFERENCES positions(id) ON DELETE RESTRICT,
    trade_id UUID NOT NULL UNIQUE REFERENCES trades(id) ON DELETE RESTRICT,
    settlement_price NUMERIC(20,8) NOT NULL CHECK (settlement_price>=0),
    settled_at TIMESTAMPTZ NOT NULL,
    exit_reason TEXT NOT NULL CHECK (exit_reason IN ('expired_worthless','exercise_cash_settled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE option_status_idempotency (
    idempotency_key TEXT PRIMARY KEY,
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    environment TEXT NOT NULL CHECK (environment IN ('paper_scored','paper_stress','shadow','live')),
    origin_type TEXT NOT NULL,
    origin_id TEXT NOT NULL,
    order_id UUID NOT NULL UNIQUE REFERENCES orders(id) ON DELETE RESTRICT,
    status TEXT NOT NULL CHECK (status IN ('filled','cancelled','rejected')),
    filled_quantity NUMERIC(20,8) NOT NULL CHECK (filled_quantity>=0),
    external_id TEXT,
    submitted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE option_broker_sync_retries (
    position_id UUID PRIMARY KEY REFERENCES positions(id) ON DELETE RESTRICT,
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    environment TEXT NOT NULL CHECK (environment IN ('paper_scored','paper_stress','shadow','live')),
    origin_type TEXT NOT NULL,
    origin_id TEXT NOT NULL,
    settlement_price NUMERIC(20,8) NOT NULL CHECK (settlement_price>=0),
    settled_at TIMESTAMPTZ NOT NULL,
    last_error TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'retry' CHECK (status IN ('retry','resolved')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE conversations
    ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
    ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live')),
    ADD COLUMN pipeline_run_trade_date DATE;
ALTER TABLE conversation_messages
    ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT;
ALTER TABLE agent_memories
    ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
    ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live')),
    ADD COLUMN pipeline_run_trade_date DATE;

ALTER TABLE copy_subscriptions
    ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
    ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live'));
ALTER TABLE copy_trade_intents
    ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
    ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live')),
    ADD COLUMN pipeline_run_trade_date DATE,
    ADD COLUMN execution_claim_id UUID,
    ADD COLUMN execution_claimed_at TIMESTAMPTZ,
    ADD CONSTRAINT copy_intent_execution_claim_pair CHECK ((execution_claim_id IS NULL) = (execution_claimed_at IS NULL));
ALTER TABLE copy_origin_rebalance_runs
    ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
    ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live'));
ALTER TABLE copy_origin_rebalance_intents
    ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
    ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live')),
    ADD COLUMN origin_type TEXT CHECK (origin_type IN ('strategy_version','copy_subscription','portfolio_rebalance','risk_reduction','operator','settlement','reconciliation')),
    ADD COLUMN origin_id TEXT;
ALTER TABLE copy_target_drift_runs
    ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
    ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live'));
ALTER TABLE copy_target_drift_legs
    ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
    ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live')),
    ADD COLUMN origin_type TEXT CHECK (origin_type IN ('strategy_version','copy_subscription','portfolio_rebalance','risk_reduction','operator','settlement','reconciliation')),
    ADD COLUMN origin_id TEXT;

ALTER TABLE execution_intents
    ADD COLUMN copy_origin_rebalance_run_id UUID REFERENCES copy_origin_rebalance_runs(id) ON DELETE RESTRICT;
ALTER TABLE execution_orders
    ADD COLUMN copy_origin_rebalance_run_id UUID REFERENCES copy_origin_rebalance_runs(id) ON DELETE RESTRICT;

CREATE INDEX idx_pipeline_runs_account_trade_date ON pipeline_runs(account_id,trade_date,id) WHERE account_id IS NOT NULL;
CREATE INDEX idx_pipeline_run_snapshots_account_run ON pipeline_run_snapshots(account_id,pipeline_run_trade_date,pipeline_run_id,created_at,id) WHERE account_id IS NOT NULL;
CREATE INDEX idx_agent_decisions_account_run ON agent_decisions(account_id,pipeline_run_trade_date,pipeline_run_id,created_at,id) WHERE account_id IS NOT NULL;
CREATE INDEX idx_agent_events_account_run ON agent_events(account_id,pipeline_run_trade_date,pipeline_run_id,created_at,id) WHERE account_id IS NOT NULL;
CREATE INDEX idx_trade_decisions_account_created ON trade_decisions(account_id,created_at,id) WHERE account_id IS NOT NULL;
CREATE INDEX idx_orders_account_created ON orders(account_id,created_at,id) WHERE account_id IS NOT NULL;
CREATE UNIQUE INDEX orders_allocation_effect_once ON orders(account_id,allocation_opportunity_id) WHERE allocation_opportunity_id IS NOT NULL;
CREATE UNIQUE INDEX orders_copy_origin_effect_once ON orders(account_id,environment,origin_id,copy_origin_rebalance_run_id,ticker,side) WHERE origin_type='copy_subscription' AND copy_origin_rebalance_run_id IS NOT NULL;
CREATE INDEX idx_orders_copy_intent ON orders(copy_intent_id) WHERE copy_intent_id IS NOT NULL;
CREATE INDEX idx_positions_account_opened ON positions(account_id,opened_at,id) WHERE account_id IS NOT NULL;
CREATE INDEX idx_trades_account_executed ON trades(account_id,executed_at,id) WHERE account_id IS NOT NULL;
CREATE INDEX idx_portfolio_opportunities_account_created ON portfolio_opportunities(account_id,created_at,id) WHERE account_id IS NOT NULL;
CREATE INDEX idx_portfolio_opportunities_allocation_claim ON portfolio_opportunities(account_id,status,allocation_claim_expires_at,id) WHERE status='selected';
CREATE UNIQUE INDEX uq_portfolio_opportunities_execution_dedupe ON portfolio_opportunities(account_id,environment,origin_type,origin_id,pipeline_run_id,pipeline_run_trade_date,strategy_id,dedupe_key);
CREATE INDEX idx_allocation_decisions_account_created ON allocation_decisions(account_id,created_at,id) WHERE account_id IS NOT NULL;
CREATE UNIQUE INDEX uq_allocation_decisions_opportunity ON allocation_decisions(opportunity_id)
    WHERE opportunity_id IS NOT NULL AND account_id IS NOT NULL AND environment IS NOT NULL
      AND origin_type IS NOT NULL AND origin_id IS NOT NULL AND pipeline_run_id IS NOT NULL
      AND pipeline_run_trade_date IS NOT NULL;
CREATE INDEX idx_replay_events_account_occurred ON replay_events(account_id,occurred_at,id) WHERE account_id IS NOT NULL;
CREATE UNIQUE INDEX uq_replay_events_initial ON replay_events(trade_decision_id,event_type)
    WHERE event_type IN ('decision_created','risk_reviewed') AND account_id IS NOT NULL
      AND environment IS NOT NULL AND origin_type IS NOT NULL AND origin_id IS NOT NULL;
CREATE UNIQUE INDEX uq_replay_events_fill_trade ON replay_events(account_id,environment,trade_decision_id,event_type,(payload->>'order_id'),(payload->>'trade_id'))
    WHERE event_type='fill_observed' AND account_id IS NOT NULL AND environment IS NOT NULL
      AND origin_type IS NOT NULL AND origin_id IS NOT NULL AND payload->>'order_id' IS NOT NULL AND payload->>'trade_id' IS NOT NULL;
CREATE UNIQUE INDEX uq_replay_events_fill_cumulative ON replay_events(account_id,environment,trade_decision_id,event_type,(payload->>'order_id'),(payload->>'cumulative_quantity'))
    WHERE event_type='fill_observed' AND account_id IS NOT NULL AND environment IS NOT NULL
      AND origin_type IS NOT NULL AND origin_id IS NOT NULL AND payload->>'order_id' IS NOT NULL AND payload->>'cumulative_quantity' IS NOT NULL;
CREATE UNIQUE INDEX uq_replay_events_position ON replay_events(account_id,trade_decision_id,event_type,(payload->>'position_id'))
    WHERE event_type='position_updated' AND account_id IS NOT NULL AND environment IS NOT NULL
      AND origin_type IS NOT NULL AND origin_id IS NOT NULL AND payload->>'position_id' IS NOT NULL;
CREATE UNIQUE INDEX uq_orders_client_order_id ON orders(client_order_id) WHERE client_order_id IS NOT NULL;
CREATE INDEX idx_financial_fill_idempotency_account ON financial_fill_idempotency(account_id,created_at,idempotency_key) WHERE account_id IS NOT NULL;
CREATE INDEX idx_prediction_settlement_idempotency_account ON prediction_settlement_idempotency(account_id,created_at,idempotency_key) WHERE account_id IS NOT NULL;
CREATE INDEX idx_copy_subscriptions_account_status ON copy_subscriptions(account_id,status,created_at,id) WHERE account_id IS NOT NULL;
CREATE INDEX idx_copy_trade_intents_account_created ON copy_trade_intents(account_id,created_at,id) WHERE account_id IS NOT NULL;
CREATE INDEX idx_copy_origin_rebalance_runs_account_created ON copy_origin_rebalance_runs(account_id,created_at,id) WHERE account_id IS NOT NULL;
CREATE INDEX idx_copy_origin_rebalance_intents_account_run ON copy_origin_rebalance_intents(account_id,run_id,sequence) WHERE account_id IS NOT NULL;
CREATE INDEX idx_copy_target_drift_runs_account_created ON copy_target_drift_runs(account_id,created_at,id) WHERE account_id IS NOT NULL;
CREATE INDEX idx_copy_target_drift_legs_account_run ON copy_target_drift_legs(account_id,run_id,sequence) WHERE account_id IS NOT NULL;
CREATE INDEX idx_conversations_account_run ON conversations(account_id,pipeline_run_trade_date,pipeline_run_id,created_at,id) WHERE account_id IS NOT NULL;
CREATE INDEX idx_conversation_messages_account_conversation ON conversation_messages(account_id,conversation_id,created_at,id) WHERE account_id IS NOT NULL;
CREATE INDEX idx_agent_memories_account_run ON agent_memories(account_id,pipeline_run_trade_date,pipeline_run_id,created_at,id) WHERE account_id IS NOT NULL;

CREATE TABLE account_projection_outbox (
    id UUID PRIMARY KEY,
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    request_kind TEXT NOT NULL CHECK (request_kind IN ('economic_fill','mark_rebuild')),
    through_transaction_id UUID NOT NULL REFERENCES ledger_transactions(id) ON DELETE RESTRICT,
    as_of TIMESTAMPTZ NOT NULL CHECK (as_of=date_trunc('microseconds',as_of)),
    mark_as_of TIMESTAMPTZ CHECK (mark_as_of IS NULL OR mark_as_of=date_trunc('microseconds',mark_as_of)),
    mark_generation UUID NOT NULL,
    mark_source TEXT,
    mark_namespace TEXT,
    max_mark_age_microseconds BIGINT,
    status TEXT NOT NULL CHECK (status IN ('pending','processing','retry','completed','degraded')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count>=0),
    next_attempt_at TIMESTAMPTZ NOT NULL CHECK (next_attempt_at=date_trunc('microseconds',next_attempt_at)),
    last_error_code TEXT,
    claimed_at TIMESTAMPTZ,
    claimed_by TEXT,
    claim_expires_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT date_trunc('microseconds',now()) CHECK (created_at=date_trunc('microseconds',created_at)),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT date_trunc('microseconds',now()) CHECK (updated_at=date_trunc('microseconds',updated_at)),
    CHECK (
        (request_kind='economic_fill' AND mark_as_of IS NULL AND mark_generation='00000000-0000-0000-0000-000000000000'::UUID AND mark_source IS NULL AND mark_namespace IS NULL AND max_mark_age_microseconds IS NULL) OR
        (request_kind='mark_rebuild' AND mark_as_of IS NOT NULL AND mark_generation<>'00000000-0000-0000-0000-000000000000'::UUID AND mark_source IS NOT NULL AND mark_namespace IS NOT NULL AND max_mark_age_microseconds IS NOT NULL AND max_mark_age_microseconds>0)
    ),
    CHECK (
        (status='processing' AND claimed_at IS NOT NULL AND claimed_by IS NOT NULL AND claim_expires_at IS NOT NULL) OR
        (status<>'processing' AND claimed_at IS NULL AND claimed_by IS NULL AND claim_expires_at IS NULL)
    ),
    CHECK ((status='completed')=(completed_at IS NOT NULL)),
    CHECK (last_error_code IS NULL OR (last_error_code=btrim(last_error_code) AND last_error_code<>'' AND char_length(last_error_code)<=128)),
    CHECK (claimed_by IS NULL OR (claimed_by=btrim(claimed_by) AND claimed_by<>'' AND char_length(claimed_by)<=256)),
    CHECK (mark_source IS NULL OR (mark_source=lower(btrim(mark_source)) AND mark_source<>'')),
    CHECK (mark_namespace IS NULL OR (mark_namespace=btrim(mark_namespace) AND mark_namespace<>''))
);

CREATE UNIQUE INDEX uq_account_projection_outbox_request
    ON account_projection_outbox(account_id,request_kind,through_transaction_id,mark_generation);
CREATE INDEX idx_account_projection_outbox_claimable
    ON account_projection_outbox(status,next_attempt_at,claim_expires_at,created_at,id);

CREATE FUNCTION validate_account_projection_outbox_row() RETURNS TRIGGER AS $$
DECLARE frontier ledger_transactions%ROWTYPE;
BEGIN
    SELECT * INTO frontier FROM ledger_transactions WHERE id=NEW.through_transaction_id;
    IF frontier.id IS NULL OR frontier.account_id<>NEW.account_id OR
       frontier.effective_at>NEW.as_of OR frontier.observed_at>NEW.as_of THEN
        RAISE EXCEPTION 'projection outbox frontier does not belong to the account snapshot';
    END IF;
    IF NEW.updated_at<NEW.created_at OR NEW.next_attempt_at<NEW.created_at OR
       (NEW.mark_as_of IS NOT NULL AND NEW.mark_as_of>NEW.as_of) OR
       (NEW.claimed_at IS NOT NULL AND NEW.claim_expires_at<=NEW.claimed_at) THEN
        RAISE EXCEPTION 'projection outbox timestamps are inconsistent';
    END IF;
    IF TG_OP='UPDATE' AND (NEW.id,NEW.account_id,NEW.request_kind,NEW.through_transaction_id,NEW.as_of,NEW.mark_as_of,NEW.mark_generation,NEW.mark_source,NEW.mark_namespace,NEW.max_mark_age_microseconds,NEW.created_at)
       IS DISTINCT FROM (OLD.id,OLD.account_id,OLD.request_kind,OLD.through_transaction_id,OLD.as_of,OLD.mark_as_of,OLD.mark_generation,OLD.mark_source,OLD.mark_namespace,OLD.max_mark_age_microseconds,OLD.created_at) THEN
        RAISE EXCEPTION 'projection outbox request identity is immutable';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_validate_account_projection_outbox_row
    BEFORE INSERT OR UPDATE ON account_projection_outbox
    FOR EACH ROW EXECUTE FUNCTION validate_account_projection_outbox_row();

CREATE OR REPLACE FUNCTION validate_canonical_projection_checkpoint() RETURNS TRIGGER AS $$
DECLARE
    expected_account_currency TEXT;
    expected_transaction_count INTEGER;
    frontier_account_id UUID;
    frontier_effective_at TIMESTAMPTZ;
    frontier_observed_at TIMESTAMPTZ;
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

    SELECT account_id,effective_at,observed_at
      INTO frontier_account_id,frontier_effective_at,frontier_observed_at
    FROM ledger_transactions WHERE id=NEW.through_transaction_id;
    IF frontier_account_id IS NULL OR frontier_account_id<>NEW.account_id OR
       frontier_effective_at>NEW.as_of OR frontier_observed_at>NEW.as_of THEN
        RAISE EXCEPTION 'canonical checkpoint ledger frontier is unavailable or mismatched';
    END IF;
    SELECT COUNT(*)::INTEGER INTO expected_transaction_count
    FROM ledger_transactions
    WHERE account_id = NEW.account_id
      AND effective_at <= NEW.as_of
      AND observed_at <= NEW.as_of
      AND (effective_at,observed_at,id) <= (frontier_effective_at,frontier_observed_at,NEW.through_transaction_id);
    IF NEW.transaction_count <> expected_transaction_count THEN
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

DO $seed$
DECLARE
    reviewed_json CONSTANT JSONB := $policy${"schema":"capital-margin-policy-v1","currency":"USD","scale":12,"tiers":["500","5000","25000","100000","1000000","5000000"],"profiles":[{"name":"cash","initial_long":"1","initial_short":"0","maintenance_long":"1","maintenance_short":"0","maximum_gross":"1","cash_reserve":"0","allow_short":false,"unlimited":false},{"name":"portfolio","initial_long":"0.15","initial_short":"0.3","maintenance_long":"0.15","maintenance_short":"0.3","maximum_gross":"6","cash_reserve":"0","allow_short":true,"unlimited":false},{"name":"reg_t","initial_long":"0.5","initial_short":"1.5","maintenance_long":"0.25","maintenance_short":"0.3","maximum_gross":"2","cash_reserve":"0","allow_short":true,"unlimited":false},{"name":"stress_unlimited","initial_long":"0","initial_short":"0","maintenance_long":"0","maintenance_short":"0","maximum_gross":"0","cash_reserve":"0","allow_short":true,"unlimited":true}]}$policy$::JSONB;
    seed_bytes BYTEA;
    seed_sha TEXT;
    seed_version TEXT;
    seed_artifact_id UUID;
    seed_binding_id UUID;
BEGIN
    seed_bytes := capital_margin_policy_v1_canonical_bytes(reviewed_json);
    seed_sha := encode(digest(seed_bytes,'sha256'),'hex');
    seed_version := 'capital-margin-policy-v1@sha256:' || seed_sha;
    seed_artifact_id := economic_deterministic_uuid('capital-margin-policy-artifact',seed_version);
    seed_binding_id := economic_deterministic_uuid('capital-policy-binding','00000000-0000-4000-8000-000000000064',seed_version);
    INSERT INTO capital_margin_policy_artifacts(id,schema_name,policy_version,sha256,canonical_bytes,canonical_json)
    VALUES(seed_artifact_id,'capital-margin-policy-v1',seed_version,seed_sha,seed_bytes,reviewed_json);
    INSERT INTO account_capital_policy_bindings(id,account_id,policy_artifact_id,policy_version,tier,margin_profile,environment,starting_capital,buying_power_multiplier,evidence_class,storage_namespace,currency)
    SELECT seed_binding_id,id,seed_artifact_id,seed_version,starting_capital,margin_profile,environment,starting_capital,buying_power_multiplier,evidence_class,storage_namespace,base_currency
    FROM accounts WHERE id='00000000-0000-4000-8000-000000000064'::UUID;
    IF NOT FOUND THEN RAISE EXCEPTION 'migration-64 canonical account is missing'; END IF;
END;
$seed$;
