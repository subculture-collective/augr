package postgres

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/lifecycle"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
	"github.com/PatrickFanella/get-rich-quick/internal/marketdata"
	"github.com/PatrickFanella/get-rich-quick/internal/simulation"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestLoadSignalPreparationRetainedGraph(t *testing.T) {
	f := newExecutionLifecycleFixture(t)
	other := newExecutionLifecycleFixtureWithPool(t, f.ctx, f.pool)
	for _, ticker := range []string{"FIXTURE", "LATE"} {
		created := f.baseTime.Add(-time.Hour)
		if ticker == "LATE" {
			created = f.baseTime.Add(time.Hour)
		}
		alias, err := instrument.NewAliasEvent(instrument.AliasEventInput{InstrumentID: f.instrument.ID, Provider: "fixture", AliasType: instrument.AliasTicker, AliasValue: ticker, Action: instrument.AliasAssigned, EffectiveAt: f.baseTime.Add(-time.Hour), CreatedAt: created, Source: "test-fixture"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := NewInstrumentRepo(f.pool).AppendAliasEvent(f.ctx, alias); err != nil {
			t.Fatal(err)
		}
	}
	depth := decimal.NewFromInt(100)
	quote, err := marketdata.NewQuoteSnapshot(marketdata.QuoteSnapshotInput{InstrumentID: f.instrument.ID, VenueContractID: &f.contract.ID, Provider: "fixture", Venue: f.contract.Venue, Source: "fixture-feed", ObservationNamespace: "loader-fixture", ObservationID: uuid.NewString(), ExchangeAt: f.snapshot.ExchangeAt, ReceivedAt: f.snapshot.ReceivedAt, AvailableAt: f.snapshot.AvailableAt, Bid: f.snapshot.Bid, Ask: f.snapshot.Ask, BidSize: &depth, AskSize: &depth, MarketStatus: "open", SessionStatus: "regular", CreatedAt: f.snapshot.CreatedAt, Bids: []marketdata.DepthLevelInput{{Price: *f.snapshot.Bid, Size: depth}}, Asks: []marketdata.DepthLevelInput{{Price: *f.snapshot.Ask, Size: depth}}})
	if err != nil {
		t.Fatal(err)
	}
	f.snapshot, err = NewQuoteSnapshotRepo(f.pool).RecordQuoteSnapshot(f.ctx, quote)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := simulation.NewPolicy(simulation.PolicyInput{Schema: simulation.PolicySchemaV1, Assets: []simulation.AssetPolicy{{AssetClass: instrument.AssetClassEquity, OrderTypes: []lifecycle.OrderType{lifecycle.OrderLimit}, TimeInForce: []lifecycle.TimeInForce{lifecycle.TimeInForceDay}, QuoteRequirements: marketdata.QuoteRequirements{RequireSource: true, RequireVenueContract: true, RequireBid: true, RequireAsk: true, RequireBidDepth: true, RequireAskDepth: true, RequireMarketStatus: true, RequireSessionStatus: true, AllowedMarketStatuses: []string{"open"}, AllowedSessionStatuses: []string{"regular"}, MaxAge: 10 * time.Second}, MaxDepthParticipation: decimal.NewFromInt(1), Calendar: simulation.CalendarPolicy{Kind: simulation.CalendarExplicitSessions, Sessions: []simulation.SessionWindow{{Label: "fixture", OpenAt: f.baseTime.Add(-time.Hour), CloseAt: f.baseTime.Add(time.Hour)}}}, Fees: simulation.FeePolicy{Scale: 4}}}})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := policy.NewArtifact(f.baseTime.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewSimulationPolicyRepo(f.pool).RegisterSimulationPolicy(f.ctx, artifact); err != nil {
		t.Fatal(err)
	}
	futureAsset, ok := policy.AssetPolicy(instrument.AssetClassEquity)
	if !ok {
		t.Fatal("fixture has no equity policy")
	}
	futureAsset.Fees.Scale = 3 // Distinct content-addressed artifact.
	futurePolicy, err := simulation.NewPolicy(simulation.PolicyInput{Schema: simulation.PolicySchemaV1, Assets: []simulation.AssetPolicy{futureAsset}})
	if err != nil {
		t.Fatal(err)
	}
	futureArtifact, err := futurePolicy.NewArtifact(f.baseTime.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewSimulationPolicyRepo(f.pool).RegisterSimulationPolicy(f.ctx, futureArtifact); err != nil {
		t.Fatal(err)
	}
	scope, err := execution.NewStrategyExecutionScope(f.account.ID, f.account.Environment, uuid.New(), domain.PipelineRunRef{ID: uuid.New(), TradeDate: f.baseTime.Truncate(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("pipeline_factory", func(t *testing.T) {
		applyRepositoryMigrationRange(t, f.ctx, f.pool, "000108", "000113")
		ref, ok := scope.PipelineRun()
		if !ok {
			t.Fatal("fixture has no run")
		}
		origin, originID := scope.Origin()
		run := &domain.PipelineRun{ID: ref.ID, TradeDate: ref.TradeDate, AccountID: f.account.ID, Environment: scope.Environment(), OriginType: string(origin), OriginID: originID, StrategyID: uuid.New(), Ticker: "FIXTURE", Status: domain.PipelineStatusCompleted, Signal: domain.PipelineSignalBuy, StartedAt: f.baseTime.Add(-time.Minute), CompletedAt: &f.baseTime}
		if err := NewPipelineRunRepo(f.pool, f.account.ID).Create(f.ctx, run); err != nil {
			t.Fatal(err)
		}
		plan := execution.TradingPlan{Ticker: "FIXTURE", MarketType: domain.MarketTypeStock, Action: domain.PipelineSignalBuy, EntryType: "limit", EntryPrice: 10.25}
		if _, err := LoadPipelineSignalPreparation(f.ctx, f.pool, scope, plan); err == nil {
			t.Fatal("factory accepted missing selection")
		}
		selection, err := json.Marshal(CanonicalSignalSelection{Schema: CanonicalSignalSelectionSchema, Ticker: "FIXTURE", AliasProvider: "fixture", VenueContractID: f.contract.ID, QuoteSnapshotID: f.snapshot.ID, SimulationPolicyVersion: artifact.Version, TimeInForce: lifecycle.TimeInForceDay})
		if err != nil {
			t.Fatal(err)
		}
		// Explicit historical fixture timestamp in the disposable schema only.
		if _, err := f.pool.Exec(f.ctx, `INSERT INTO pipeline_run_snapshots (id,account_id,environment,origin_type,origin_id,pipeline_run_id,pipeline_run_trade_date,data_type,payload,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, uuid.New(), f.account.ID, scope.Environment(), string(origin), originID, ref.ID, ref.TradeDate, "market", selection, f.baseTime.Add(-time.Second)); err != nil {
			t.Fatal(err)
		}
		loaded, err := LoadPipelineSignalPreparation(f.ctx, f.pool, scope, plan)
		if err != nil || loaded == nil {
			t.Fatalf("load pipeline graph: %v", err)
		}
		var count int
		if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM execution_intents WHERE account_id=$1`, f.account.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("pipeline loader writes: count=%d err=%v", count, err)
		}
		t.Run("selection_writer", func(t *testing.T) {
			pin := CanonicalSignalSelection{Schema: CanonicalSignalSelectionSchema, Ticker: "FIXTURE", AliasProvider: "fixture", VenueContractID: f.contract.ID, QuoteSnapshotID: f.snapshot.ID, SimulationPolicyVersion: artifact.Version, TimeInForce: lifecycle.TimeInForceDay}
			if _, err := RecordPipelineSignalSelection(f.ctx, f.pool, scope, pin); err == nil {
				t.Fatal("writer accepted completed run")
			}
			activeRun := *run
			activeRun.ID, activeRun.Status, activeRun.CompletedAt = uuid.New(), domain.PipelineStatusRunning, nil
			if err := NewPipelineRunRepo(f.pool, f.account.ID).Create(f.ctx, &activeRun); err != nil {
				t.Fatal(err)
			}
			activeScope, err := execution.NewStrategyExecutionScope(f.account.ID, scope.Environment(), uuid.MustParse(originID), domain.PipelineRunRef{ID: activeRun.ID, TradeDate: activeRun.TradeDate})
			if err != nil {
				t.Fatal(err)
			}
			missing := pin
			missing.QuoteSnapshotID = uuid.New()
			if _, err := RecordPipelineSignalSelection(f.ctx, f.pool, activeScope, missing); err == nil {
				t.Fatal("writer accepted missing quote")
			}
			pinned, err := RecordPipelineSignalSelection(f.ctx, f.pool, activeScope, pin)
			if err != nil || pinned == nil {
				t.Fatalf("pin real retained fixture: %v", err)
			}
			if !pinned.CreatedAt.After(f.baseTime) {
				t.Fatal("writer backdated retention time")
			}
			if _, err := RecordPipelineSignalSelection(f.ctx, f.pool, activeScope, pin); err == nil {
				t.Fatal("writer duplicated pinned selection")
			}
		})
	})
	for _, name := range []string{"valid", "stale", "late_alias", "missing_quote", "wrong_contract", "wrong_quote", "missing_alias", "wrong_tif", "wrong_order", "missing_policy", "future_policy", "missing_key", "missing_decision"} {
		t.Run(name, func(t *testing.T) {
			plan := execution.TradingPlan{Ticker: "FIXTURE", EntryType: "limit", EntryPrice: 10.25}
			input := PersistedSignalEvidence{AliasProvider: "fixture", VenueContractID: f.contract.ID, QuoteSnapshotID: f.snapshot.ID, SimulationPolicyVersion: artifact.Version, DecisionAt: f.baseTime, Proposal: f.proposeInput("loader-" + name), TimeInForce: lifecycle.TimeInForceDay, OrderIdempotencyKey: "loader-" + name}
			switch name {
			case "stale":
				input.DecisionAt = f.baseTime.Add(time.Minute)
			case "late_alias":
				plan.Ticker = "LATE"
			case "missing_quote":
				input.QuoteSnapshotID = uuid.New()
			case "wrong_contract":
				input.VenueContractID = other.contract.ID
			case "wrong_quote":
				input.QuoteSnapshotID = other.snapshot.ID
			case "missing_alias":
				plan.Ticker = "UNKNOWN"
			case "missing_key":
				input.OrderIdempotencyKey = ""
			case "missing_decision":
				input.DecisionAt = time.Time{}
			case "wrong_tif":
				input.TimeInForce = lifecycle.TimeInForceGTC
			case "wrong_order":
				plan.EntryType = "market"
			case "missing_policy":
				input.SimulationPolicyVersion = "missing"
			case "future_policy":
				input.SimulationPolicyVersion = futureArtifact.Version
			}
			loaded, err := LoadSignalPreparation(f.ctx, f.pool, scope, plan, input)
			if name == "valid" {
				if err != nil || loaded == nil {
					t.Fatalf("load: %v", err)
				}
			} else if err == nil {
				t.Fatal("accepted invalid retained evidence")
			}
			var count int
			if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM execution_intents WHERE account_id=$1`, f.account.ID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatal("loader wrote execution intent")
			}
		})
	}
}
