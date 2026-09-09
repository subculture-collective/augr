package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
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
	phase   string
	tamper  bool
}

func (s *stockCaptureSourceFixture) QuoteConditions(context.Context, string) (*alpacadata.QuoteConditionEvidence, error) {
	raw := []byte(`{"R":"Regular Market Maker Open"}`)
	digest := sha256.Sum256(raw)
	return &alpacadata.QuoteConditionEvidence{Tape: "B", RequestPath: "/v2/stocks/meta/conditions/quote?tape=B", ResponseSHA256: hex.EncodeToString(digest[:]), ObservedAt: s.receipt.ObservedAt, RawResponse: raw}, nil
}

func (s *stockCaptureSourceFixture) ClockAt(_ context.Context, _ string, at time.Time) (*alpacadata.MarketClockEvidence, error) {
	until := at.Add(time.Hour)
	raw := []byte(fmt.Sprintf(`{"clocks":[{"market":{"acronym":"IEX","mic":"IEXG"},"timestamp":%q,"phase_until":%q,"phase":%q,"is_market_day":true}]}`, at.Format(time.RFC3339Nano), until.Format(time.RFC3339Nano), s.phase))
	digest := sha256.Sum256(raw)
	result := &alpacadata.MarketClockEvidence{Market: "IEX", MIC: "IEXG", Phase: s.phase, At: at, PhaseUntil: until, ObservedAt: s.receipt.ObservedAt, IsMarketDay: true, RawResponse: raw, ResponseSHA256: hex.EncodeToString(digest[:]), RequestPath: "/v3/clock?" + url.Values{"markets": {"IEX"}, "time": {at.UTC().Format(time.RFC3339Nano)}}.Encode()}
	if s.tamper {
		result.Phase = "pre"
	}
	return result, nil
}

func (s *stockCaptureSourceFixture) LatestQuote(context.Context, string, string) (*alpacadata.StockQuoteEvidence, error) {
	s.calls++
	return s.receipt, nil
}

func TestPipelineStockCaptureRejectsMissingStatus(t *testing.T) {
	testPipelineStockCapture(t, false, false)
}

func TestPipelineStockCaptureQualifiedStatus(t *testing.T) {
	testPipelineStockCapture(t, true, false)
}

func TestPipelineETFCaptureQualifiedStatus(t *testing.T) {
	testPipelineStockCapture(t, true, true)
}

func testPipelineStockCapture(t *testing.T, qualified, etf bool) {
	t.Helper()
	f := newExecutionLifecycleFixture(t)
	applyRepositoryMigrationRange(t, f.ctx, f.pool, "000108", "000113")
	if etf {
		value := *f.instrument
		value.ID = uuid.New()
		value.IdentityKey = "fixture-etf:" + value.ID.String()
		value.AssetClass = instrument.AssetClassETF
		var err error
		f.instrument, err = NewInstrumentRepo(f.pool).CreateInstrument(f.ctx, &value)
		if err != nil {
			t.Fatal(err)
		}
		contract := *f.contract
		contract.ID = uuid.New()
		contract.InstrumentID = value.ID
		contract.ContractID = strings.ToUpper("fixture-etf:" + contract.ID.String())
		f.contract, err = NewInstrumentRepo(f.pool).RegisterVenueContract(f.ctx, &contract)
		if err != nil {
			t.Fatal(err)
		}
	}
	alias, err := instrument.NewAliasEvent(instrument.AliasEventInput{InstrumentID: f.instrument.ID, Provider: "alpaca", AliasType: instrument.AliasTicker, AliasValue: "FIXTURE", Action: instrument.AliasAssigned, EffectiveAt: f.baseTime.Add(-time.Hour), CreatedAt: f.baseTime.Add(-time.Hour), Source: "test-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewInstrumentRepo(f.pool).AppendAliasEvent(f.ctx, alias); err != nil {
		t.Fatal(err)
	}
	marketStatus, sessionStatus := "open", "regular"
	if qualified {
		marketStatus, sessionStatus = alpacadata.IEXRegularQuoteStatus, "core"
	}
	policy, err := simulation.NewPolicy(simulation.PolicyInput{Schema: simulation.PolicySchemaV1, Assets: []simulation.AssetPolicy{{AssetClass: f.instrument.AssetClass, OrderTypes: []lifecycle.OrderType{lifecycle.OrderLimit}, TimeInForce: []lifecycle.TimeInForce{lifecycle.TimeInForceDay}, QuoteRequirements: marketdata.QuoteRequirements{RequireSource: true, RequireVenueContract: true, RequireBid: true, RequireAsk: true, RequireBidDepth: true, RequireAskDepth: true, RequireMarketStatus: true, RequireSessionStatus: true, AllowedMarketStatuses: []string{marketStatus}, AllowedSessionStatuses: []string{sessionStatus}, MaxAge: 10 * time.Second}, MaxDepthParticipation: decimal.NewFromInt(1), Calendar: simulation.CalendarPolicy{Kind: simulation.CalendarExplicitSessions, Sessions: []simulation.SessionWindow{{Label: "fixture", OpenAt: f.baseTime.Add(-time.Hour), CloseAt: f.baseTime.Add(time.Hour)}}}, Fees: simulation.FeePolicy{Scale: 4}}}})
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
	raw := []byte(`{"symbol":"FIXTURE","quote":{"bp":10.24,"ap":10.26,"bs":80,"as":90,"bx":"V","ax":"V","c":["R"],"z":"B","t":"2026-08-15T17:59:59.123456789Z"}}`)
	digest := sha256.Sum256(raw)
	exchange, err := time.Parse(time.RFC3339Nano, "2026-08-15T17:59:59.123456789Z")
	if err != nil {
		t.Fatal(err)
	}
	cases := []string{"valid", "missing_policy", "pinned_input", "completed"}
	if qualified {
		cases = append(cases, "premarket", "tamper", "mixed")
	}
	for _, name := range cases {
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
			capture := &PipelineStockCapture{Pool: f.pool, Provider: source, Feed: "iex", now: func() time.Time { return f.baseTime.Add(123 * time.Nanosecond) }}
			source.receipt.Tape, source.receipt.Conditions = "B", []string{"R"}
			if qualified {
				source.phase = "core"
				capture.Calendar = source
			}
			if name == "premarket" {
				source.phase = "pre"
			}
			if name == "tamper" {
				source.tamper = true
			}
			if name == "mixed" {
				source.receipt.Conditions = []string{"R", "H"}
			}
			selection := CanonicalSignalSelection{Schema: CanonicalSignalSelectionSchema, Ticker: "FIXTURE", AliasProvider: "alpaca", VenueContractID: f.contract.ID, SimulationPolicyVersion: artifact.Version, TimeInForce: lifecycle.TimeInForceDay}
			if name == "missing_policy" {
				selection.SimulationPolicyVersion = "missing"
			}
			if name == "pinned_input" {
				selection.QuoteSnapshotID = uuid.New()
			}
			pinned, err := capture.Capture(f.ctx, scope, selection)
			if name == "premarket" || name == "tamper" || name == "mixed" {
				if err == nil || pinned != nil {
					t.Fatal("invalid status capture accepted")
				}
				return
			}
			if qualified && name == "valid" {
				if err != nil || pinned == nil || source.calls != 1 {
					t.Fatalf("qualified capture failed: %v", err)
				}
				var selections, intents int
				if err := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM pipeline_run_snapshots WHERE pipeline_run_id=$1),(SELECT count(*) FROM execution_intents)`, ref.ID).Scan(&selections, &intents); err != nil || selections != 1 || intents != 0 {
					t.Fatalf("capture side effects %d/%d: %v", selections, intents, err)
				}
				return
			}
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
