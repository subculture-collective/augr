package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// AgentDecisionRepo implements repository.AgentDecisionRepository using PostgreSQL.
type AgentDecisionRepo struct {
	pool      *pgxpool.Pool
	accountID uuid.UUID
}

// Compile-time check that AgentDecisionRepo satisfies AgentDecisionRepository.
var _ repository.AgentDecisionRepository = (*AgentDecisionRepo)(nil)

// NewAgentDecisionRepo returns an AgentDecisionRepo backed by the given connection
// pool.
func NewAgentDecisionRepo(pool *pgxpool.Pool, accountID uuid.UUID) *AgentDecisionRepo {
	return &AgentDecisionRepo{pool: pool, accountID: accountID}
}

// Create inserts a new agent decision and populates the generated ID and
// CreatedAt on the provided struct.
func (r *AgentDecisionRepo) Create(ctx context.Context, decision *domain.AgentDecision) error {
	if decision.AccountID != uuid.Nil && decision.AccountID != r.accountID {
		return fmt.Errorf("postgres: create agent decision: account mismatch")
	}
	decision.AccountID = r.accountID
	outputStructured, err := marshalOutputStructured(decision.OutputStructured)
	if err != nil {
		return err
	}

	row := r.pool.QueryRow(ctx,
		`INSERT INTO agent_decisions (
			account_id, environment, origin_type, origin_id, pipeline_run_id, pipeline_run_trade_date, agent_role, phase, round_number, input_summary,
			output_text, output_structured, llm_provider, llm_model,
			prompt_text, prompt_tokens, completion_tokens, latency_ms, cost_usd
		)
		 SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18 FROM pipeline_runs run
		 WHERE run.id=$5 AND run.trade_date=$6::date AND run.account_id=$1
		 RETURNING id, created_at`,
		r.accountID, decision.Environment, decision.OriginType, decision.OriginID, decision.PipelineRunID, decision.PipelineRunTradeDate,
		decision.AgentRole,
		decision.Phase,
		decision.RoundNumber,
		decision.InputSummary,
		decision.OutputText,
		outputStructured,
		decision.LLMProvider,
		decision.LLMModel,
		decision.PromptText,
		decision.PromptTokens,
		decision.CompletionTokens,
		decision.LatencyMS,
		decision.CostUSD,
	)

	if err := row.Scan(&decision.ID, &decision.CreatedAt); err != nil {
		return fmt.Errorf("postgres: create agent decision: %w", err)
	}

	return nil
}

// GetByRun returns agent decisions for the given pipeline run, with optional
// filtering and pagination. Results are ordered by phase, round number, then
// creation time to satisfy the audit-trail ordering requirement.
func (r *AgentDecisionRepo) GetByRun(ctx context.Context, ref domain.PipelineRunRef, filter repository.AgentDecisionFilter, limit, offset int) ([]domain.AgentDecision, error) {
	query, args := buildGetByRunQuery(r.accountID, ref, filter, limit, offset)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: get agent decisions by run: %w", err)
	}
	defer rows.Close()

	var decisions []domain.AgentDecision
	for rows.Next() {
		d, err := scanAgentDecision(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: get agent decisions by run scan: %w", err)
		}
		decisions = append(decisions, *d)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: get agent decisions by run rows: %w", err)
	}

	return decisions, nil
}

// CountByRun returns the total number of agent decisions for the given run
// matching the filter.
func (r *AgentDecisionRepo) CountByRun(ctx context.Context, ref domain.PipelineRunRef, filter repository.AgentDecisionFilter) (int, error) {
	query, args := buildCountByRunQuery(r.accountID, ref, filter)
	var total int
	if err := r.pool.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("postgres: count agent decisions by run: %w", err)
	}
	return total, nil
}

func buildCountByRunQuery(accountID uuid.UUID, ref domain.PipelineRunRef, filter repository.AgentDecisionFilter) (string, []any) {
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
	conditions = append(conditions, "account_id = "+nextArg(accountID), "pipeline_run_id = "+nextArg(ref.ID), "pipeline_run_trade_date = "+nextArg(ref.TradeDate)+"::date")
	if filter.AgentRole != "" {
		conditions = append(conditions, "agent_role = "+nextArg(filter.AgentRole))
	}
	if filter.Phase != "" {
		conditions = append(conditions, "phase = "+nextArg(filter.Phase))
	}
	if filter.RoundNumber != nil {
		conditions = append(conditions, "round_number = "+nextArg(*filter.RoundNumber))
	}
	return `SELECT COUNT(*) FROM agent_decisions WHERE ` + strings.Join(conditions, " AND "), args
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// scanAgentDecision scans a single row (pgx.Row or pgx.Rows) into an
// AgentDecision. Nullable columns are scanned via pointer intermediates and
// converted to the Go zero value when NULL.
func scanAgentDecision(sc scanner) (*domain.AgentDecision, error) {
	var (
		d                    domain.AgentDecision
		inputSummary         *string
		outputStructuredJSON []byte
		llmProvider          *string
		llmModel             *string
		promptText           *string
		promptTokens         *int
		completionTokens     *int
		latencyMS            *int
		costUSD              *float64
	)

	err := sc.Scan(
		&d.ID,
		&d.AccountID, &d.Environment, &d.OriginType, &d.OriginID,
		&d.PipelineRunID,
		&d.PipelineRunTradeDate,
		&d.AgentRole,
		&d.Phase,
		&d.RoundNumber,
		&inputSummary,
		&d.OutputText,
		&outputStructuredJSON,
		&llmProvider,
		&llmModel,
		&promptText,
		&promptTokens,
		&completionTokens,
		&latencyMS,
		&costUSD,
		&d.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	if inputSummary != nil {
		d.InputSummary = *inputSummary
	}
	if outputStructuredJSON != nil {
		d.OutputStructured = json.RawMessage(outputStructuredJSON)
	}
	if llmProvider != nil {
		d.LLMProvider = *llmProvider
	}
	if llmModel != nil {
		d.LLMModel = *llmModel
	}
	if promptText != nil {
		d.PromptText = *promptText
	}
	if promptTokens != nil {
		d.PromptTokens = *promptTokens
	}
	if completionTokens != nil {
		d.CompletionTokens = *completionTokens
	}
	if latencyMS != nil {
		d.LatencyMS = *latencyMS
	}
	if costUSD != nil {
		d.CostUSD = *costUSD
	}

	return &d, nil
}

// buildGetByRunQuery constructs the SELECT query and arguments for GetByRun
// with dynamic WHERE conditions. All values are parameterized. runID is always
// included as a condition; filter fields narrow the result further.
func buildGetByRunQuery(accountID uuid.UUID, ref domain.PipelineRunRef, filter repository.AgentDecisionFilter, limit, offset int) (string, []any) {
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

	conditions = append(conditions, "account_id = "+nextArg(accountID), "pipeline_run_id = "+nextArg(ref.ID), "pipeline_run_trade_date = "+nextArg(ref.TradeDate)+"::date")

	if filter.AgentRole != "" {
		conditions = append(conditions, "agent_role = "+nextArg(filter.AgentRole))
	}

	if filter.Phase != "" {
		conditions = append(conditions, "phase = "+nextArg(filter.Phase))
	}

	if filter.RoundNumber != nil {
		conditions = append(conditions, "round_number = "+nextArg(*filter.RoundNumber))
	}

	base := `SELECT id, account_id, environment, origin_type, origin_id, pipeline_run_id, pipeline_run_trade_date, agent_role, phase, round_number, input_summary,
		 output_text, output_structured, llm_provider, llm_model, prompt_text,
		 prompt_tokens, completion_tokens, latency_ms, cost_usd, created_at
		 FROM agent_decisions`

	base += " WHERE " + strings.Join(conditions, " AND ")
	base += " ORDER BY phase, round_number NULLS LAST, created_at, id"
	base += fmt.Sprintf(" LIMIT %s OFFSET %s", nextArg(limit), nextArg(offset))

	return base, args
}

// marshalOutputStructured ensures the output_structured JSONB value is valid
// JSON. A nil or empty value is stored as SQL NULL.
func marshalOutputStructured(data json.RawMessage) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}

	if !json.Valid(data) {
		return nil, fmt.Errorf("postgres: agent decision output_structured is not valid JSON")
	}

	return data, nil
}
