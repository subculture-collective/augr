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

// OpportunityRepo implements repository.OpportunityRepository using PostgreSQL.
type OpportunityRepo struct {
	pool      *pgxpool.Pool
	accountID uuid.UUID
}

var _ repository.OpportunityRepository = (*OpportunityRepo)(nil)

// NewOpportunityRepo returns a repository backed by the given pool.
func NewOpportunityRepo(pool *pgxpool.Pool, accountID uuid.UUID) *OpportunityRepo {
	return &OpportunityRepo{pool: pool, accountID: accountID}
}

// Create inserts a new opportunity.
func (r *OpportunityRepo) Create(ctx context.Context, opportunity *domain.Opportunity) error {
	return r.save(ctx, opportunity, false)
}

// UpsertQueuedByDedupeKey inserts or refreshes a queued opportunity by dedupe key.
func (r *OpportunityRepo) UpsertQueuedByDedupeKey(ctx context.Context, opportunity *domain.Opportunity) error {
	opportunity.Status = domain.OpportunityStatusQueued
	return r.save(ctx, opportunity, true)
}

// Get retrieves an opportunity by ID.
func (r *OpportunityRepo) Get(ctx context.Context, id uuid.UUID) (*domain.Opportunity, error) {
	row := r.pool.QueryRow(ctx, opportunitySelectSQL+` WHERE id = $1 AND account_id = $2`, id, r.accountID)
	opportunity, err := scanOpportunity(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("postgres: get opportunity %s: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("postgres: get opportunity: %w", err)
	}
	return opportunity, nil
}

// List returns opportunities matching the provided filter.
func (r *OpportunityRepo) List(ctx context.Context, filter repository.OpportunityFilter, limit, offset int) ([]domain.Opportunity, error) {
	query, args := buildOpportunityListQuery(r.accountID, filter, limit, offset)
	return r.list(ctx, query, args, "list opportunities")
}

// ExpireQueuedBefore promotes due queued opportunities to expired.
func (r *OpportunityRepo) ExpireQueuedBefore(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE portfolio_opportunities
		SET status = $1, reject_reason = $2, updated_at = NOW()
		WHERE status = $3 AND expires_at <= $4 AND account_id=$5`,
		domain.OpportunityStatusExpired,
		"expired_before_allocation",
		domain.OpportunityStatusQueued,
		before.UTC(), r.accountID,
	)
	if err != nil {
		return 0, fmt.Errorf("postgres: expire queued opportunities: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ListQueuedForAllocation returns the stable allocation snapshot for queued opportunities.
func (r *OpportunityRepo) ListQueuedForAllocation(ctx context.Context, asOf time.Time) ([]domain.Opportunity, error) {
	query := opportunitySelectSQL + ` WHERE status = $1 AND expires_at > $2 AND account_id=$3 ORDER BY expires_at ASC, created_at ASC, id ASC`
	return r.list(ctx, query, []any{domain.OpportunityStatusQueued, asOf.UTC(), r.accountID}, "list queued opportunities for allocation")
}

// ListSelectedForAllocation returns durable in-flight claims for restart reconciliation.
func (r *OpportunityRepo) ListSelectedForAllocation(ctx context.Context, claimID uuid.UUID, asOf time.Time) ([]domain.Opportunity, error) {
	query := opportunitySelectSQL + ` WHERE status = $1 AND account_id=$2 AND (allocation_claim_id=$3 OR allocation_claim_expires_at <= $4 OR (allocation_claim_id IS NULL AND allocation_claimed_at IS NULL AND allocation_claim_expires_at IS NULL)) ORDER BY allocation_claim_expires_at ASC NULLS FIRST, created_at ASC, id ASC`
	return r.list(ctx, query, []any{domain.OpportunityStatusSelected, r.accountID, claimID, asOf.UTC()}, "list recoverable selected opportunities for allocation")
}

func (r *OpportunityRepo) ClaimQueuedForAllocation(ctx context.Context, id, claimID uuid.UUID, claimedAt, claimExpiresAt time.Time) (bool, error) {
	if claimID == uuid.Nil || !claimExpiresAt.After(claimedAt) {
		return false, fmt.Errorf("postgres: claim queued opportunity: valid claim and lease are required")
	}
	tag, err := r.pool.Exec(ctx, `UPDATE portfolio_opportunities SET status=$1, allocation_claim_id=$2, allocation_claimed_at=$3, allocation_claim_expires_at=$4, reject_reason='', updated_at=NOW() WHERE id=$5 AND account_id=$6 AND status=$7`, domain.OpportunityStatusSelected, claimID, claimedAt.UTC(), claimExpiresAt.UTC(), id, r.accountID, domain.OpportunityStatusQueued)
	if err != nil {
		return false, fmt.Errorf("postgres: claim queued opportunity: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *OpportunityRepo) TakeOverExpiredAllocationClaim(ctx context.Context, id, claimID uuid.UUID, asOf, claimExpiresAt time.Time) (bool, error) {
	if claimID == uuid.Nil || !claimExpiresAt.After(asOf) {
		return false, fmt.Errorf("postgres: take over allocation claim: valid claim and lease are required")
	}
	tag, err := r.pool.Exec(ctx, `UPDATE portfolio_opportunities SET allocation_claim_id=$1, allocation_claimed_at=$2, allocation_claim_expires_at=$3, updated_at=NOW() WHERE id=$4 AND account_id=$5 AND status=$6 AND (allocation_claim_id=$1 OR allocation_claim_expires_at <= $2 OR (allocation_claim_id IS NULL AND allocation_claimed_at IS NULL AND allocation_claim_expires_at IS NULL))`, claimID, asOf.UTC(), claimExpiresAt.UTC(), id, r.accountID, domain.OpportunityStatusSelected)
	if err != nil {
		return false, fmt.Errorf("postgres: take over allocation claim: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *OpportunityRepo) TransitionClaimedStatus(ctx context.Context, id, claimID uuid.UUID, from, to domain.OpportunityStatus, rejectReason string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE portfolio_opportunities SET status=$1, reject_reason=$2, allocation_claim_id=NULL, allocation_claimed_at=NULL, allocation_claim_expires_at=NULL, updated_at=NOW() WHERE id=$3 AND account_id=$4 AND status=$5 AND allocation_claim_id=$6`, to, rejectReason, id, r.accountID, from, claimID)
	if err != nil {
		return false, fmt.Errorf("postgres: transition claimed opportunity: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// TransitionStatus performs a compare-and-swap lifecycle transition.
func (r *OpportunityRepo) TransitionStatus(ctx context.Context, id uuid.UUID, from, to domain.OpportunityStatus, rejectReason string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE portfolio_opportunities SET status=$1, reject_reason=$2, updated_at=NOW() WHERE id=$3 AND account_id=$4 AND status=$5`, to, rejectReason, id, r.accountID, from)
	if err != nil {
		return false, fmt.Errorf("postgres: transition opportunity status: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Count returns the number of opportunities matching the filter.
func (r *OpportunityRepo) Count(ctx context.Context, filter repository.OpportunityFilter) (int, error) {
	query, args := buildOpportunityCountQuery(r.accountID, filter)
	var total int
	if err := r.pool.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("postgres: count opportunities: %w", err)
	}
	return total, nil
}

// UpdateStatus updates the status and reject reason for an opportunity.
func (r *OpportunityRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.OpportunityStatus, rejectReason string) error {
	row := r.pool.QueryRow(ctx,
		`UPDATE portfolio_opportunities
		 SET status = $1, reject_reason = $2, updated_at = NOW()
		 WHERE id = $3 AND account_id=$4
		 RETURNING id`,
		status,
		rejectReason,
		id, r.accountID,
	)

	var updatedID uuid.UUID
	if err := row.Scan(&updatedID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("postgres: update opportunity %s: %w", id, ErrNotFound)
		}
		return fmt.Errorf("postgres: update opportunity: %w", err)
	}
	return nil
}

func (r *OpportunityRepo) save(ctx context.Context, opportunity *domain.Opportunity, upsert bool) error {
	if err := validateOptionalPipelineRunRef(opportunity.PipelineRunID, opportunity.PipelineRunTradeDate); err != nil {
		return fmt.Errorf("postgres: save opportunity: %w", err)
	}
	if upsert && (opportunity.PipelineRunID == nil || opportunity.Environment == "" || strings.TrimSpace(opportunity.OriginType) == "" || strings.TrimSpace(opportunity.OriginID) == "" || opportunity.StrategyID == uuid.Nil) {
		return fmt.Errorf("postgres: save opportunity: complete dedupe scope is required")
	}
	if opportunity.AccountID != uuid.Nil && opportunity.AccountID != r.accountID {
		return fmt.Errorf("postgres: save opportunity: account mismatch")
	}
	opportunity.AccountID = r.accountID
	evidence, err := marshalOpportunityJSON(opportunity.Evidence)
	if err != nil {
		return err
	}

	query := `INSERT INTO portfolio_opportunities (
		account_id, environment, origin_type, origin_id, strategy_id, pipeline_run_id, pipeline_run_trade_date, market_type, ticker, side, prediction_side, signal, status, score, confidence,
		edge_pct, expected_return_pct, max_loss_pct, entry_price, liquidity_usd, market_cap_usd, spread_pct, proposed_notional,
		selected_notional, reason, reject_reason, evidence, expires_at, dedupe_key
	)
	VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29)`
	if upsert {
		query += ` ON CONFLICT (account_id,environment,origin_type,origin_id,pipeline_run_id,pipeline_run_trade_date,strategy_id,dedupe_key) DO UPDATE SET
			market_type = CASE WHEN portfolio_opportunities.status = 'queued' THEN EXCLUDED.market_type ELSE portfolio_opportunities.market_type END,
			ticker = CASE WHEN portfolio_opportunities.status = 'queued' THEN EXCLUDED.ticker ELSE portfolio_opportunities.ticker END,
			side = CASE WHEN portfolio_opportunities.status = 'queued' THEN EXCLUDED.side ELSE portfolio_opportunities.side END,
			prediction_side = CASE WHEN portfolio_opportunities.status = 'queued' THEN EXCLUDED.prediction_side ELSE portfolio_opportunities.prediction_side END,
			signal = CASE WHEN portfolio_opportunities.status = 'queued' THEN EXCLUDED.signal ELSE portfolio_opportunities.signal END,
			status = CASE WHEN portfolio_opportunities.status = 'queued' THEN EXCLUDED.status ELSE portfolio_opportunities.status END,
			score = CASE WHEN portfolio_opportunities.status = 'queued' THEN EXCLUDED.score ELSE portfolio_opportunities.score END,
			confidence = CASE WHEN portfolio_opportunities.status = 'queued' THEN EXCLUDED.confidence ELSE portfolio_opportunities.confidence END,
			edge_pct = CASE WHEN portfolio_opportunities.status = 'queued' THEN EXCLUDED.edge_pct ELSE portfolio_opportunities.edge_pct END,
			expected_return_pct = CASE WHEN portfolio_opportunities.status = 'queued' THEN EXCLUDED.expected_return_pct ELSE portfolio_opportunities.expected_return_pct END,
			max_loss_pct = CASE WHEN portfolio_opportunities.status = 'queued' THEN EXCLUDED.max_loss_pct ELSE portfolio_opportunities.max_loss_pct END,
			entry_price = CASE WHEN portfolio_opportunities.status = 'queued' THEN EXCLUDED.entry_price ELSE portfolio_opportunities.entry_price END,
			liquidity_usd = CASE WHEN portfolio_opportunities.status = 'queued' THEN EXCLUDED.liquidity_usd ELSE portfolio_opportunities.liquidity_usd END,
			market_cap_usd = CASE WHEN portfolio_opportunities.status = 'queued' THEN EXCLUDED.market_cap_usd ELSE portfolio_opportunities.market_cap_usd END,
			spread_pct = CASE WHEN portfolio_opportunities.status = 'queued' THEN EXCLUDED.spread_pct ELSE portfolio_opportunities.spread_pct END,
			proposed_notional = CASE WHEN portfolio_opportunities.status = 'queued' THEN EXCLUDED.proposed_notional ELSE portfolio_opportunities.proposed_notional END,
			selected_notional = CASE WHEN portfolio_opportunities.status = 'queued' THEN EXCLUDED.selected_notional ELSE portfolio_opportunities.selected_notional END,
			reason = CASE WHEN portfolio_opportunities.status = 'queued' THEN EXCLUDED.reason ELSE portfolio_opportunities.reason END,
			reject_reason = CASE WHEN portfolio_opportunities.status = 'queued' THEN EXCLUDED.reject_reason ELSE portfolio_opportunities.reject_reason END,
			evidence = CASE WHEN portfolio_opportunities.status = 'queued' THEN EXCLUDED.evidence ELSE portfolio_opportunities.evidence END,
			expires_at = CASE WHEN portfolio_opportunities.status = 'queued' THEN EXCLUDED.expires_at ELSE portfolio_opportunities.expires_at END,
			updated_at = CASE WHEN portfolio_opportunities.status = 'queued' THEN NOW() ELSE portfolio_opportunities.updated_at END
			WHERE portfolio_opportunities.account_id = EXCLUDED.account_id
			  AND portfolio_opportunities.environment IS NOT DISTINCT FROM EXCLUDED.environment
			  AND portfolio_opportunities.origin_type IS NOT DISTINCT FROM EXCLUDED.origin_type
			  AND portfolio_opportunities.origin_id IS NOT DISTINCT FROM EXCLUDED.origin_id
			  AND portfolio_opportunities.strategy_id = EXCLUDED.strategy_id
			  AND portfolio_opportunities.pipeline_run_id = EXCLUDED.pipeline_run_id
			  AND portfolio_opportunities.pipeline_run_trade_date = EXCLUDED.pipeline_run_trade_date`
	}
	query += ` RETURNING id, created_at, updated_at`

	row := r.pool.QueryRow(ctx, query,
		r.accountID,
		nullString(string(opportunity.Environment)),
		nullString(opportunity.OriginType),
		nullString(opportunity.OriginID),
		opportunity.StrategyID,
		opportunity.PipelineRunID,
		opportunity.PipelineRunTradeDate,
		opportunity.MarketType,
		opportunity.Ticker,
		opportunity.Side,
		opportunity.PredictionSide,
		opportunity.Signal,
		opportunity.Status,
		opportunity.Score,
		opportunity.Confidence,
		opportunity.EdgePct,
		opportunity.ExpectedReturnPct,
		opportunity.MaxLossPct,
		opportunity.EntryPrice,
		opportunity.LiquidityUSD,
		opportunity.MarketCapUSD,
		opportunity.SpreadPct,
		opportunity.ProposedNotional,
		opportunity.SelectedNotional,
		opportunity.Reason,
		opportunity.RejectReason,
		evidence,
		opportunity.ExpiresAt,
		opportunity.DedupeKey,
	)

	if err := row.Scan(&opportunity.ID, &opportunity.CreatedAt, &opportunity.UpdatedAt); err != nil {
		return fmt.Errorf("postgres: save opportunity: %w", err)
	}
	return nil
}

const opportunitySelectSQL = `SELECT id, account_id, environment, origin_type, origin_id, strategy_id, pipeline_run_id, pipeline_run_trade_date, market_type, ticker, side, prediction_side, signal,
	status, score::double precision, confidence::double precision, edge_pct::double precision,
	expected_return_pct::double precision, max_loss_pct::double precision, entry_price::double precision, liquidity_usd::double precision,
	market_cap_usd::double precision, spread_pct::double precision, proposed_notional::double precision, selected_notional::double precision,
	reason, reject_reason, evidence, expires_at, created_at, updated_at, dedupe_key
	FROM portfolio_opportunities`

func (r *OpportunityRepo) list(ctx context.Context, query string, args []any, op string) ([]domain.Opportunity, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: %s: %w", op, err)
	}
	defer rows.Close()

	var opportunities []domain.Opportunity
	for rows.Next() {
		opportunity, err := scanOpportunity(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: %s scan: %w", op, err)
		}
		opportunities = append(opportunities, *opportunity)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: %s rows: %w", op, err)
	}
	return opportunities, nil
}

func scanOpportunity(sc scanner) (*domain.Opportunity, error) {
	var (
		opportunity          domain.Opportunity
		accountID            *uuid.UUID
		environment          *domain.AccountEnvironment
		originType           *string
		originID             *string
		strategyID           uuid.UUID
		pipelineRunID        *uuid.UUID
		pipelineRunTradeDate *time.Time
		score                *float64
		evidence             []byte
	)
	if err := sc.Scan(
		&opportunity.ID,
		&accountID,
		&environment,
		&originType,
		&originID,
		&strategyID,
		&pipelineRunID,
		&pipelineRunTradeDate,
		&opportunity.MarketType,
		&opportunity.Ticker,
		&opportunity.Side,
		&opportunity.PredictionSide,
		&opportunity.Signal,
		&opportunity.Status,
		&score,
		&opportunity.Confidence,
		&opportunity.EdgePct,
		&opportunity.ExpectedReturnPct,
		&opportunity.MaxLossPct,
		&opportunity.EntryPrice,
		&opportunity.LiquidityUSD,
		&opportunity.MarketCapUSD,
		&opportunity.SpreadPct,
		&opportunity.ProposedNotional,
		&opportunity.SelectedNotional,
		&opportunity.Reason,
		&opportunity.RejectReason,
		&evidence,
		&opportunity.ExpiresAt,
		&opportunity.CreatedAt,
		&opportunity.UpdatedAt,
		&opportunity.DedupeKey,
	); err != nil {
		return nil, err
	}
	opportunity.StrategyID = strategyID
	if accountID != nil {
		opportunity.AccountID = *accountID
	}
	if environment != nil {
		opportunity.Environment = *environment
	}
	if originType != nil {
		opportunity.OriginType = *originType
	}
	if originID != nil {
		opportunity.OriginID = *originID
	}
	opportunity.PipelineRunID = pipelineRunID
	opportunity.PipelineRunTradeDate = pipelineRunTradeDate
	opportunity.Score = score
	opportunity.Evidence = json.RawMessage(evidence)
	return &opportunity, nil
}

func buildOpportunityCountQuery(accountID uuid.UUID, filter repository.OpportunityFilter) (string, []any) {
	query, args := buildOpportunityQuery(accountID, "SELECT COUNT(*) FROM portfolio_opportunities", filter, 0, 0, false)
	return query, args
}

func buildOpportunityListQuery(accountID uuid.UUID, filter repository.OpportunityFilter, limit, offset int) (string, []any) {
	return buildOpportunityQuery(accountID, opportunitySelectSQL, filter, limit, offset, true)
}

func buildOpportunityQuery(accountID uuid.UUID, base string, filter repository.OpportunityFilter, limit, offset int, includePagination bool) (string, []any) {
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

	if filter.Status != "" {
		conditions = append(conditions, "status = "+nextArg(filter.Status))
	}
	if filter.MarketType != "" {
		conditions = append(conditions, "market_type = "+nextArg(filter.MarketType.Normalize()))
	}
	if filter.StrategyID != nil {
		conditions = append(conditions, "strategy_id = "+nextArg(*filter.StrategyID))
	}
	if filter.Ticker != "" {
		conditions = append(conditions, "ticker = "+nextArg(filter.Ticker))
	}
	if filter.ExpiresBefore != nil {
		conditions = append(conditions, "expires_at <= "+nextArg(*filter.ExpiresBefore))
	}
	if filter.CreatedAfter != nil {
		conditions = append(conditions, "created_at >= "+nextArg(*filter.CreatedAfter))
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

func marshalOpportunityJSON(data json.RawMessage) ([]byte, error) {
	if len(data) == 0 {
		return []byte("{}"), nil
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("postgres: opportunity evidence is not valid")
	}
	return data, nil
}
