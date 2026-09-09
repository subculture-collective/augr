package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/agent"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/lifecycle"
	pgrepo "github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
	"github.com/google/uuid"
)

type captureConfigurationFixture struct {
	calls       int
	fail, empty bool
}

func (f *captureConfigurationFixture) Capture(context.Context, execution.ExecutionScope, pgrepo.CanonicalSignalSelection) (*domain.PipelineRunSnapshot, error) {
	f.calls++
	if f.fail {
		return nil, errors.New("capture failed")
	}
	if f.empty {
		return nil, nil
	}
	return &domain.PipelineRunSnapshot{}, nil
}

func TestConfigurePreparedStockCapture(t *testing.T) {
	for _, name := range []string{"valid", "hold", "missing", "wrong_ticker", "pinned", "wrong_scope", "failure", "empty", "prediction"} {
		t.Run(name, func(t *testing.T) {
			capture := &captureConfigurationFixture{fail: name == "failure", empty: name == "empty"}
			runner := &realStrategyRunner{executionAccount: testExecutionAccountBinding, stockCapture: capture}
			selection := pgrepo.CanonicalSignalSelection{Schema: pgrepo.CanonicalSignalSelectionSchema, Ticker: "SPY", AliasProvider: "alpaca", VenueContractID: uuid.New(), SimulationPolicyVersion: "fixture-policy", TimeInForce: lifecycle.TimeInForceDay}
			if name == "wrong_ticker" {
				selection.Ticker = "QQQ"
			}
			if name == "pinned" {
				selection.QuoteSnapshotID = uuid.New()
			}
			config, err := json.Marshal(map[string]any{"canonical_signal_selection": selection})
			if err != nil {
				t.Fatal(err)
			}
			if name == "missing" {
				config = []byte(`{}`)
			}
			strategy := domain.Strategy{ID: uuid.New(), Ticker: "SPY", MarketType: domain.MarketTypeStock, Config: config}
			if name == "prediction" {
				strategy.MarketType = domain.MarketTypeKalshi
				strategy.Config = []byte(`{}`)
			}
			prepared := agent.PreparedRun{}
			err = runner.configurePreparedStockCapture(&prepared, strategy)
			if name == "missing" || name == "wrong_ticker" || name == "pinned" {
				if err == nil || capture.calls != 0 {
					t.Fatal("invalid selector accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if name == "prediction" {
				if prepared.PrepareCompletion != nil {
					t.Fatal("prediction path intercepted")
				}
				return
			}
			run := domain.PipelineRun{ID: uuid.New(), TradeDate: time.Now().UTC().Truncate(24 * time.Hour), StrategyID: strategy.ID}
			if err := bindStrategyRunScope(&run, testExecutionAccountBinding, uuid.New()); err != nil {
				t.Fatal(err)
			}
			if name == "wrong_scope" {
				run.StrategyID = uuid.New()
			}
			signal := domain.PipelineSignalBuy
			if name == "hold" {
				signal = domain.PipelineSignalHold
			}
			err = prepared.PrepareCompletion(t.Context(), run, signal)
			wantErr := name == "wrong_scope" || name == "failure" || name == "empty"
			if (err != nil) != wantErr {
				t.Fatalf("unexpected capture outcome: %v", err)
			}
			wantCalls := 1
			if name == "hold" || name == "wrong_scope" {
				wantCalls = 0
			}
			if capture.calls != wantCalls {
				t.Fatal("unexpected capture calls", capture.calls)
			}
		})
	}
}
