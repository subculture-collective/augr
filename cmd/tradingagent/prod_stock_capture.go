package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/PatrickFanella/get-rich-quick/internal/agent"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	pgrepo "github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
	"github.com/google/uuid"
)

type stockEvidenceCapture interface {
	Capture(context.Context, execution.ExecutionScope, pgrepo.CanonicalSignalSelection) (*domain.PipelineRunSnapshot, error)
}

func (r *realStrategyRunner) configurePreparedStockCapture(prepared *agent.PreparedRun, strategy domain.Strategy) error {
	if r.stockCapture == nil || r.executionAccount.Environment() != domain.AccountEnvironmentPaperScored || strategy.MarketType.Normalize() != domain.MarketTypeStock {
		return nil
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(strategy.Config, &config); err != nil {
		return fmt.Errorf("stock capture requires strategy configuration: %w", err)
	}
	raw := config["canonical_signal_selection"]
	var selection pgrepo.CanonicalSignalSelection
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&selection); err != nil {
		return fmt.Errorf("stock capture requires explicit canonical_signal_selection: %w", err)
	}
	if selection.Schema != pgrepo.CanonicalSignalSelectionSchema || selection.Ticker != strategy.Ticker || selection.AliasProvider == "" || selection.VenueContractID == uuid.Nil || selection.QuoteSnapshotID != uuid.Nil || selection.SimulationPolicyVersion == "" || selection.TimeInForce == "" {
		return fmt.Errorf("stock capture requires complete unpinned matching strategy selectors")
	}
	prepared.PrepareCompletion = func(ctx context.Context, run domain.PipelineRun, signal domain.PipelineSignal) error {
		if signal == domain.PipelineSignalHold {
			return nil
		}
		scope, err := executionScopeFromPersistedRun(&run, strategy)
		if err != nil {
			return err
		}
		_, err = r.stockCapture.Capture(ctx, scope, selection)
		return err
	}
	return nil
}
