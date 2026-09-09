package postgres

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/lifecycle"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
	"github.com/PatrickFanella/get-rich-quick/internal/simulation"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PersistedSignalEvidence identifies already-retained facts. Loading it never
// registers reference data, invents a policy, or copies a model price into a quote.
type PersistedSignalEvidence struct {
	AliasProvider           string
	VenueContractID         uuid.UUID
	QuoteSnapshotID         uuid.UUID
	SimulationPolicyVersion string
	DecisionAt              time.Time
	Proposal                lifecycle.ProposeInput
	TimeInForce             lifecycle.TimeInForce
	OrderIdempotencyKey     string
}

// LoadSignalPreparation binds one exact provider alias and retained evidence
// graph to a paper signal. Callers own the proposal's decision provenance.
func LoadSignalPreparation(ctx context.Context, pool *pgxpool.Pool, scope execution.ExecutionScope, plan execution.TradingPlan, input PersistedSignalEvidence) (*SignalPreparation, error) {
	if pool == nil || scope.Environment() != domain.AccountEnvironmentPaperScored || input.AliasProvider == "" || plan.Ticker == "" || input.DecisionAt.IsZero() || input.VenueContractID == uuid.Nil || input.QuoteSnapshotID == uuid.Nil || input.SimulationPolicyVersion == "" || input.OrderIdempotencyKey == "" {
		return nil, fmt.Errorf("persisted signal preparation requires complete paper evidence selectors")
	}
	account, err := NewAccountRepo(pool).GetByID(ctx, scope.AccountID())
	if err != nil {
		return nil, err
	}
	references := NewInstrumentRepo(pool)
	provider, alias, err := instrument.NormalizeAlias(input.AliasProvider, instrument.AliasTicker, plan.Ticker)
	if err != nil {
		return nil, err
	}
	var aliasCreatedAt time.Time
	var instrumentID uuid.UUID
	var aliasAction instrument.AliasAction
	// Select identity and knowledge time from the same event. Separate alias
	// reads could observe different bindings during a concurrent reassignment.
	if err := pool.QueryRow(ctx, `SELECT instrument_id, action, created_at FROM instrument_alias_events WHERE provider=$1 AND alias_type=$2 AND alias_value=$3 AND effective_at <= $4 ORDER BY effective_at DESC, created_at DESC, id DESC LIMIT 1`, provider, instrument.AliasTicker, alias, input.DecisionAt.UTC().Truncate(time.Microsecond)).Scan(&instrumentID, &aliasAction, &aliasCreatedAt); err != nil {
		return nil, err
	}
	if aliasAction != instrument.AliasAssigned || aliasCreatedAt.After(input.DecisionAt) {
		return nil, fmt.Errorf("signal alias was not assigned and known at decision time")
	}
	reference, err := references.GetInstrumentByID(ctx, instrumentID)
	if err != nil {
		return nil, err
	}
	contract, err := references.GetVenueContractByID(ctx, input.VenueContractID)
	if err != nil {
		return nil, err
	}
	quote, err := NewQuoteSnapshotRepo(pool).GetQuoteSnapshotByID(ctx, input.QuoteSnapshotID)
	if err != nil {
		return nil, err
	}
	if contract.InstrumentID != reference.ID || quote.InstrumentID != reference.ID || quote.VenueContractID == nil || *quote.VenueContractID != contract.ID {
		return nil, fmt.Errorf("persisted signal reference, venue, and quote bindings disagree")
	}
	artifact, err := NewSimulationPolicyRepo(pool).GetSimulationPolicyByVersion(ctx, input.SimulationPolicyVersion)
	if err != nil {
		return nil, err
	}
	if reference.CreatedAt.After(input.DecisionAt) || contract.CreatedAt.After(input.DecisionAt) || quote.CreatedAt.After(input.DecisionAt) || artifact.CreatedAt.After(input.DecisionAt) {
		return nil, fmt.Errorf("signal evidence was not retained at decision time")
	}
	policy, err := simulation.PolicyFromArtifact(*artifact)
	if err != nil {
		return nil, err
	}
	asset, ok := policy.AssetPolicy(reference.AssetClass)
	if !ok {
		return nil, fmt.Errorf("persisted policy does not cover signal asset")
	}
	orderType := lifecycle.OrderType(plan.EntryType)
	if orderType == "" {
		orderType = lifecycle.OrderMarket
	}
	if !slices.Contains(asset.OrderTypes, orderType) || !slices.Contains(asset.TimeInForce, input.TimeInForce) {
		return nil, fmt.Errorf("persisted policy does not permit signal order type or time in force")
	}
	if _, err := quote.AssessForExecution(input.DecisionAt, asset.QuoteRequirements, *reference, *contract); err != nil {
		return nil, err
	}
	if _, err := policy.RouteSession(reference.AssetClass, input.DecisionAt); err != nil {
		return nil, err
	}
	proposal := input.Proposal
	proposal.Account = *account
	proposal.Instrument = *reference
	proposal.DecisionSnapshot = *quote
	proposal.DecisionAt = input.DecisionAt
	return NewSignalPreparation(NewExecutionLifecycleRepo(pool), plan.Ticker, proposal, lifecycle.RouteInput{
		OrderIdempotencyKey: input.OrderIdempotencyKey, Instrument: *reference, VenueContract: *contract, RouteSnapshot: *quote,
		QuoteRequirements: asset.QuoteRequirements, TimeInForce: input.TimeInForce, PolicyKind: lifecycle.PolicySimulation, PolicyVersion: artifact.Version,
	}), nil
}
