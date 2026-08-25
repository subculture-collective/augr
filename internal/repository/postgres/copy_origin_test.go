package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/copyorigin"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/economicid"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

func TestCopyOriginRetainedQualification(t *testing.T) {
	databaseURL := os.Getenv("COPY_ORIGIN_QUALIFICATION_DB_URL")
	if databaseURL == "" {
		t.Skip("set COPY_ORIGIN_QUALIFICATION_DB_URL to a dedicated empty schema-88 database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var version, existing, strategiesBefore int
	if err = pool.QueryRow(ctx, `SELECT version FROM schema_migrations WHERE NOT dirty`).Scan(&version); err != nil || version != 88 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM copy_subscriptions)+(SELECT count(*) FROM copy_origin_rebalance_runs),(SELECT count(*) FROM strategies)`).Scan(&existing, &strategiesBefore); err != nil || existing != 0 {
		t.Fatalf("existing=%d err=%v", existing, err)
	}
	leaderID, sourceID, observationID := uuid.New(), uuid.New(), uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO copy_leaders(id,entity_type,display_name) VALUES($1,'institution','OVR501 fixture')`, leaderID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO copy_leader_sources(id,leader_id,provider,source_type,external_key) VALUES($1,$2,'sec','sec_13f',$3)`, sourceID, leaderID, sourceID.String()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)
	if _, err = pool.Exec(ctx, `INSERT INTO copy_source_observations(id,source_id,provider_observation_id,observation_kind,effective_at,published_at,observed_at,content_hash,normalized_payload) VALUES($1,$2,'filing-1','portfolio_snapshot',$3,$4,$5,$6,'{}')`, observationID, sourceID, now.Add(-24*time.Hour), now.Add(-time.Hour), now, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	subscription := domain.DefaultCopySubscription()
	subscription.ID, subscription.LeaderID, subscription.SourceID = uuid.New(), leaderID, sourceID
	subscription.OriginType, subscription.OriginID, subscription.CreatedBy = "copy_subscription", subscription.ID, "ovr501"
	secondSubscription := domain.DefaultCopySubscription()
	secondSubscription.ID, secondSubscription.LeaderID, secondSubscription.SourceID = uuid.New(), leaderID, sourceID
	secondSubscription.OriginType, secondSubscription.OriginID, secondSubscription.CreatedBy = "copy_subscription", secondSubscription.ID, "ovr501"
	for _, fixture := range []*domain.CopySubscription{&subscription, &secondSubscription} {
		if err = fixture.Validate(); err != nil {
			t.Fatal(err)
		}
		_, err = pool.Exec(ctx, `INSERT INTO copy_subscriptions (id,leader_id,source_id,legacy_strategy_id,origin_type,origin_id,status,is_paper,method,capital_budget,cash_buffer_pct,top_n,min_source_weight,max_position_weight,max_turnover_pct,min_price,min_avg_dollar_volume,max_spread_bps,stock_allowlist,stock_blocklist,created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`, fixture.ID, fixture.LeaderID, fixture.SourceID, fixture.LegacyStrategyID, fixture.OriginType, fixture.OriginID, fixture.Status, fixture.IsPaper, fixture.Method, fixture.CapitalBudget, fixture.CashBufferPct, fixture.TopN, fixture.MinSourceWeight, fixture.MaxPositionWeight, fixture.MaxTurnoverPct, fixture.MinPrice, fixture.MinAvgDollarVolume, fixture.MaxSpreadBPS, fixture.StockAllowlist, fixture.StockBlocklist, fixture.CreatedBy)
		if err != nil {
			t.Fatal(err)
		}
	}
	intents := make([]domain.CopyTradeIntent, 2)
	for i, key := range []string{"AAPL", "MSFT"} {
		id := economicid.DeterministicUUID("copy-trade-intent", subscription.ID.String(), observationID.String(), key, "1")
		intents[i] = domain.CopyTradeIntent{ID: id, SubscriptionID: subscription.ID, OriginType: "copy_subscription", OriginID: subscription.ID, SourceObservationID: observationID, InstrumentKey: key, Ticker: key, Side: domain.OrderSideBuy, TargetWeight: 0.1, TargetValue: 1000, RequestedNotional: 1000, CalculationVersion: 1, Calculation: json.RawMessage(`{"fixture":true}`), PolicyStatus: "approved", RiskStatus: "pending", Status: "received"}
	}
	run, err := copyorigin.NewRun(subscription, intents)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewCopyOriginRepo(pool)
	repo.createPlannedIntent = createSchema88CopyIntent
	for _, stage := range []string{"run", "intent", "planned_intent"} {
		repo.afterStage = func(current string) error {
			if current == stage {
				return errors.New("injected")
			}
			return nil
		}
		if _, _, stageErr := repo.RegisterPlannedRun(ctx, run, intents); stageErr == nil {
			t.Fatalf("stage %s accepted", stage)
		}
		var partialRuns, partialIntents int
		if countErr := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM copy_origin_rebalance_runs),(SELECT count(*) FROM copy_trade_intents)`).Scan(&partialRuns, &partialIntents); countErr != nil || partialRuns != 0 || partialIntents != 0 {
			t.Fatalf("stage %s partial runs/intents=%d/%d err=%v", stage, partialRuns, partialIntents, countErr)
		}
	}
	repo.afterStage = nil
	invalid := append([]domain.CopyTradeIntent(nil), intents...)
	invalid[1].Calculation = json.RawMessage(`{`)
	if _, _, insertErr := repo.RegisterPlannedRun(ctx, run, invalid); insertErr == nil {
		t.Fatal("invalid intent insert accepted")
	}
	var failedRuns, failedIntents int
	if countErr := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM copy_origin_rebalance_runs),(SELECT count(*) FROM copy_trade_intents)`).Scan(&failedRuns, &failedIntents); countErr != nil || failedRuns != 0 || failedIntents != 0 {
		t.Fatalf("intent insert failure left runs/intents=%d/%d err=%v", failedRuns, failedIntents, countErr)
	}
	var stages []string
	repo.afterStage = func(stage string) error {
		stages = append(stages, stage)
		return nil
	}
	if _, canonical, writeErr := repo.RegisterPlannedRun(ctx, run, intents); writeErr != nil || len(canonical) != len(intents) {
		t.Fatalf("fresh planned run = %d intents, error %v", len(canonical), writeErr)
	}
	wantStages := []string{"planned_intent", "planned_intent", "run", "intent", "intent"}
	if strings.Join(stages, ",") != strings.Join(wantStages, ",") {
		t.Fatalf("registration stages = %v, want %v", stages, wantStages)
	}
	repo.afterStage = nil
	errs := make(chan error, 8)
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, canonical, writeErr := repo.RegisterPlannedRun(ctx, run, intents)
			if writeErr == nil && len(canonical) != len(intents) {
				writeErr = errors.New("canonical intents missing")
			}
			errs <- writeErr
		}()
	}
	wait.Wait()
	close(errs)
	for writeErr := range errs {
		if writeErr != nil {
			t.Error(writeErr)
		}
	}
	loaded, err := NewCopyOriginRepo(pool).GetRun(ctx, run.ID())
	if err != nil || loaded.Digest() != run.Digest() {
		t.Fatalf("reload=%v/%v", loaded, err)
	}
	conflictIntents := append([]domain.CopyTradeIntent(nil), intents...)
	for i := range conflictIntents {
		conflictIntents[i].ID = uuid.New()
		conflictIntents[i].SubscriptionID = secondSubscription.ID
		conflictIntents[i].OriginID = secondSubscription.ID
	}
	conflictRun, err := copyorigin.NewRun(secondSubscription, conflictIntents)
	if err != nil {
		t.Fatal(err)
	}
	var conflict copyOriginEnvelope
	if err = json.Unmarshal(conflictRun.CanonicalBytes(), &conflict); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO copy_origin_rebalance_runs(id,schema_name,state,subscription_id,origin_type,origin_id,source_observation_id,calculation_version,intent_count,sha256,canonical_bytes,canonical_json) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,convert_from($11,'UTF8')::jsonb)`, conflictRun.ID(), conflict.Schema, conflict.State, conflict.SubscriptionID, conflict.OriginType, conflict.OriginID, conflict.SourceObservationID, conflict.CalculationVersion, len(conflict.Intents), run.Digest(), run.CanonicalBytes()); err != nil {
		t.Fatal(err)
	}
	plannedBeforeConflict := false
	repo.afterStage = func(stage string) error {
		plannedBeforeConflict = plannedBeforeConflict || stage == "planned_intent"
		return nil
	}
	if _, _, err = repo.RegisterPlannedRun(ctx, conflictRun, conflictIntents); !errors.Is(err, repository.ErrIdempotencyConflict) {
		t.Fatalf("run conflict error=%v", err)
	}
	repo.afterStage = nil
	if !plannedBeforeConflict {
		t.Fatal("run conflict occurred before planned intent insertion was attempted")
	}
	var conflictIntentCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM copy_trade_intents WHERE subscription_id=$1`, secondSubscription.ID).Scan(&conflictIntentCount); err != nil || conflictIntentCount != 0 {
		t.Fatalf("run conflict intents=%d err=%v", conflictIntentCount, err)
	}
	var subscriptions, runs, children, strategiesAfter int
	err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM copy_subscriptions),(SELECT count(*) FROM copy_origin_rebalance_runs),(SELECT count(*) FROM copy_origin_rebalance_intents),(SELECT count(*) FROM strategies)`).Scan(&subscriptions, &runs, &children, &strategiesAfter)
	if err != nil || subscriptions != 2 || runs != 2 || children != 2 || strategiesAfter != strategiesBefore {
		t.Fatalf("counts=%d/%d/%d strategies=%d->%d err=%v", subscriptions, runs, children, strategiesBefore, strategiesAfter, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE copy_origin_rebalance_runs SET state=state WHERE id=$1`, run.ID()); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("append-only=%v", err)
	}
	t.Logf("subscription=%s origin=copy_subscription/%s run=%s sha=%s intents=%d strategies=%d", subscription.ID, subscription.ID, run.ID(), run.Digest(), children, strategiesAfter)
}

func createSchema88CopyIntent(ctx context.Context, tx pgx.Tx, value domain.CopyTradeIntent) (domain.CopyTradeIntent, error) {
	if value.Calculation == nil {
		value.Calculation = json.RawMessage(`{}`)
	}
	if value.PolicyReasons == nil {
		value.PolicyReasons = []string{}
	}
	if value.RiskReasons == nil {
		value.RiskReasons = []string{}
	}
	err := tx.QueryRow(ctx, `INSERT INTO copy_trade_intents (id,subscription_id,origin_type,origin_id,source_observation_id,pipeline_run_id,instrument_key,ticker,side,target_weight,target_value,attributed_current_value,requested_notional,executable_price,calculation_version,calculation,policy_status,policy_reasons,risk_status,risk_reasons,order_id,status) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22) ON CONFLICT (subscription_id,source_observation_id,instrument_key,calculation_version) DO NOTHING RETURNING created_at,updated_at`, value.ID, value.SubscriptionID, value.OriginType, value.OriginID, value.SourceObservationID, value.PipelineRunID, value.InstrumentKey, value.Ticker, value.Side, value.TargetWeight, value.TargetValue, value.AttributedCurrentValue, value.RequestedNotional, value.ExecutablePrice, value.CalculationVersion, value.Calculation, value.PolicyStatus, value.PolicyReasons, value.RiskStatus, value.RiskReasons, value.OrderID, value.Status).Scan(&value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT created_at,updated_at FROM copy_trade_intents WHERE subscription_id=$1 AND source_observation_id=$2 AND instrument_key=$3 AND calculation_version=$4`, value.SubscriptionID, value.SourceObservationID, value.InstrumentKey, value.CalculationVersion).Scan(&value.CreatedAt, &value.UpdatedAt)
	}
	return value, err
}
