package postgres

import (
	"context"
	"fmt"
	"slices"
	"time"

	alpacadata "github.com/PatrickFanella/get-rich-quick/internal/data/alpaca"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
	"github.com/PatrickFanella/get-rich-quick/internal/simulation"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// StockQuoteSource returns actual receipts from an explicitly selected feed.
type StockQuoteSource interface {
	LatestQuote(context.Context, string, string) (*alpacadata.StockQuoteEvidence, error)
	QuoteConditions(context.Context, string) (*alpacadata.QuoteConditionEvidence, error)
}

// StockCalendarSource classifies the exact quote instant using source evidence.
type StockCalendarSource interface {
	ClockAt(context.Context, string, time.Time) (*alpacadata.MarketClockEvidence, error)
}

// PipelineStockCapture owns source acquisition, normalization and pre-completion
// selection. Configuration supplies retained reference and policy selectors;
// this component never creates those facts or enables a broker account.
type PipelineStockCapture struct {
	Pool     *pgxpool.Pool
	Provider StockQuoteSource
	Calendar StockCalendarSource
	Feed     string
	now      func() time.Time
}

// Capture retains a source quote then pins it only if the explicit policy admits
// its evidence. A rejected quote can remain retained for diagnosis; no execution
// intent is written. Completion racing acquisition prevents selection pinning.
func (c *PipelineStockCapture) Capture(ctx context.Context, scope execution.ExecutionScope, selection CanonicalSignalSelection) (*domain.PipelineRunSnapshot, error) {
	ref, ok := scope.PipelineRun()
	if c == nil || c.Pool == nil || c.Provider == nil || c.Feed == "" || !ok || scope.Environment() != domain.AccountEnvironmentPaperScored || selection.Schema != CanonicalSignalSelectionSchema || selection.Ticker == "" || selection.AliasProvider == "" || selection.VenueContractID == uuid.Nil || selection.SimulationPolicyVersion == "" || selection.TimeInForce == "" || selection.QuoteSnapshotID != uuid.Nil {
		return nil, fmt.Errorf("stock capture requires complete unpinned paper-run selection")
	}
	clock := c.now
	if clock == nil {
		clock = time.Now
	}
	// Policy and retained reference timestamps use UTC microsecond precision.
	// Normalize the local eligibility instant, not the provider's quote time.
	at := clock().UTC().Truncate(time.Microsecond)
	run, err := NewPipelineRunRepo(c.Pool, scope.AccountID()).Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	origin, originID := scope.Origin()
	if run.Status != domain.PipelineStatusRunning || run.Environment != scope.Environment() || run.OriginType != string(origin) || run.OriginID != originID || run.Ticker != selection.Ticker {
		return nil, fmt.Errorf("stock capture requires matching running authority")
	}
	references := NewInstrumentRepo(c.Pool)
	reference, err := references.ResolveAlias(ctx, selection.AliasProvider, instrument.AliasTicker, selection.Ticker, at)
	if err != nil {
		return nil, err
	}
	contract, err := references.GetVenueContractByID(ctx, selection.VenueContractID)
	if err != nil {
		return nil, err
	}
	if contract.InstrumentID != reference.ID || (reference.AssetClass != instrument.AssetClassEquity && reference.AssetClass != instrument.AssetClassETF) || reference.Status != instrument.StatusActive || reference.CreatedAt.After(at) || contract.CreatedAt.After(at) || contract.ValidFrom.After(at) || contract.ValidTo != nil && !at.Before(*contract.ValidTo) {
		return nil, fmt.Errorf("stock capture reference is not eligible at capture time")
	}
	artifact, err := NewSimulationPolicyRepo(c.Pool).GetSimulationPolicyByVersion(ctx, selection.SimulationPolicyVersion)
	if err != nil {
		return nil, err
	}
	if artifact.CreatedAt.After(at) {
		return nil, fmt.Errorf("stock capture policy is not yet retained")
	}
	policy, err := simulation.PolicyFromArtifact(*artifact)
	if err != nil {
		return nil, err
	}
	asset, ok := policy.AssetPolicy(reference.AssetClass)
	if !ok || !slices.Contains(asset.TimeInForce, selection.TimeInForce) {
		return nil, fmt.Errorf("stock capture policy does not cover selection")
	}
	if _, err := policy.RouteSession(reference.AssetClass, at); err != nil {
		return nil, err
	}
	// Missing references/policy/run authority fail before consuming a data call.
	receipt, err := c.Provider.LatestQuote(ctx, selection.Ticker, c.Feed)
	if err != nil {
		return nil, err
	}
	if receipt == nil || receipt.Ticker != selection.Ticker || receipt.Feed != c.Feed || receipt.ObservedAt.Before(at) {
		return nil, fmt.Errorf("stock capture source returned mismatched or cached receipt")
	}
	var calendar *alpacadata.MarketClockEvidence
	var conditions *alpacadata.QuoteConditionEvidence
	if c.Calendar != nil {
		conditions, err = c.Provider.QuoteConditions(ctx, receipt.Tape)
		if err != nil {
			return nil, err
		}
		calendar, err = c.Calendar.ClockAt(ctx, "IEX", receipt.ExchangeAt)
		if err != nil {
			return nil, err
		}
	}
	retainedAt := clock().UTC()
	snapshot, err := receipt.QuoteSnapshot(*reference, *contract, retainedAt)
	if c.Calendar != nil {
		snapshot, err = receipt.QuoteSnapshotWithStatus(*reference, *contract, retainedAt, calendar, conditions)
	}
	if err != nil {
		return nil, err
	}
	persisted, err := NewQuoteSnapshotRepo(c.Pool).RecordQuoteSnapshot(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	if _, err := persisted.AssessForExecution(retainedAt, asset.QuoteRequirements, *reference, *contract); err != nil {
		return nil, err
	}
	selection.QuoteSnapshotID = persisted.ID
	return RecordPipelineSignalSelection(ctx, c.Pool, scope, selection)
}
