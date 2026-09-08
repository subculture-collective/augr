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
	createPlannedIntent func(context.Context, pgx.Tx, domain.CopyTradeIntent) (copyorigin.PlannedIntent, error)
}

var (
	_ copyorigin.Store         = (*CopyOriginRepo)(nil)
	_ copyorigin.PlannedStore  = (*CopyOriginRepo)(nil)
	_ copyorigin.RecoveryStore = (*CopyOriginRepo)(nil)
)

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
func (r *CopyOriginRepo) RegisterPlannedRun(ctx context.Context, run *copyorigin.Run, intents []domain.CopyTradeIntent) (*copyorigin.Run, []copyorigin.PlannedIntent, error) {
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
	var accountID uuid.UUID
	var environment domain.AccountEnvironment
	var subscriptionOriginType string
	var subscriptionOriginID uuid.UUID
	var subscriptionStatus domain.CopySubscriptionStatus
	var isPaper bool
	if err = tx.QueryRow(ctx, `SELECT account_id,environment,origin_type,origin_id,status,is_paper FROM copy_subscriptions WHERE id=$1 FOR UPDATE`, envelope.SubscriptionID).Scan(&accountID, &environment, &subscriptionOriginType, &subscriptionOriginID, &subscriptionStatus, &isPaper); err != nil {
		return nil, nil, fmt.Errorf("postgres: lock copy subscription scope: %w", err)
	}
	if accountID == uuid.Nil || !environment.IsValid() || subscriptionOriginType != envelope.OriginType || subscriptionOriginID.String() != envelope.OriginID || subscriptionStatus != domain.CopySubscriptionPaperActive || !isPaper {
		return nil, nil, fmt.Errorf("postgres: copy subscription scope is invalid")
	}

	canonical := make([]copyorigin.PlannedIntent, len(intents))
	for i := range intents {
		intents[i].AccountID = accountID
		intents[i].Environment = environment
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

	tag, err := tx.Exec(ctx, `INSERT INTO copy_origin_rebalance_runs(id,account_id,environment,schema_name,state,subscription_id,origin_type,origin_id,source_observation_id,calculation_version,intent_count,sha256,canonical_bytes,canonical_json) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,convert_from($13,'UTF8')::jsonb) ON CONFLICT(id) DO NOTHING`, run.ID(), accountID, environment, envelope.Schema, envelope.State, envelope.SubscriptionID, envelope.OriginType, envelope.OriginID, envelope.SourceObservationID, envelope.CalculationVersion, len(envelope.Intents), run.Digest(), run.CanonicalBytes())
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
		_, err = tx.Exec(ctx, `INSERT INTO copy_origin_rebalance_intents(run_id,sequence,intent_id,account_id,environment,origin_type,origin_id,instrument_key,source_observation_id,canonical_intent) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb) ON CONFLICT(run_id,sequence) DO NOTHING`, run.ID(), sequence, intent.ID, accountID, environment, envelope.OriginType, envelope.OriginID, intent.InstrumentKey, intent.SourceObservationID, string(raw))
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

func createCopyIntentTx(ctx context.Context, tx pgx.Tx, value domain.CopyTradeIntent) (copyorigin.PlannedIntent, error) {
	intent := &value
	if err := canonicalizeCopyIntentNumerics(intent); err != nil {
		return copyorigin.PlannedIntent{}, err
	}
	if intent.Calculation == nil {
		intent.Calculation = json.RawMessage(`{}`)
	}
	if intent.PolicyReasons == nil {
		intent.PolicyReasons = []string{}
	}
	if intent.RiskReasons == nil {
		intent.RiskReasons = []string{}
	}
	err := tx.QueryRow(ctx, `INSERT INTO copy_trade_intents (id,account_id,environment,subscription_id,origin_type,origin_id,source_observation_id,pipeline_run_id,pipeline_run_trade_date,instrument_key,ticker,side,target_weight,target_value,attributed_current_value,requested_notional,executable_price,quote_gate_version,decision_quote_snapshot_id,decision_bid,decision_ask,decision_spread_bps,decision_available_at,decision_at,decision_market_status,decision_session_status,calculation_version,calculation,policy_status,policy_reasons,risk_status,risk_reasons,order_id,status) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34) ON CONFLICT (subscription_id,source_observation_id,instrument_key,calculation_version) DO NOTHING RETURNING created_at,updated_at`, intent.ID, nullableUUID(intent.AccountID), nullString(string(intent.Environment)), intent.SubscriptionID, intent.OriginType, intent.OriginID, intent.SourceObservationID, intent.PipelineRunID, intent.PipelineRunTradeDate, intent.InstrumentKey, intent.Ticker, intent.Side, intent.TargetWeight, intent.TargetValue, intent.AttributedCurrentValue, intent.RequestedNotional, intent.ExecutablePrice, intent.QuoteGateVersion, intent.DecisionQuoteSnapshotID, nullIfEmpty(intent.DecisionBid), nullIfEmpty(intent.DecisionAsk), nullIfEmpty(intent.DecisionSpreadBPS), intent.DecisionAvailableAt, intent.DecisionAt, nullIfEmpty(intent.DecisionMarketStatus), nullIfEmpty(intent.DecisionSessionStatus), intent.CalculationVersion, intent.Calculation, intent.PolicyStatus, intent.PolicyReasons, intent.RiskStatus, intent.RiskReasons, intent.OrderID, intent.Status).Scan(&intent.CreatedAt, &intent.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := scanCopyIntent(tx.QueryRow(ctx, copyIntentSelect+` WHERE subscription_id=$1 AND source_observation_id=$2 AND instrument_key=$3 AND calculation_version=$4`, intent.SubscriptionID, intent.SourceObservationID, intent.InstrumentKey, intent.CalculationVersion))
		if loadErr != nil {
			return copyorigin.PlannedIntent{}, loadErr
		}
		if !sameCopyIntentCreation(existing, intent) {
			return copyorigin.PlannedIntent{}, fmt.Errorf("postgres: copy intent retry changed immutable evidence: %w", repository.ErrIdempotencyConflict)
		}
		return copyorigin.PlannedIntent{Intent: *existing}, nil
	}
	if err != nil {
		return copyorigin.PlannedIntent{}, err
	}
	return copyorigin.PlannedIntent{Intent: *intent, Created: true}, nil
}

func (r *CopyOriginRepo) GetRun(ctx context.Context, id uuid.UUID) (*copyorigin.Run, error) {
	if r == nil || r.pool == nil || id == uuid.Nil {
		return nil, fmt.Errorf("postgres: copy origin run identity is required")
	}
	return getCopyOriginRun(ctx, r.pool, id)
}

func (r *CopyOriginRepo) GetPlannedRun(ctx context.Context, subscriptionID, sourceObservationID uuid.UUID, calculationVersion int) (*copyorigin.Run, []copyorigin.PlannedIntent, error) {
	if r == nil || r.pool == nil || subscriptionID == uuid.Nil || sourceObservationID == uuid.Nil || calculationVersion < 1 {
		return nil, nil, repository.ErrNotFound
	}
	var runID uuid.UUID
	err := r.pool.QueryRow(ctx, `SELECT id FROM copy_origin_rebalance_runs WHERE subscription_id=$1 AND source_observation_id=$2 AND calculation_version=$3 ORDER BY created_at DESC,id DESC LIMIT 1`, subscriptionID, sourceObservationID, calculationVersion).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	return r.getPlannedRunByID(ctx, runID)
}

const listUnfinishedCopyRunsSQL = `SELECT DISTINCT run.id,run.subscription_id FROM copy_origin_rebalance_runs run
		JOIN copy_subscriptions subscription ON subscription.id=run.subscription_id
		JOIN copy_origin_rebalance_intents child ON child.run_id=run.id
		JOIN copy_trade_intents intent ON intent.id=child.intent_id
		WHERE run.account_id=$1 AND run.environment=$2 AND subscription.account_id=$1 AND subscription.environment=$2
		  AND subscription.is_paper=true AND intent.account_id=$1 AND intent.environment=$2
		  AND intent.policy_status='approved' AND ((subscription.status='paper_active' AND (intent.status='received' OR (intent.status='failed' AND intent.risk_status='pending')))
		    OR (intent.status IN ('ordered','partial') AND intent.order_id IS NOT NULL)
		    OR (intent.status='received' AND EXISTS (
		      SELECT 1 FROM orders o WHERE o.copy_intent_id=intent.id AND o.copy_origin_rebalance_run_id=run.id
		        AND o.account_id=intent.account_id AND o.environment=intent.environment
		        AND o.origin_type=intent.origin_type AND o.origin_id=intent.origin_id::text)))
		ORDER BY run.id`

func (r *CopyOriginRepo) ListUnfinishedRuns(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment) ([]copyorigin.RecoverableRun, error) {
	if r == nil || r.pool == nil || accountID == uuid.Nil || !environment.IsValid() {
		return nil, fmt.Errorf("postgres: unfinished copy run scope is required")
	}
	rows, err := r.pool.Query(ctx, listUnfinishedCopyRunsSQL, accountID, environment)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type identity struct{ runID, subscriptionID uuid.UUID }
	var identities []identity
	for rows.Next() {
		var value identity
		if err := rows.Scan(&value.runID, &value.subscriptionID); err != nil {
			return nil, err
		}
		identities = append(identities, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]copyorigin.RecoverableRun, 0, len(identities))
	for _, identity := range identities {
		run, intents, err := r.getPlannedRunByID(ctx, identity.runID)
		if err != nil {
			return nil, err
		}
		result = append(result, copyorigin.RecoverableRun{Run: run, SubscriptionID: identity.subscriptionID, Intents: intents})
	}
	return result, nil
}

func (r *CopyOriginRepo) getPlannedRunByID(ctx context.Context, runID uuid.UUID) (*copyorigin.Run, []copyorigin.PlannedIntent, error) {
	run, err := getCopyOriginRun(ctx, r.pool, runID)
	if err != nil {
		return nil, nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT intent_id FROM copy_origin_rebalance_intents WHERE run_id=$1 ORDER BY sequence`, runID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	intentIDs := make([]uuid.UUID, 0)
	for rows.Next() {
		var intentID uuid.UUID
		if scanErr := rows.Scan(&intentID); scanErr != nil {
			return nil, nil, scanErr
		}
		intentIDs = append(intentIDs, intentID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	rows.Close()
	intents := make([]copyorigin.PlannedIntent, 0, len(intentIDs))
	for _, intentID := range intentIDs {
		intent, scanErr := scanCopyIntent(r.pool.QueryRow(ctx, copyIntentSelect+` WHERE id=$1`, intentID))
		if scanErr != nil {
			return nil, nil, scanErr
		}
		intents = append(intents, copyorigin.PlannedIntent{Intent: *intent})
	}
	return run, intents, nil
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
