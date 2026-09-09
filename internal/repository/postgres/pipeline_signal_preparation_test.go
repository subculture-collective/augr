package postgres

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/lifecycle"
	"github.com/google/uuid"
)

func TestPipelineSignalEvidenceScopeAndProvenance(t *testing.T) {
	date := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	completed := date.Add(15 * time.Hour)
	ref := domain.PipelineRunRef{ID: uuid.New(), TradeDate: date}
	scope, err := execution.NewStrategyExecutionScope(uuid.New(), domain.AccountEnvironmentPaperScored, uuid.New(), ref)
	if err != nil {
		t.Fatal(err)
	}
	origin, originID := scope.Origin()
	selection := CanonicalSignalSelection{Schema: CanonicalSignalSelectionSchema, Ticker: "SPY", AliasProvider: "fixture", VenueContractID: uuid.New(), QuoteSnapshotID: uuid.New(), SimulationPolicyVersion: "fixture-policy", TimeInForce: lifecycle.TimeInForceDay}
	payload, err := json.Marshal(selection)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"valid", "missing", "ambiguous", "wrong_account", "wrong_origin", "wrong_date", "late", "incomplete_run", "wrong_signal", "wrong_ticker", "unknown_field", "missing_quote"} {
		t.Run(name, func(t *testing.T) {
			run := domain.PipelineRun{ID: ref.ID, TradeDate: date, AccountID: scope.AccountID(), Environment: scope.Environment(), OriginType: string(origin), OriginID: originID, Ticker: "SPY", Status: domain.PipelineStatusCompleted, Signal: domain.PipelineSignalBuy, CompletedAt: &completed}
			snapshot := domain.PipelineRunSnapshot{ID: uuid.New(), AccountID: run.AccountID, Environment: run.Environment, OriginType: run.OriginType, OriginID: run.OriginID, PipelineRunID: ref.ID, PipelineRunTradeDate: date, DataType: "market", Payload: payload, CreatedAt: completed.Add(-time.Second)}
			plan := execution.TradingPlan{Ticker: "SPY", MarketType: domain.MarketTypeStock, Action: domain.PipelineSignalBuy}
			switch name {
			case "wrong_account":
				snapshot.AccountID = uuid.New()
			case "wrong_origin":
				snapshot.OriginID = uuid.NewString()
			case "wrong_date":
				snapshot.PipelineRunTradeDate = date.Add(24 * time.Hour)
			case "late":
				snapshot.CreatedAt = completed.Add(time.Second)
			case "incomplete_run":
				run.CompletedAt = nil
			case "wrong_signal":
				run.Signal = domain.PipelineSignalSell
			case "wrong_ticker":
				plan.Ticker = "QQQ"
			case "unknown_field":
				snapshot.Payload = json.RawMessage(`{"schema":"canonical-signal-evidence-v1","ticker":"SPY","unknown":true}`)
			case "missing_quote":
				changed := selection
				changed.QuoteSnapshotID = uuid.Nil
				var marshalErr error
				snapshot.Payload, marshalErr = json.Marshal(changed)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
			}
			snapshots := []domain.PipelineRunSnapshot{snapshot}
			if name == "missing" {
				snapshots = nil
			}
			if name == "ambiguous" {
				snapshots = append(snapshots, snapshot)
			}
			evidence, err := pipelineSignalEvidence(scope, plan, &run, snapshots)
			if name != "valid" {
				if err == nil {
					t.Fatal("accepted invalid scoped evidence")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if evidence.QuoteSnapshotID != selection.QuoteSnapshotID || evidence.VenueContractID != selection.VenueContractID || !evidence.DecisionAt.Equal(completed) || evidence.Proposal.Event.SourceEventID != evidence.OrderIdempotencyKey || string(evidence.Proposal.Metadata) != string(payload) {
				t.Fatal("retained selectors or durable decision provenance changed")
			}
			replay, err := pipelineSignalEvidence(scope, plan, &run, snapshots)
			if err != nil || replay.Proposal.IdempotencyKey != evidence.Proposal.IdempotencyKey {
				t.Fatal("replay identity changed")
			}
		})
	}
}
