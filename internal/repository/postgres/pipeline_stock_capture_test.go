package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	alpacadata "github.com/PatrickFanella/get-rich-quick/internal/data/alpaca"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/lifecycle"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
	"github.com/PatrickFanella/get-rich-quick/internal/marketdata"
	"github.com/PatrickFanella/get-rich-quick/internal/simulation"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type stockCaptureSourceFixture struct {
	receipt *alpacadata.StockQuoteEvidence
	calls   int
}

func (s *stockCaptureSourceFixture) LatestQuote(context.Context, string, string) (*alpacadata.StockQuoteEvidence, error) {
	s.calls++
	return s.receipt, nil
}

func TestPipelineStockCaptureRejectsMissingStatus(t *testing.T) {
	f := newExecutionLifecycleFixture(t)
	applyRepositoryMigrationRange(t, f.ctx, f.pool, "000108", "000113")
	alias, err := instrument.NewAliasEvent(instrument.AliasEventInput{InstrumentID: f.instrument.ID, Provider: "alpaca", AliasType: instrument.AliasTicker, AliasValue: "FIXTURE", Action: instrument.AliasAssigned, EffectiveAt: f.baseTime.Add(-time.Hour), CreatedAt: f.baseTime.Add(-time.Hour), Source: "test-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewInstrumentRepo(f.pool).AppendAliasEvent(f.ctx, alias); err != nil {
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
	raw := []byte(`{"symbol":"FIXTURE","quote":{"bp":10.24,"ap":10.26,"bs":80,"as":90,"bx":"V","ax":"V","t":"2026-08-15T17:59:59.123456789Z"}}`)
	digest := sha256.Sum256(raw)
	exchange, err := time.Parse(time.RFC3339Nano, "2026-08-15T17:59:59.123456789Z")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"valid", "missing_policy", "pinned_input", "completed"} {
		t.Run(name, func(t *testing.T) {
			ref := domain.PipelineRunRef{ID: uuid.New(), TradeDate: f.baseTime.Truncate(24 * time.Hour)}
			scope, err := execution.NewStrategyExecutionScope(f.account.ID, f.account.Environment, uuid.New(), ref)
			if err != nil {
				t.Fatal(err)
			}
			origin, originID := scope.Origin()
			run := &domain.PipelineRun{ID: ref.ID, TradeDate: ref.TradeDate, AccountID: f.account.ID, Environment: scope.Environment(), OriginType: string(origin), OriginID: originID, StrategyID: uuid.New(), Ticker: "FIXTURE", Status: domain.PipelineStatusRunning, StartedAt: f.baseTime.Add(-time.Minute)}
			if name == "completed" {
				run.Status = domain.PipelineStatusCompleted
				run.CompletedAt = &f.baseTime
			}
			if err := NewPipelineRunRepo(f.pool, f.account.ID).Create(f.ctx, run); err != nil {
				t.Fatal(err)
			}
			source := &stockCaptureSourceFixture{receipt: &alpacadata.StockQuoteEvidence{Ticker: "FIXTURE", Feed: "iex", RequestPath: "/v2/stocks/FIXTURE/quotes/latest?currency=USD&feed=iex", ResponseSHA256: hex.EncodeToString(digest[:]), BidExchange: "V", AskExchange: "V", Bid: decimal.RequireFromString("10.24"), Ask: decimal.RequireFromString("10.26"), BidSize: decimal.NewFromInt(80), AskSize: decimal.NewFromInt(90), ExchangeAt: exchange, ObservedAt: f.baseTime, RawResponse: raw}}
			capture := &PipelineStockCapture{Pool: f.pool, Provider: source, Feed: "iex", now: func() time.Time { return f.baseTime }}
			selection := CanonicalSignalSelection{Schema: CanonicalSignalSelectionSchema, Ticker: "FIXTURE", AliasProvider: "alpaca", VenueContractID: f.contract.ID, SimulationPolicyVersion: artifact.Version, TimeInForce: lifecycle.TimeInForceDay}
			if name == "missing_policy" {
				selection.SimulationPolicyVersion = "missing"
			}
			if name == "pinned_input" {
				selection.QuoteSnapshotID = uuid.New()
			}
			pinned, err := capture.Capture(f.ctx, scope, selection)
			if name != "valid" {
				if err == nil || source.calls != 0 {
					t.Fatalf("invalid selection consumed source: %v calls=%d", err, source.calls)
				}
				return
			}
			if err == nil || pinned != nil || source.calls != 1 {
				t.Fatalf("missing status was accepted: %v calls=%d", err, source.calls)
			}
			var quotes, selections int
			if err := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM quote_snapshots WHERE instrument_id=$1 AND provider='alpaca'),(SELECT count(*) FROM pipeline_run_snapshots WHERE pipeline_run_id=$2 AND pipeline_run_trade_date=$3::date)`, f.instrument.ID, ref.ID, ref.TradeDate).Scan(&quotes, &selections); err != nil || quotes != 1 || selections != 0 {
				t.Fatalf("rejected source retention/pin counts: %d/%d err=%v", quotes, selections, err)
			}
		})
	}
}
