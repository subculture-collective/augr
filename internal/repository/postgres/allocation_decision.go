package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// AllocationDecisionRepo implements repository.AllocationDecisionRepository using PostgreSQL.
type AllocationDecisionRepo struct {
	pool      *pgxpool.Pool
	accountID uuid.UUID
}

var _ repository.AllocationDecisionRepository = (*AllocationDecisionRepo)(nil)

// NewAllocationDecisionRepo returns a repository backed by the given pool.
func NewAllocationDecisionRepo(pool *pgxpool.Pool, accountID uuid.UUID) *AllocationDecisionRepo {
	return &AllocationDecisionRepo{pool: pool, accountID: accountID}
}

// Create inserts a new allocation decision.
func (r *AllocationDecisionRepo) Create(ctx context.Context, decision *domain.AllocationDecision) error {
	if decision == nil || decision.OpportunityID == nil {
		return fmt.Errorf("postgres: create allocation decision: opportunity is required")
	}
	if err := validateOptionalPipelineRunRef(decision.PipelineRunID, decision.PipelineRunTradeDate); err != nil {
		return fmt.Errorf("postgres: create allocation decision: %w", err)
	}
	if decision.PipelineRunID == nil || decision.Environment == "" || strings.TrimSpace(decision.OriginType) == "" || strings.TrimSpace(decision.OriginID) == "" || decision.StrategyID == nil || *decision.StrategyID == uuid.Nil {
		return fmt.Errorf("postgres: create allocation decision: complete opportunity lineage is required")
	}
	if decision.AccountID != uuid.Nil && decision.AccountID != r.accountID {
		return fmt.Errorf("postgres: create allocation decision: account mismatch")
	}
	if decision.RiskPolicyVersion != "" && (decision.AccountSnapshotID == uuid.Nil || len(decision.RiskStateBytes) == 0 || digestBytes(decision.RiskStateBytes) != decision.RiskStateSHA256) {
		return fmt.Errorf("postgres: create allocation decision: complete canonical risk-state evidence is required")
	}
	decision.AccountID = r.accountID
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: create allocation decision: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row := tx.QueryRow(ctx,
		`WITH authorized AS (
			SELECT 1 FROM portfolio_opportunities o
			WHERE o.id=$5 AND o.account_id=$1
			  AND o.environment IS NOT DISTINCT FROM $2
			  AND o.origin_type IS NOT DISTINCT FROM $3
			  AND o.origin_id IS NOT DISTINCT FROM $4
			  AND o.pipeline_run_id IS NOT DISTINCT FROM $6
			  AND o.pipeline_run_trade_date IS NOT DISTINCT FROM $7
			  AND o.strategy_id IS NOT DISTINCT FROM $8
			  AND ($15::uuid IS NULL OR EXISTS (
				SELECT 1 FROM orders ord WHERE ord.id=$15 AND ord.account_id=$1 AND ord.allocation_opportunity_id=$5
				  AND ord.environment IS NOT DISTINCT FROM $2 AND ord.origin_type IS NOT DISTINCT FROM $3 AND ord.origin_id IS NOT DISTINCT FROM $4
				  AND ord.pipeline_run_id IS NOT DISTINCT FROM $6 AND ord.pipeline_run_trade_date IS NOT DISTINCT FROM $7
				  AND ord.strategy_id IS NOT DISTINCT FROM $8
			  ))
		), inserted AS (
			INSERT INTO allocation_decisions (
				account_id, environment, origin_type, origin_id, pipeline_run_id, pipeline_run_trade_date,
				opportunity_id, strategy_id, mode, action, score, notional_usd, quantity, reasons, created_order_id,
				risk_policy_id,risk_policy_version,account_snapshot_id,proposed_quantity,max_loss_per_unit,
				reserved_risk_usd,reserved_capital_usd,exposure_before_usd,exposure_after_usd,binding_constraint,execution_route,risk_state_sha256,risk_state_bytes
			)
			SELECT $1,$2,$3,$4,$6,$7,$5,$8,$9,$10,$11,$12,$13,$14,$15,
				(SELECT id FROM portfolio_risk_policy_artifacts WHERE schema_name||'@sha256:'||sha256=$17),NULLIF($17,''),$18,$19,$20,$21,$22,$23,$24,NULLIF($25,''),NULLIF($26,''),NULLIF($27,''),$28 FROM authorized
			WHERE $16::uuid IS NULL OR EXISTS (SELECT 1 FROM portfolio_opportunities claimed WHERE claimed.id=$5 AND claimed.account_id=$1 AND claimed.status='selected' AND claimed.allocation_claim_id=$16 AND claimed.allocation_claim_expires_at>clock_timestamp())
			ON CONFLICT (opportunity_id) WHERE opportunity_id IS NOT NULL AND account_id IS NOT NULL AND environment IS NOT NULL AND origin_type IS NOT NULL AND origin_id IS NOT NULL AND pipeline_run_id IS NOT NULL AND pipeline_run_trade_date IS NOT NULL DO NOTHING
			RETURNING id, created_at
		)
		SELECT id,created_at FROM inserted
		UNION ALL
		SELECT d.id,d.created_at FROM allocation_decisions d, authorized
		WHERE d.opportunity_id=$5 AND d.account_id=$1
		  AND d.environment IS NOT DISTINCT FROM $2
		  AND d.origin_type IS NOT DISTINCT FROM $3
		  AND d.origin_id IS NOT DISTINCT FROM $4
		  AND d.pipeline_run_id IS NOT DISTINCT FROM $6
		  AND d.pipeline_run_trade_date IS NOT DISTINCT FROM $7
		  AND d.strategy_id IS NOT DISTINCT FROM $8
		  AND d.mode IS NOT DISTINCT FROM $9
		  AND d.action IS NOT DISTINCT FROM $10
		  AND d.score IS NOT DISTINCT FROM $11
		  AND d.notional_usd IS NOT DISTINCT FROM $12
		  AND d.quantity IS NOT DISTINCT FROM $13
		  AND d.reasons IS NOT DISTINCT FROM $14
		  AND d.created_order_id IS NOT DISTINCT FROM $15
		  AND d.risk_policy_version IS NOT DISTINCT FROM NULLIF($17,'')
		  AND d.account_snapshot_id IS NOT DISTINCT FROM $18
		  AND d.proposed_quantity IS NOT DISTINCT FROM $19
		  AND d.max_loss_per_unit IS NOT DISTINCT FROM $20
		  AND d.reserved_risk_usd IS NOT DISTINCT FROM $21
		  AND d.reserved_capital_usd IS NOT DISTINCT FROM $22
		  AND d.exposure_before_usd IS NOT DISTINCT FROM $23
		  AND d.exposure_after_usd IS NOT DISTINCT FROM $24
		  AND d.binding_constraint IS NOT DISTINCT FROM NULLIF($25,'')
		  AND d.execution_route IS NOT DISTINCT FROM NULLIF($26,'')
		  AND d.risk_state_sha256 IS NOT DISTINCT FROM NULLIF($27,'')
		  AND d.risk_state_bytes IS NOT DISTINCT FROM $28
		LIMIT 1`,
		r.accountID, decision.Environment, decision.OriginType, decision.OriginID, decision.OpportunityID,
		decision.PipelineRunID, decision.PipelineRunTradeDate, decision.StrategyID,
		decision.Mode,
		decision.Action,
		decision.Score,
		decision.NotionalUSD,
		decision.Quantity,
		stringSliceOrEmpty(decision.Reasons),
		decision.CreatedOrderID,
		nullableUUIDValue(decision.ExecutionClaimID),
		decision.RiskPolicyVersion,
		nullableUUIDValue(decision.AccountSnapshotID),
		decision.ProposedQuantity,
		decision.MaxLossPerUnit,
		decision.ReservedRiskUSD,
		decision.ReservedCapitalUSD,
		decision.ExposureBeforeUSD,
		decision.ExposureAfterUSD,
		decision.BindingConstraint,
		decision.ExecutionRoute,
		decision.RiskStateSHA256,
		nullableJSON(decision.RiskStateBytes),
	)
	if err := row.Scan(&decision.ID, &decision.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("postgres: create allocation decision: immutable payload changed: %w", repository.ErrIdempotencyConflict)
		}
		return fmt.Errorf("postgres: create allocation decision: %w", err)
	}
	for _, cap := range decision.RiskCaps {
		command, insertErr := tx.Exec(ctx, `INSERT INTO allocation_risk_caps(decision_id,sequence,name,available_amount,unit_amount,quantity_cap,binding)
			VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(decision_id,sequence) DO NOTHING`, decision.ID, cap.Sequence, cap.Name,
			cap.AvailableAmount, cap.UnitAmount, cap.QuantityCap, cap.Binding)
		if insertErr != nil {
			return fmt.Errorf("postgres: create allocation decision risk cap: %w", insertErr)
		}
		if command.RowsAffected() == 0 {
			var persisted domain.AllocationRiskCap
			persisted.Sequence = cap.Sequence
			if scanErr := tx.QueryRow(ctx, `SELECT name,available_amount::double precision,unit_amount::double precision,quantity_cap::double precision,binding
				FROM allocation_risk_caps WHERE decision_id=$1 AND sequence=$2`, decision.ID, cap.Sequence).
				Scan(&persisted.Name, &persisted.AvailableAmount, &persisted.UnitAmount, &persisted.QuantityCap, &persisted.Binding); scanErr != nil || persisted != cap {
				return fmt.Errorf("postgres: create allocation decision risk cap changed: %w", repository.ErrIdempotencyConflict)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: create allocation decision commit: %w", err)
	}
	return nil
}

func nullableUUIDValue(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

// List returns allocation decisions matching the filter.
func (r *AllocationDecisionRepo) List(ctx context.Context, filter repository.AllocationDecisionFilter, limit, offset int) ([]domain.AllocationDecision, error) {
	query, args := buildAllocationDecisionListQuery(r.accountID, filter, limit, offset)
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: list allocation decisions: %w", err)
	}
	defer rows.Close()

	var decisions []domain.AllocationDecision
	for rows.Next() {
		decision, err := scanAllocationDecision(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: list allocation decisions scan: %w", err)
		}
		decisions = append(decisions, *decision)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list allocation decisions rows: %w", err)
	}
	rows.Close()
	for index := range decisions {
		if err := r.loadRiskCaps(ctx, &decisions[index]); err != nil {
			return nil, fmt.Errorf("postgres: list allocation decision risk caps: %w", err)
		}
	}
	return decisions, nil
}

// Count returns the total number of allocation decisions matching the filter.
func (r *AllocationDecisionRepo) Count(ctx context.Context, filter repository.AllocationDecisionFilter) (int, error) {
	query, args := buildAllocationDecisionCountQuery(r.accountID, filter)
	var total int
	if err := r.pool.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("postgres: count allocation decisions: %w", err)
	}
	return total, nil
}

// RecordPaperOrderResult attaches the durable effect and resolves it only when terminal.
func (r *AllocationDecisionRepo) RecordPaperOrderResult(ctx context.Context, id, claimID uuid.UUID, orderID *uuid.UUID, action domain.AllocationDecisionAction, reasons []string) (bool, error) {
	if action != domain.AllocationDecisionActionPaperOrderIntent && action != domain.AllocationDecisionActionExecuted && action != domain.AllocationDecisionActionExecutionRejected {
		return false, fmt.Errorf("postgres: record paper order result: paper action required")
	}
	tag, err := r.pool.Exec(ctx, `UPDATE allocation_decisions d SET action=$1, reasons=$2, created_order_id=COALESCE(d.created_order_id,$3)
		WHERE d.id=$4 AND d.account_id=$5 AND d.action=$6 AND (d.created_order_id IS NULL OR d.created_order_id=$3)
		  AND EXISTS (SELECT 1 FROM portfolio_opportunities claimed WHERE claimed.id=d.opportunity_id AND claimed.account_id=d.account_id AND claimed.status='selected' AND claimed.allocation_claim_id=$7 AND claimed.allocation_claim_expires_at>clock_timestamp())
		  AND (($3::uuid IS NULL AND $1 IN ('paper_order_intent','execution_rejected')) OR ($3::uuid IS NOT NULL AND EXISTS (
			SELECT 1 FROM orders o
			WHERE o.id=$3 AND o.account_id=d.account_id AND o.allocation_opportunity_id=d.opportunity_id
			  AND o.environment IS NOT DISTINCT FROM d.environment AND o.origin_type IS NOT DISTINCT FROM d.origin_type AND o.origin_id IS NOT DISTINCT FROM d.origin_id
			  AND o.pipeline_run_id IS NOT DISTINCT FROM d.pipeline_run_id AND o.pipeline_run_trade_date IS NOT DISTINCT FROM d.pipeline_run_trade_date
			  AND o.strategy_id IS NOT DISTINCT FROM d.strategy_id
		  )))`, action, stringSliceOrEmpty(reasons), orderID, id, r.accountID, domain.AllocationDecisionActionPaperOrderIntent, claimID)
	if err != nil {
		return false, fmt.Errorf("postgres: record paper order result: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

const allocationDecisionSelectSQL = `SELECT id, account_id, environment, origin_type, origin_id, pipeline_run_id, pipeline_run_trade_date, opportunity_id, strategy_id, mode, action,
	score::double precision, notional_usd::double precision, quantity::double precision,
	reasons, created_order_id, created_at,risk_policy_version,account_snapshot_id,proposed_quantity::double precision,
	max_loss_per_unit::double precision,reserved_risk_usd::double precision,reserved_capital_usd::double precision,
	exposure_before_usd::double precision,exposure_after_usd::double precision,binding_constraint,execution_route,risk_state_sha256,risk_state_bytes
	FROM allocation_decisions`

func scanAllocationDecision(sc scanner) (*domain.AllocationDecision, error) {
	var decision domain.AllocationDecision
	var riskPolicyVersion, bindingConstraint, executionRoute, riskStateSHA256 *string
	var riskStateBytes []byte
	var accountSnapshotID *uuid.UUID
	var proposedQuantity, maxLossPerUnit, reservedRisk, reservedCapital, exposureBefore, exposureAfter *float64
	if err := sc.Scan(
		&decision.ID,
		&decision.AccountID, &decision.Environment, &decision.OriginType, &decision.OriginID,
		&decision.PipelineRunID, &decision.PipelineRunTradeDate,
		&decision.OpportunityID,
		&decision.StrategyID,
		&decision.Mode,
		&decision.Action,
		&decision.Score,
		&decision.NotionalUSD,
		&decision.Quantity,
		&decision.Reasons,
		&decision.CreatedOrderID,
		&decision.CreatedAt,
		&riskPolicyVersion,
		&accountSnapshotID,
		&proposedQuantity,
		&maxLossPerUnit,
		&reservedRisk,
		&reservedCapital,
		&exposureBefore,
		&exposureAfter,
		&bindingConstraint,
		&executionRoute,
		&riskStateSHA256,
		&riskStateBytes,
	); err != nil {
		return nil, err
	}
	if riskPolicyVersion != nil {
		decision.RiskPolicyVersion = *riskPolicyVersion
	}
	if accountSnapshotID != nil {
		decision.AccountSnapshotID = *accountSnapshotID
	}
	decision.ProposedQuantity = float64Value(proposedQuantity)
	decision.MaxLossPerUnit = float64Value(maxLossPerUnit)
	decision.ReservedRiskUSD = float64Value(reservedRisk)
	decision.ReservedCapitalUSD = float64Value(reservedCapital)
	decision.ExposureBeforeUSD = float64Value(exposureBefore)
	decision.ExposureAfterUSD = float64Value(exposureAfter)
	if bindingConstraint != nil {
		decision.BindingConstraint = *bindingConstraint
	}
	if executionRoute != nil {
		decision.ExecutionRoute = *executionRoute
	}
	if riskStateSHA256 != nil {
		decision.RiskStateSHA256 = *riskStateSHA256
		decision.RiskStateBytes = append([]byte(nil), riskStateBytes...)
	}
	return &decision, nil
}

func (r *AllocationDecisionRepo) loadRiskCaps(ctx context.Context, decision *domain.AllocationDecision) error {
	rows, err := r.pool.Query(ctx, `SELECT sequence,name,available_amount::double precision,unit_amount::double precision,quantity_cap::double precision,binding
		FROM allocation_risk_caps WHERE decision_id=$1 ORDER BY sequence`, decision.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var riskCap domain.AllocationRiskCap
		if err := rows.Scan(&riskCap.Sequence, &riskCap.Name, &riskCap.AvailableAmount, &riskCap.UnitAmount, &riskCap.QuantityCap, &riskCap.Binding); err != nil {
			return err
		}
		decision.RiskCaps = append(decision.RiskCaps, riskCap)
	}
	return rows.Err()
}

func buildAllocationDecisionCountQuery(accountID uuid.UUID, filter repository.AllocationDecisionFilter) (string, []any) {
	query, args := buildAllocationDecisionQuery(accountID, "SELECT COUNT(*) FROM allocation_decisions", filter, 0, 0, false)
	return query, args
}

func buildAllocationDecisionListQuery(accountID uuid.UUID, filter repository.AllocationDecisionFilter, limit, offset int) (string, []any) {
	return buildAllocationDecisionQuery(accountID, allocationDecisionSelectSQL, filter, limit, offset, true)
}

func buildAllocationDecisionQuery(accountID uuid.UUID, base string, filter repository.AllocationDecisionFilter, limit, offset int, includePagination bool) (string, []any) {
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

	if filter.Mode != "" {
		conditions = append(conditions, "mode = "+nextArg(filter.Mode))
	}
	if filter.Action != "" {
		conditions = append(conditions, "action = "+nextArg(filter.Action))
	}
	if filter.StrategyID != nil {
		conditions = append(conditions, "strategy_id = "+nextArg(*filter.StrategyID))
	}
	if filter.OpportunityID != nil {
		conditions = append(conditions, "opportunity_id = "+nextArg(*filter.OpportunityID))
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
