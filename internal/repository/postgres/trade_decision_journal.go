package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// TradeDecisionJournalRepo implements repository.TradeDecisionJournalRepository using PostgreSQL.
type TradeDecisionJournalRepo struct {
	pool      *pgxpool.Pool
	accountID uuid.UUID
}

// Compile-time check that TradeDecisionJournalRepo satisfies the repository interface.
var _ repository.TradeDecisionJournalRepository = (*TradeDecisionJournalRepo)(nil)
var _ repository.AtomicOrderReplayRepository = (*TradeDecisionJournalRepo)(nil)
var _ repository.AtomicDecisionReplayRepository = (*TradeDecisionJournalRepo)(nil)

// NewTradeDecisionJournalRepo returns a repository backed by the given pool.
func NewTradeDecisionJournalRepo(pool *pgxpool.Pool, accountID uuid.UUID) *TradeDecisionJournalRepo {
	return &TradeDecisionJournalRepo{pool: pool, accountID: accountID}
}

const tradeDecisionSelectSQL = `SELECT id, account_id, environment, origin_type, origin_id, pipeline_run_trade_date, strategy_id, pipeline_run_id, market_type, instrument_key,
		external_market_id, side, outcome, fair_value::double precision,
		executable_price::double precision, spread::double precision,
		depth::double precision, gross_ev::double precision, net_ev::double precision,
		kelly_fraction::double precision, proposed_size::double precision,
		approved_size::double precision, risk_status, risk_reasons, evidence,
		features, regime_tags, prompt_text, llm_provider, llm_model,
		prompt_tokens, completion_tokens, latency_ms, cost_usd::double precision,
	paper_order_id, live_order_id, status, created_at,
		updated_at
	 FROM trade_decisions`

// Create inserts a new trade decision and populates the generated ID and timestamps.
func (r *TradeDecisionJournalRepo) Create(ctx context.Context, decision *domain.TradeDecision) error {
	return r.create(ctx, r.pool, decision, false)
}

type tradeDecisionQueryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (r *TradeDecisionJournalRepo) create(ctx context.Context, db tradeDecisionQueryRower, decision *domain.TradeDecision, idempotent bool) error {
	if err := validateOptionalPipelineRunRef(decision.PipelineRunID, decision.PipelineRunTradeDate); err != nil {
		return fmt.Errorf("postgres: create trade decision: %w", err)
	}
	if decision.AccountID != uuid.Nil && decision.AccountID != r.accountID {
		return fmt.Errorf("postgres: create trade decision: account mismatch")
	}
	decision.AccountID = r.accountID
	if decision.ID == uuid.Nil {
		decision.ID = uuid.New()
	}
	evidence, err := marshalTradeDecisionJSON(decision.Evidence)
	if err != nil {
		return err
	}
	features, err := marshalTradeDecisionJSON(decision.Features)
	if err != nil {
		return err
	}

	query :=
		`INSERT INTO trade_decisions (
			id, account_id, environment, origin_type, origin_id, pipeline_run_trade_date, strategy_id, pipeline_run_id, market_type, instrument_key, external_market_id,
			side, outcome, fair_value, executable_price, spread, depth, gross_ev,
			net_ev, kelly_fraction, proposed_size, approved_size, risk_status,
			risk_reasons, evidence, features, regime_tags, prompt_text, llm_provider,
			llm_model, prompt_tokens, completion_tokens, latency_ms, cost_usd,
			paper_order_id, live_order_id, status
		)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37)`
	if idempotent {
		query += ` ON CONFLICT (id) DO UPDATE SET id=EXCLUDED.id WHERE
			trade_decisions.account_id=EXCLUDED.account_id AND
			trade_decisions.environment=EXCLUDED.environment AND
			trade_decisions.origin_type=EXCLUDED.origin_type AND
			trade_decisions.origin_id=EXCLUDED.origin_id AND
			trade_decisions.pipeline_run_trade_date IS NOT DISTINCT FROM EXCLUDED.pipeline_run_trade_date AND
			trade_decisions.strategy_id IS NOT DISTINCT FROM EXCLUDED.strategy_id AND
			trade_decisions.pipeline_run_id IS NOT DISTINCT FROM EXCLUDED.pipeline_run_id AND
			trade_decisions.market_type=EXCLUDED.market_type AND
			trade_decisions.instrument_key=EXCLUDED.instrument_key AND
			trade_decisions.external_market_id IS NOT DISTINCT FROM EXCLUDED.external_market_id AND
			trade_decisions.side=EXCLUDED.side AND trade_decisions.outcome IS NOT DISTINCT FROM EXCLUDED.outcome AND
			trade_decisions.fair_value=EXCLUDED.fair_value AND trade_decisions.executable_price=EXCLUDED.executable_price AND
			trade_decisions.spread=EXCLUDED.spread AND trade_decisions.depth=EXCLUDED.depth AND
			trade_decisions.gross_ev=EXCLUDED.gross_ev AND trade_decisions.net_ev=EXCLUDED.net_ev AND
			trade_decisions.kelly_fraction=EXCLUDED.kelly_fraction AND trade_decisions.proposed_size=EXCLUDED.proposed_size AND
			trade_decisions.approved_size=EXCLUDED.approved_size AND trade_decisions.risk_status=EXCLUDED.risk_status AND
			trade_decisions.risk_reasons=EXCLUDED.risk_reasons AND trade_decisions.evidence=EXCLUDED.evidence AND
			trade_decisions.features=EXCLUDED.features AND trade_decisions.regime_tags=EXCLUDED.regime_tags AND
			trade_decisions.prompt_text IS NOT DISTINCT FROM EXCLUDED.prompt_text AND
			trade_decisions.llm_provider IS NOT DISTINCT FROM EXCLUDED.llm_provider AND
			trade_decisions.llm_model IS NOT DISTINCT FROM EXCLUDED.llm_model AND
			trade_decisions.prompt_tokens IS NOT DISTINCT FROM EXCLUDED.prompt_tokens AND
			trade_decisions.completion_tokens IS NOT DISTINCT FROM EXCLUDED.completion_tokens AND
			trade_decisions.latency_ms IS NOT DISTINCT FROM EXCLUDED.latency_ms AND
			trade_decisions.cost_usd IS NOT DISTINCT FROM EXCLUDED.cost_usd`
	}
	query += ` RETURNING id, created_at, updated_at`
	row := db.QueryRow(ctx, query,
		decision.ID, r.accountID, decision.Environment, decision.OriginType, decision.OriginID, decision.PipelineRunTradeDate, decision.StrategyID,
		decision.PipelineRunID,
		decision.MarketType,
		decision.InstrumentKey,
		nullString(decision.ExternalMarketID),
		decision.Side,
		nullString(decision.Outcome),
		decision.FairValue,
		decision.ExecutablePrice,
		decision.Spread,
		decision.Depth,
		decision.GrossEV,
		decision.NetEV,
		decision.KellyFraction,
		decision.ProposedSize,
		decision.ApprovedSize,
		decision.RiskStatus,
		stringSliceOrEmpty(decision.RiskReasons),
		evidence,
		features,
		stringSliceOrEmpty(decision.RegimeTags),
		nullString(decision.PromptText),
		nullString(decision.LLMProvider),
		nullString(decision.LLMModel),
		nullableInt(decision.PromptTokens),
		nullableInt(decision.CompletionTokens),
		nullableInt(decision.LatencyMS),
		nullableFloat(decision.CostUSD),
		decision.PaperOrderID,
		decision.LiveOrderID,
		decision.Status,
	)

	if err := row.Scan(&decision.ID, &decision.CreatedAt, &decision.UpdatedAt); err != nil {
		if idempotent && errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("postgres: create trade decision: immutable payload changed: %w", repository.ErrIdempotencyConflict)
		}
		return fmt.Errorf("postgres: create trade decision: %w", err)
	}

	return nil
}

// CreateWithInitialReplay commits the decision and mandatory pre-execution
// replay evidence together. A same-ID retry repairs missing initial events.
func (r *TradeDecisionJournalRepo) CreateWithInitialReplay(ctx context.Context, decision *domain.TradeDecision) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin decision replay: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := r.create(ctx, tx, decision, true); err != nil {
		return err
	}
	decisionPayload, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("postgres: marshal decision replay: %w", err)
	}
	riskPayload, err := json.Marshal(map[string]any{"status": decision.RiskStatus, "reasons": decision.RiskReasons, "proposed_size": decision.ProposedSize, "approved_size": decision.ApprovedSize})
	if err != nil {
		return fmt.Errorf("postgres: marshal risk replay: %w", err)
	}
	for _, event := range []struct {
		type_   domain.ReplayEventType
		source  string
		payload []byte
		at      time.Time
	}{{domain.ReplayEventTypeDecisionCreated, "decision_journal", decisionPayload, decision.CreatedAt}, {domain.ReplayEventTypeRiskReviewed, "risk_engine", riskPayload, decision.UpdatedAt}} {
		if _, err := tx.Exec(ctx, `INSERT INTO replay_events (account_id,environment,origin_type,origin_id,trade_decision_id,event_type,source,payload,occurred_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (trade_decision_id,event_type) WHERE event_type IN ('decision_created','risk_reviewed') DO NOTHING`, r.accountID, decision.Environment, decision.OriginType, decision.OriginID, decision.ID, event.type_, event.source, event.payload, event.at); err != nil {
			return fmt.Errorf("postgres: insert initial decision replay: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit decision replay: %w", err)
	}
	return nil
}

// Get retrieves a trade decision by ID.
func (r *TradeDecisionJournalRepo) Get(ctx context.Context, id uuid.UUID) (*domain.TradeDecision, error) {
	row := r.pool.QueryRow(ctx, tradeDecisionSelectSQL+` WHERE id = $1 AND account_id = $2`, id, r.accountID)
	decision, err := scanTradeDecision(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("postgres: get trade decision %s: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("postgres: get trade decision: %w", err)
	}
	return decision, nil
}

// List returns trade decisions matching the provided filter with pagination.
func (r *TradeDecisionJournalRepo) List(ctx context.Context, filter repository.TradeDecisionFilter, limit, offset int) ([]domain.TradeDecision, error) {
	query, args := buildTradeDecisionListQuery(r.accountID, filter, limit, offset)
	return r.list(ctx, query, args, "list trade decisions")
}

// Count returns the number of trade decisions matching the filter.
func (r *TradeDecisionJournalRepo) Count(ctx context.Context, filter repository.TradeDecisionFilter) (int, error) {
	query, args := buildTradeDecisionCountQuery(r.accountID, filter)
	var total int
	if err := r.pool.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("postgres: count trade decisions: %w", err)
	}
	return total, nil
}

func (r *TradeDecisionJournalRepo) CountByStatus(ctx context.Context, filter repository.TradeDecisionFilter) (map[domain.TradeDecisionStatus]int, error) {
	query, args := buildTradeDecisionFilteredQuery(r.accountID, "SELECT status, COUNT(*) FROM trade_decisions", filter, 0, 0, false)
	query += " GROUP BY status ORDER BY status"
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: count trade decisions by status: %w", err)
	}
	defer rows.Close()
	out := map[domain.TradeDecisionStatus]int{}
	for rows.Next() {
		var key string
		var total int
		if err := rows.Scan(&key, &total); err != nil {
			return nil, fmt.Errorf("postgres: count trade decisions by status scan: %w", err)
		}
		out[domain.TradeDecisionStatus(key)] = total
	}
	return out, rows.Err()
}

func (r *TradeDecisionJournalRepo) CountByNoActionReason(ctx context.Context, filter repository.TradeDecisionFilter) (map[string]int, error) {
	filtered, args := buildTradeDecisionFilteredQuery(r.accountID, `SELECT
			id, risk_reasons, status, evidence
			FROM trade_decisions`, filter, 0, 0, false)
	query := `SELECT
		COALESCE(SUM(CASE WHEN risk_reasons @> ARRAY['hold_signal']::text[] OR status = 'hold' THEN 1 ELSE 0 END), 0) AS hold_signal,
		COALESCE(SUM(CASE WHEN risk_reasons @> ARRAY['risk_rejected']::text[] OR evidence::text ILIKE '%risk%' THEN 1 ELSE 0 END), 0) AS risk_rejected,
		COALESCE(SUM(CASE WHEN risk_reasons @> ARRAY['sizing_zero']::text[] OR evidence::text ILIKE '%size%0%' THEN 1 ELSE 0 END), 0) AS sizing_zero,
		COALESCE(SUM(CASE WHEN risk_reasons @> ARRAY['sell_without_position']::text[] THEN 1 ELSE 0 END), 0) AS sell_without_position,
		COALESCE(SUM(CASE WHEN risk_reasons @> ARRAY['kill_switch']::text[] THEN 1 ELSE 0 END), 0) AS kill_switch,
		COALESCE(SUM(CASE WHEN risk_reasons @> ARRAY['live_gate_denied']::text[] THEN 1 ELSE 0 END), 0) AS live_gate_denied,
		COALESCE(SUM(CASE WHEN risk_reasons @> ARRAY['missing_data']::text[] THEN 1 ELSE 0 END), 0) AS missing_data,
		COALESCE(SUM(CASE WHEN COALESCE(array_length(risk_reasons,1),0)=0 AND evidence IS NULL THEN 1 ELSE 0 END), 0) AS unknown
		FROM (` + filtered + `) td`
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: count trade decisions reasons: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	if rows.Next() {
		var hold, risk, size, sell, kill, live, missing, unknown int
		if err := rows.Scan(&hold, &risk, &size, &sell, &kill, &live, &missing, &unknown); err != nil {
			return nil, fmt.Errorf("postgres: count trade decisions reasons scan: %w", err)
		}
		out["hold_signal"] = hold
		out["risk_rejected"] = risk
		out["sizing_zero"] = size
		out["sell_without_position"] = sell
		out["kill_switch"] = kill
		out["live_gate_denied"] = live
		out["missing_data"] = missing
		out["unknown"] = unknown
	}
	return out, rows.Err()
}

// AttachPaperOrder links a paper order to the trade decision.
func (r *TradeDecisionJournalRepo) AttachPaperOrder(ctx context.Context, decisionID, orderID uuid.UUID) (bool, error) {
	return r.attachOrder(ctx, decisionID, orderID, "paper_order_id", domain.TradeDecisionStatusPaper, false, nil)
}

func (r *TradeDecisionJournalRepo) AttachPaperOrderScoped(ctx context.Context, decisionID, orderID uuid.UUID, scope repository.DecisionOrderAttachmentScope) (bool, error) {
	return r.attachOrder(ctx, decisionID, orderID, "paper_order_id", domain.TradeDecisionStatusPaper, false, &scope)
}

// AttachLiveOrder links a live order to the trade decision.
func (r *TradeDecisionJournalRepo) AttachLiveOrder(ctx context.Context, decisionID, orderID uuid.UUID) (bool, error) {
	return r.attachOrder(ctx, decisionID, orderID, "live_order_id", domain.TradeDecisionStatusLive, true, nil)
}

func (r *TradeDecisionJournalRepo) AttachLiveOrderScoped(ctx context.Context, decisionID, orderID uuid.UUID, scope repository.DecisionOrderAttachmentScope) (bool, error) {
	return r.attachOrder(ctx, decisionID, orderID, "live_order_id", domain.TradeDecisionStatusLive, true, &scope)
}

// AttachOrderWithReplay atomically links an order and persists the matching
// ordered replay event. The decision row lock serializes same-decision retries.
func (r *TradeDecisionJournalRepo) AttachOrderWithReplay(ctx context.Context, decisionID, orderID uuid.UUID, live bool, source string, occurredAt time.Time) error {
	return r.attachOrderWithReplay(ctx, decisionID, orderID, live, source, occurredAt, nil)
}

func (r *TradeDecisionJournalRepo) AttachOrderWithReplayScoped(ctx context.Context, decisionID, orderID uuid.UUID, live bool, source string, occurredAt time.Time, scope repository.DecisionOrderAttachmentScope) error {
	return r.attachOrderWithReplay(ctx, decisionID, orderID, live, source, occurredAt, &scope)
}

func (r *TradeDecisionJournalRepo) attachOrderWithReplay(ctx context.Context, decisionID, orderID uuid.UUID, live bool, source string, occurredAt time.Time, scope *repository.DecisionOrderAttachmentScope) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin order replay attachment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	column, status, eventType := "paper_order_id", domain.TradeDecisionStatusPaper, domain.ReplayEventTypePaperOrdered
	if live {
		column, status, eventType = "live_order_id", domain.TradeDecisionStatusLive, domain.ReplayEventTypeLiveOrdered
	}
	var accountID uuid.UUID
	var attached *uuid.UUID
	if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT account_id,%s FROM trade_decisions WHERE id=$1 AND account_id=$2 FOR UPDATE`, column), decisionID, r.accountID).Scan(&accountID, &attached); err != nil {
		return fmt.Errorf("postgres: lock trade decision for order replay: %w", err)
	}
	if attached != nil && *attached != orderID {
		return fmt.Errorf("postgres: attach %s: different order already attached", column)
	}
	if attached == nil {
		query, args := buildTradeDecisionAttachQuery(column, r.accountID, decisionID, orderID, status, live, scope)
		var updatedID uuid.UUID
		if err := tx.QueryRow(ctx, query, args...).Scan(&updatedID); err != nil {
			return fmt.Errorf("postgres: attach order with replay: %w", err)
		}
	}
	if source == "" {
		source = "order_manager"
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	_, err = tx.Exec(ctx, `INSERT INTO replay_events (account_id,environment,origin_type,origin_id,trade_decision_id,event_type,source,payload,occurred_at)
		SELECT account_id,environment,origin_type,origin_id,id,$3,$4,jsonb_build_object('order_id',$2::text),$5
		FROM trade_decisions td WHERE td.id=$1 AND td.account_id=$6
		AND NOT EXISTS (SELECT 1 FROM replay_events re WHERE re.trade_decision_id=td.id AND re.account_id=td.account_id AND re.event_type=$3 AND re.payload->>'order_id'=$2::text)`,
		decisionID, orderID, eventType, source, occurredAt, r.accountID)
	if err != nil {
		return fmt.Errorf("postgres: insert attached order replay: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit order replay attachment: %w", err)
	}
	return nil
}

// ResolvePredictionOutcome marks a paper event-market decision closed after
// its provider outcome has been durably settled.
func (r *TradeDecisionJournalRepo) ResolvePredictionOutcome(ctx context.Context, decisionID uuid.UUID) error {
	var updatedID uuid.UUID
	err := r.pool.QueryRow(ctx, `UPDATE trade_decisions SET status = $2, updated_at = NOW()
		WHERE id = $1 AND status = $3 AND account_id=$4 RETURNING id`, decisionID, domain.TradeDecisionStatusClosed, domain.TradeDecisionStatusPaper, r.accountID).Scan(&updatedID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("postgres: resolve prediction decision %s: %w", decisionID, ErrNotFound)
		}
		return fmt.Errorf("postgres: resolve prediction decision: %w", err)
	}
	return nil
}

func (r *TradeDecisionJournalRepo) attachOrder(ctx context.Context, decisionID, orderID uuid.UUID, column string, status domain.TradeDecisionStatus, live bool, scope *repository.DecisionOrderAttachmentScope) (bool, error) {
	query, args := buildTradeDecisionAttachQuery(column, r.accountID, decisionID, orderID, status, live, scope)
	var updatedID uuid.UUID
	if err := r.pool.QueryRow(ctx, query, args...).Scan(&updatedID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			decision, getErr := r.Get(ctx, decisionID)
			if getErr != nil {
				return false, fmt.Errorf("postgres: attach %s order to trade decision %s: %w", column, decisionID, getErr)
			}
			attached := decision.PaperOrderID
			if live {
				attached = decision.LiveOrderID
			}
			if attached != nil && *attached == orderID {
				return false, nil
			}
			if attached != nil {
				return false, fmt.Errorf("postgres: attach %s order to trade decision %s: different order already attached", column, decisionID)
			}
			return false, fmt.Errorf("postgres: attach %s order to trade decision %s: order ownership or environment mismatch", column, decisionID)
		}
		return false, fmt.Errorf("postgres: attach %s order to trade decision: %w", column, err)
	}
	return true, nil
}

func (r *TradeDecisionJournalRepo) list(ctx context.Context, query string, args []any, op string) ([]domain.TradeDecision, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: %s: %w", op, err)
	}
	defer rows.Close()

	var decisions []domain.TradeDecision
	for rows.Next() {
		decision, err := scanTradeDecision(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: %s scan: %w", op, err)
		}
		decisions = append(decisions, *decision)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: %s rows: %w", op, err)
	}

	return decisions, nil
}

func scanTradeDecision(sc scanner) (*domain.TradeDecision, error) {
	var (
		decision         domain.TradeDecision
		strategyID       *uuid.UUID
		pipelineRunID    *uuid.UUID
		externalID       *string
		outcome          *string
		promptText       *string
		llmProvider      *string
		llmModel         *string
		promptTokens     *int
		completionTokens *int
		latencyMS        *int
		costUSD          *float64
		riskReasons      []string
		evidence         []byte
		features         []byte
		regimeTags       []string
		paperOrderID     *uuid.UUID
		liveOrderID      *uuid.UUID
	)

	if err := sc.Scan(
		&decision.ID,
		&decision.AccountID, &decision.Environment, &decision.OriginType, &decision.OriginID, &decision.PipelineRunTradeDate,
		&strategyID,
		&pipelineRunID,
		&decision.MarketType,
		&decision.InstrumentKey,
		&externalID,
		&decision.Side,
		&outcome,
		&decision.FairValue,
		&decision.ExecutablePrice,
		&decision.Spread,
		&decision.Depth,
		&decision.GrossEV,
		&decision.NetEV,
		&decision.KellyFraction,
		&decision.ProposedSize,
		&decision.ApprovedSize,
		&decision.RiskStatus,
		&riskReasons,
		&evidence,
		&features,
		&regimeTags,
		&promptText,
		&llmProvider,
		&llmModel,
		&promptTokens,
		&completionTokens,
		&latencyMS,
		&costUSD,
		&paperOrderID,
		&liveOrderID,
		&decision.Status,
		&decision.CreatedAt,
		&decision.UpdatedAt,
	); err != nil {
		return nil, err
	}

	decision.StrategyID = strategyID
	decision.PipelineRunID = pipelineRunID
	if externalID != nil {
		decision.ExternalMarketID = *externalID
	}
	if outcome != nil {
		decision.Outcome = *outcome
	}
	decision.RiskReasons = riskReasons
	decision.Evidence = json.RawMessage(evidence)
	decision.Features = json.RawMessage(features)
	decision.RegimeTags = regimeTags
	if promptText != nil {
		decision.PromptText = *promptText
	}
	if llmProvider != nil {
		decision.LLMProvider = *llmProvider
	}
	if llmModel != nil {
		decision.LLMModel = *llmModel
	}
	decision.PromptTokens = promptTokens
	decision.CompletionTokens = completionTokens
	decision.LatencyMS = latencyMS
	decision.CostUSD = costUSD
	decision.PaperOrderID = paperOrderID
	decision.LiveOrderID = liveOrderID

	return &decision, nil
}

func buildTradeDecisionCountQuery(accountID uuid.UUID, filter repository.TradeDecisionFilter) (string, []any) {
	query, args := buildTradeDecisionFilteredQuery(accountID, "SELECT COUNT(*) FROM trade_decisions", filter, 0, 0, false)
	return query, args
}

func buildTradeDecisionListQuery(accountID uuid.UUID, filter repository.TradeDecisionFilter, limit, offset int) (string, []any) {
	query, args := buildTradeDecisionFilteredQuery(accountID, tradeDecisionSelectSQL, filter, limit, offset, true)
	return query, args
}

func buildTradeDecisionAttachQuery(column string, accountID, decisionID, orderID uuid.UUID, status domain.TradeDecisionStatus, live bool, scope *repository.DecisionOrderAttachmentScope) (string, []any) {
	lineage := ""
	args := []any{decisionID, accountID, orderID, status, live}
	if scope != nil {
		lineage = `
			AND o.pipeline_run_id IS NOT DISTINCT FROM $6 AND o.pipeline_run_trade_date IS NOT DISTINCT FROM $7
			AND o.copy_origin_rebalance_run_id IS NOT DISTINCT FROM $8 AND o.strategy_id IS NOT DISTINCT FROM $9`
		args = append(args, scope.PipelineRunID, scope.PipelineRunTradeDate, scope.CopyOriginRebalanceRunID, scope.StrategyID)
	}
	query := fmt.Sprintf(`UPDATE trade_decisions td SET %s = $3, status = $4, updated_at = NOW()
		WHERE td.id = $1 AND td.account_id=$2 AND td.%s IS NULL
		AND EXISTS (SELECT 1 FROM orders o WHERE o.id=$3 AND o.account_id=td.account_id
			AND o.environment=td.environment AND o.origin_type=td.origin_type AND o.origin_id=td.origin_id
			AND o.pipeline_run_id IS NOT DISTINCT FROM td.pipeline_run_id AND o.pipeline_run_trade_date IS NOT DISTINCT FROM td.pipeline_run_trade_date
			AND o.strategy_id IS NOT DISTINCT FROM td.strategy_id
			%s
			AND (($5 AND o.environment='live') OR (NOT $5 AND o.environment IN ('paper_scored','paper_stress'))))
		RETURNING td.id`, column, column, lineage)
	return query, args
}

func buildTradeDecisionFilteredQuery(accountID uuid.UUID, base string, filter repository.TradeDecisionFilter, limit, offset int, includePagination bool) (string, []any) {
	var (
		conditions []string
		args       []any
		argIdx     int
	)

	nextArg := func(v any) string {
		argIdx++
		args = append(args, v)
		return fmt.Sprintf("$%d", argIdx)
	}
	conditions = append(conditions, "account_id = "+nextArg(accountID))

	if filter.StrategyID != nil {
		conditions = append(conditions, "strategy_id = "+nextArg(*filter.StrategyID))
	}
	if filter.InstrumentKey != "" {
		conditions = append(conditions, "instrument_key = "+nextArg(filter.InstrumentKey))
	}
	if filter.MarketType != "" {
		conditions = append(conditions, "market_type = "+nextArg(filter.MarketType))
	}
	if filter.Status != "" {
		conditions = append(conditions, "status = "+nextArg(filter.Status))
	}
	if filter.CreatedAfter != nil {
		conditions = append(conditions, "created_at >= "+nextArg(*filter.CreatedAfter))
	}
	if filter.CreatedBefore != nil {
		conditions = append(conditions, "created_at <= "+nextArg(*filter.CreatedBefore))
	}

	query := base
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	if includePagination {
		query += " ORDER BY created_at DESC, id DESC"
		query += fmt.Sprintf(" LIMIT %s OFFSET %s", nextArg(limit), nextArg(offset))
	}

	return query, args
}

func marshalTradeDecisionJSON(data json.RawMessage) ([]byte, error) {
	if len(data) == 0 {
		return []byte("{}"), nil
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("postgres: trade decision json is not valid")
	}
	return data, nil
}

func stringSliceOrEmpty(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return values
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableFloat(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}
