package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/copyorigin"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type CopyOriginRepo struct {
	pool                *pgxpool.Pool
	afterStage          func(string) error
	createPlannedIntent func(context.Context, pgx.Tx, domain.CopyTradeIntent) (domain.CopyTradeIntent, error)
}

var _ copyorigin.Store = (*CopyOriginRepo)(nil)
var _ copyorigin.PlannedStore = (*CopyOriginRepo)(nil)

func NewCopyOriginRepo(pool *pgxpool.Pool) *CopyOriginRepo { return &CopyOriginRepo{pool: pool} }

type copyOriginEnvelope struct {
	Schema              string            `json:"schema"`
	State               string            `json:"state"`
	SubscriptionID      string            `json:"subscription_id"`
	OriginType          string            `json:"origin_type"`
	OriginID            string            `json:"origin_id"`
	SourceObservationID string            `json:"source_observation_id"`
	CalculationVersion  int               `json:"calculation_version"`
	Intents             []json.RawMessage `json:"intents"`
}

type copyOriginIntentEnvelope struct {
	ID                  string `json:"id"`
	InstrumentKey       string `json:"instrument_key"`
	SourceObservationID string `json:"source_observation_id"`
}

func (r *CopyOriginRepo) RegisterRun(ctx context.Context, run *copyorigin.Run) (*copyorigin.Run, error) {
	if r == nil || r.pool == nil || run == nil {
		return nil, fmt.Errorf("postgres: copy origin run is required")
	}
	var envelope copyOriginEnvelope
	if err := json.Unmarshal(run.CanonicalBytes(), &envelope); err != nil {
		return nil, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `INSERT INTO copy_origin_rebalance_runs(id,schema_name,state,subscription_id,origin_type,origin_id,source_observation_id,calculation_version,intent_count,sha256,canonical_bytes,canonical_json) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,convert_from($11,'UTF8')::jsonb) ON CONFLICT(id) DO NOTHING`, run.ID(), envelope.Schema, envelope.State, envelope.SubscriptionID, envelope.OriginType, envelope.OriginID, envelope.SourceObservationID, envelope.CalculationVersion, len(envelope.Intents), run.Digest(), run.CanonicalBytes())
	if err != nil {
		return nil, fmt.Errorf("postgres: insert copy origin run: %w", err)
	}
	if r.afterStage != nil {
		if err = r.afterStage("run"); err != nil {
			return nil, err
		}
	}
	for sequence, raw := range envelope.Intents {
		var intent copyOriginIntentEnvelope
		if err = json.Unmarshal(raw, &intent); err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO copy_origin_rebalance_intents(run_id,sequence,intent_id,instrument_key,source_observation_id,canonical_intent) VALUES($1,$2,$3,$4,$5,$6::jsonb) ON CONFLICT(run_id,sequence) DO NOTHING`, run.ID(), sequence, intent.ID, intent.InstrumentKey, intent.SourceObservationID, string(raw))
		if err != nil {
			return nil, fmt.Errorf("postgres: insert copy origin run intent: %w", err)
		}
		if r.afterStage != nil {
			if err = r.afterStage("intent"); err != nil {
				return nil, err
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	got, err := r.GetRun(ctx, run.ID())
	if err != nil {
		return nil, err
	}
	if got.Digest() != run.Digest() || !bytes.Equal(got.CanonicalBytes(), run.CanonicalBytes()) {
		return nil, fmt.Errorf("postgres: copy origin run conflict: %w", repository.ErrIdempotencyConflict)
	}
	return got, nil
}

// RegisterPlannedRun atomically registers run attribution and the exact copy
// intents that may subsequently produce orders.
func (r *CopyOriginRepo) RegisterPlannedRun(ctx context.Context, run *copyorigin.Run, intents []domain.CopyTradeIntent) (*copyorigin.Run, []domain.CopyTradeIntent, error) {
	if r == nil || r.pool == nil || run == nil {
		return nil, nil, fmt.Errorf("postgres: copy origin run is required")
	}
	var envelope copyOriginEnvelope
	if err := json.Unmarshal(run.CanonicalBytes(), &envelope); err != nil {
		return nil, nil, err
	}
	if err := validatePlannedIntents(envelope, intents); err != nil {
		return nil, nil, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	canonical := make([]domain.CopyTradeIntent, len(intents))
	for i := range intents {
		createIntent := createCopyIntentTx
		if r.createPlannedIntent != nil {
			createIntent = r.createPlannedIntent
		}
		canonical[i], err = createIntent(ctx, tx, intents[i])
		if err != nil {
			return nil, nil, fmt.Errorf("postgres: register planned copy intent: %w", err)
		}
		if r.afterStage != nil {
			if err = r.afterStage("planned_intent"); err != nil {
				return nil, nil, err
			}
		}
	}

	tag, err := tx.Exec(ctx, `INSERT INTO copy_origin_rebalance_runs(id,schema_name,state,subscription_id,origin_type,origin_id,source_observation_id,calculation_version,intent_count,sha256,canonical_bytes,canonical_json) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,convert_from($11,'UTF8')::jsonb) ON CONFLICT(id) DO NOTHING`, run.ID(), envelope.Schema, envelope.State, envelope.SubscriptionID, envelope.OriginType, envelope.OriginID, envelope.SourceObservationID, envelope.CalculationVersion, len(envelope.Intents), run.Digest(), run.CanonicalBytes())
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: insert copy origin run: %w", err)
	}
	if r.afterStage != nil {
		if err = r.afterStage("run"); err != nil {
			return nil, nil, err
		}
	}
	if tag.RowsAffected() == 0 {
		var digest string
		var raw []byte
		if loadErr := tx.QueryRow(ctx, `SELECT sha256,canonical_bytes FROM copy_origin_rebalance_runs WHERE id=$1`, run.ID()).Scan(&digest, &raw); loadErr != nil {
			return nil, nil, loadErr
		}
		if digest != run.Digest() || !bytes.Equal(raw, run.CanonicalBytes()) {
			return nil, nil, fmt.Errorf("postgres: copy origin run conflict: %w", repository.ErrIdempotencyConflict)
		}
	}
	for sequence, raw := range envelope.Intents {
		var intent copyOriginIntentEnvelope
		if err = json.Unmarshal(raw, &intent); err != nil {
			return nil, nil, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO copy_origin_rebalance_intents(run_id,sequence,intent_id,instrument_key,source_observation_id,canonical_intent) VALUES($1,$2,$3,$4,$5,$6::jsonb) ON CONFLICT(run_id,sequence) DO NOTHING`, run.ID(), sequence, intent.ID, intent.InstrumentKey, intent.SourceObservationID, string(raw))
		if err != nil {
			return nil, nil, fmt.Errorf("postgres: insert copy origin run intent: %w", err)
		}
		if r.afterStage != nil {
			if err = r.afterStage("intent"); err != nil {
				return nil, nil, err
			}
		}
	}
	registered, err := getCopyOriginRun(ctx, tx, run.ID())
	if err != nil {
		return nil, nil, err
	}
	if registered.Digest() != run.Digest() || !bytes.Equal(registered.CanonicalBytes(), run.CanonicalBytes()) {
		return nil, nil, fmt.Errorf("postgres: copy origin run conflict: %w", repository.ErrIdempotencyConflict)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return registered, canonical, nil
}

func validatePlannedIntents(envelope copyOriginEnvelope, intents []domain.CopyTradeIntent) error {
	if len(intents) != len(envelope.Intents) {
		return fmt.Errorf("postgres: planned copy intents do not match run")
	}
	want := make(map[uuid.UUID]copyOriginIntentEnvelope, len(envelope.Intents))
	for _, raw := range envelope.Intents {
		var value copyOriginIntentEnvelope
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		id, err := uuid.Parse(value.ID)
		if err != nil {
			return err
		}
		want[id] = value
	}
	for _, intent := range intents {
		value, ok := want[intent.ID]
		if !ok || value.InstrumentKey != intent.InstrumentKey || value.SourceObservationID != intent.SourceObservationID.String() || intent.SubscriptionID.String() != envelope.SubscriptionID || intent.OriginType != envelope.OriginType || intent.OriginID.String() != envelope.OriginID || intent.CalculationVersion != envelope.CalculationVersion {
			return fmt.Errorf("postgres: planned copy intents do not match run")
		}
	}
	return nil
}

func createCopyIntentTx(ctx context.Context, tx pgx.Tx, value domain.CopyTradeIntent) (domain.CopyTradeIntent, error) {
	intent := &value
	if intent.Calculation == nil {
		intent.Calculation = json.RawMessage(`{}`)
	}
	if intent.PolicyReasons == nil {
		intent.PolicyReasons = []string{}
	}
	if intent.RiskReasons == nil {
		intent.RiskReasons = []string{}
	}
	err := tx.QueryRow(ctx, `INSERT INTO copy_trade_intents (id,subscription_id,origin_type,origin_id,source_observation_id,pipeline_run_id,instrument_key,ticker,side,target_weight,target_value,attributed_current_value,requested_notional,executable_price,quote_gate_version,decision_quote_snapshot_id,decision_bid,decision_ask,decision_spread_bps,decision_available_at,decision_at,decision_market_status,decision_session_status,calculation_version,calculation,policy_status,policy_reasons,risk_status,risk_reasons,order_id,status) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31) ON CONFLICT (subscription_id,source_observation_id,instrument_key,calculation_version) DO NOTHING RETURNING created_at,updated_at`, intent.ID, intent.SubscriptionID, intent.OriginType, intent.OriginID, intent.SourceObservationID, intent.PipelineRunID, intent.InstrumentKey, intent.Ticker, intent.Side, intent.TargetWeight, intent.TargetValue, intent.AttributedCurrentValue, intent.RequestedNotional, intent.ExecutablePrice, intent.QuoteGateVersion, intent.DecisionQuoteSnapshotID, nullIfEmpty(intent.DecisionBid), nullIfEmpty(intent.DecisionAsk), nullIfEmpty(intent.DecisionSpreadBPS), intent.DecisionAvailableAt, intent.DecisionAt, nullIfEmpty(intent.DecisionMarketStatus), nullIfEmpty(intent.DecisionSessionStatus), intent.CalculationVersion, intent.Calculation, intent.PolicyStatus, intent.PolicyReasons, intent.RiskStatus, intent.RiskReasons, intent.OrderID, intent.Status).Scan(&intent.CreatedAt, &intent.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := scanCopyIntent(tx.QueryRow(ctx, copyIntentSelect+` WHERE subscription_id=$1 AND source_observation_id=$2 AND instrument_key=$3 AND calculation_version=$4`, intent.SubscriptionID, intent.SourceObservationID, intent.InstrumentKey, intent.CalculationVersion))
		if loadErr != nil {
			return domain.CopyTradeIntent{}, loadErr
		}
		if !sameCopyIntentCreation(existing, intent) {
			return domain.CopyTradeIntent{}, fmt.Errorf("postgres: copy intent retry changed immutable evidence: %w", repository.ErrIdempotencyConflict)
		}
		return *existing, nil
	}
	if err != nil {
		return domain.CopyTradeIntent{}, err
	}
	return *intent, nil
}

func (r *CopyOriginRepo) GetRun(ctx context.Context, id uuid.UUID) (*copyorigin.Run, error) {
	if r == nil || r.pool == nil || id == uuid.Nil {
		return nil, fmt.Errorf("postgres: copy origin run identity is required")
	}
	return getCopyOriginRun(ctx, r.pool, id)
}

type copyOriginQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func getCopyOriginRun(ctx context.Context, query copyOriginQuerier, id uuid.UUID) (*copyorigin.Run, error) {
	var digest string
	var raw []byte
	err := query.QueryRow(ctx, `SELECT sha256,canonical_bytes FROM copy_origin_rebalance_runs WHERE id=$1`, id).Scan(&digest, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var envelope copyOriginEnvelope
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	rows, err := query.Query(ctx, `SELECT canonical_intent FROM copy_origin_rebalance_intents WHERE run_id=$1 ORDER BY sequence`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	index := 0
	for rows.Next() {
		var child []byte
		if rows.Scan(&child) != nil || index >= len(envelope.Intents) || !jsonEqual(child, envelope.Intents[index]) {
			return nil, fmt.Errorf("postgres: normalized copy origin run does not reconstruct")
		}
		index++
	}
	if index != len(envelope.Intents) {
		return nil, fmt.Errorf("postgres: normalized copy origin run does not reconstruct")
	}
	return copyorigin.FromCanonical(id, digest, raw)
}
