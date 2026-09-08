package execution

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/lifecycle"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// ErrAcceptedEconomicRollbackConfirmed means failure occurred before the
// coordinator could commit. Synchronous simulators may compensate only when
// this marker is present; every other error remains an ambiguous commit until
// replay proves otherwise.
var ErrAcceptedEconomicRollbackConfirmed = errors.New("accepted economic write rolled back before commit")

// AcceptedFillInput is the complete graph that must become durable in one
// database transaction after its raw provider evidence has committed.
type AcceptedFillInput struct {
	Scope             ExecutionScopeView
	Mutation          repository.OrderFillInput
	OptionMutation    *repository.OptionFillInput
	PriorLifecycle    *lifecycle.Aggregate
	Transition        *lifecycle.Transition
	AcceptedFill      *lifecycle.Fill
	SourceEvent       *ledger.EconomicSourceEvent
	Instrument        *instrument.Instrument
	VenueContract     *instrument.VenueContract
	Normalization     *ledger.EconomicNormalization
	LedgerTransaction *ledger.Transaction
}

// AcceptedFillResult returns both mutable compatibility state and the common
// immutable lifecycle produced by the same commit.
type AcceptedFillResult struct {
	Mutation   repository.OrderFillResult
	OptionFill *repository.OptionFillResult
	Lifecycle  *lifecycle.Aggregate
	Replayed   bool
}

// AcceptedPredictionSettlementInput is the settlement equivalent of an
// accepted fill graph. Raw provider evidence is persisted before this value is
// passed to the coordinator.
type AcceptedPredictionSettlementInput struct {
	Scope             ExecutionScopeView
	Mutation          repository.PredictionDecisionSettlementInput
	SourceEvent       *ledger.EconomicSourceEvent
	Instrument        *instrument.Instrument
	VenueContract     *instrument.VenueContract
	Normalization     *ledger.EconomicNormalization
	LedgerTransaction *ledger.Transaction
}

// EconomicFillCoordinator is deliberately owned by the execution consumer.
// PostgreSQL implements it without introducing a repository -> execution
// package dependency in the neutral repository interfaces package.
type EconomicFillCoordinator interface {
	ApplyAcceptedFill(context.Context, AcceptedFillInput) (AcceptedFillResult, error)
	ApplyAcceptedOptionFills(context.Context, []AcceptedFillInput) ([]AcceptedFillResult, error)
	SettlePredictionDecision(context.Context, AcceptedPredictionSettlementInput) (repository.PredictionDecisionSettlementResult, error)
}

// AcceptedEconomicPlanner is the upstream, evidence-owning side of the
// boundary. Implementations must retain the exact account, instrument,
// contract, routed lifecycle and provider payload used to interpret a fill;
// the coordinator deliberately cannot reconstruct those facts from mutable
// order rows.
type AcceptedEconomicPlanner interface {
	PlanAcceptedOrderFill(context.Context, ExecutionScope, repository.OrderFillInput) (AcceptedFillInput, error)
	PlanAcceptedOptionFills(context.Context, ExecutionScope, []repository.OptionFillInput) ([]AcceptedFillInput, error)
	PlanAcceptedPredictionSettlement(context.Context, ExecutionScope, repository.PredictionDecisionSettlementInput) (AcceptedPredictionSettlementInput, error)
}

type PositionExecutionScopeResolver interface {
	ResolvePositionExecutionScope(context.Context, domain.Position) (ExecutionScope, error)
}

type AcceptedOrderPreparationChecker interface {
	RequireAcceptedOrderPrepared(context.Context, ExecutionScope, *domain.Order) error
}

type rawEconomicEvidenceRecorder interface {
	RecordEconomicSourceEvent(context.Context, *ledger.EconomicSourceEvent) (*ledger.EconomicSourceEvent, error)
}

// CoordinatedEconomicWriter is the only compatibility-facing accepted-effect
// writer. It obtains a complete graph from the evidence-owning planner,
// commits raw evidence first, and only then enters the atomic coordinator.
type CoordinatedEconomicWriter struct {
	planner     AcceptedEconomicPlanner
	raw         rawEconomicEvidenceRecorder
	coordinator EconomicFillCoordinator
	locker      repository.ExecutionAccountLocker
}

func NewCoordinatedEconomicWriter(planner AcceptedEconomicPlanner, raw rawEconomicEvidenceRecorder, coordinator EconomicFillCoordinator) (*CoordinatedEconomicWriter, error) {
	locker, ok := raw.(repository.ExecutionAccountLocker)
	if planner == nil || raw == nil || coordinator == nil || !ok {
		return nil, fmt.Errorf("coordinated economic writer requires planner, raw evidence store, and coordinator")
	}
	return &CoordinatedEconomicWriter{planner: planner, raw: raw, coordinator: coordinator, locker: locker}, nil
}

func (writer *CoordinatedEconomicWriter) WithExecutionAccountLock(ctx context.Context, accountID uuid.UUID, fn func() error) error {
	if writer == nil || writer.locker == nil {
		return fmt.Errorf("coordinated economic writer account locker is required")
	}
	return writer.locker.WithExecutionAccountLock(ctx, accountID, fn)
}

func (writer *CoordinatedEconomicWriter) ResolvePositionExecutionScope(ctx context.Context, position domain.Position) (ExecutionScope, error) {
	resolver, ok := writer.planner.(PositionExecutionScopeResolver)
	if !ok {
		return ExecutionScope{}, fmt.Errorf("accepted economic planner cannot resolve position execution scope")
	}
	return resolver.ResolvePositionExecutionScope(ctx, position)
}

func (writer *CoordinatedEconomicWriter) RequireAcceptedOrderPrepared(ctx context.Context, scope ExecutionScope, order *domain.Order) error {
	checker, ok := writer.planner.(AcceptedOrderPreparationChecker)
	if !ok {
		return fmt.Errorf("accepted economic planner cannot verify routed order preparation")
	}
	return checker.RequireAcceptedOrderPrepared(ctx, scope, order)
}

func (writer *CoordinatedEconomicWriter) ApplyAcceptedOrderFill(ctx context.Context, scope ExecutionScope, mutation repository.OrderFillInput) (repository.OrderFillResult, error) {
	input, err := writer.planner.PlanAcceptedOrderFill(ctx, scope, mutation)
	if err != nil {
		return repository.OrderFillResult{}, errors.Join(ErrAcceptedEconomicRollbackConfirmed, err)
	}
	if err := writer.persistRaw(ctx, input.SourceEvent); err != nil {
		return repository.OrderFillResult{}, errors.Join(ErrAcceptedEconomicRollbackConfirmed, err)
	}
	result, err := writer.coordinator.ApplyAcceptedFill(ctx, input)
	if err != nil {
		resolveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		result, err = writer.coordinator.ApplyAcceptedFill(resolveCtx, input)
		cancel()
	}
	return result.Mutation, err
}

func (writer *CoordinatedEconomicWriter) ApplyAcceptedOptionFills(ctx context.Context, scope ExecutionScope, mutations []repository.OptionFillInput) ([]repository.OptionFillResult, error) {
	inputs, err := writer.planner.PlanAcceptedOptionFills(ctx, scope, mutations)
	if err != nil {
		return nil, errors.Join(ErrAcceptedEconomicRollbackConfirmed, err)
	}
	for index := range inputs {
		if err := writer.persistRaw(ctx, inputs[index].SourceEvent); err != nil {
			return nil, errors.Join(ErrAcceptedEconomicRollbackConfirmed, fmt.Errorf("record accepted option fill raw evidence %d: %w", index, err))
		}
	}
	results, err := writer.coordinator.ApplyAcceptedOptionFills(ctx, inputs)
	if err != nil {
		resolveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		results, err = writer.coordinator.ApplyAcceptedOptionFills(resolveCtx, inputs)
		cancel()
	}
	if err != nil {
		return nil, err
	}
	mutated := make([]repository.OptionFillResult, len(results))
	for index := range results {
		if results[index].OptionFill == nil {
			return nil, fmt.Errorf("accepted option fill %d returned no mutable result", index)
		}
		mutated[index] = *results[index].OptionFill
	}
	return mutated, nil
}

func (writer *CoordinatedEconomicWriter) SettleAcceptedPredictionDecision(ctx context.Context, scope ExecutionScope, mutation repository.PredictionDecisionSettlementInput) (repository.PredictionDecisionSettlementResult, error) {
	input, err := writer.planner.PlanAcceptedPredictionSettlement(ctx, scope, mutation)
	if err != nil {
		return repository.PredictionDecisionSettlementResult{}, errors.Join(ErrAcceptedEconomicRollbackConfirmed, err)
	}
	if err := writer.persistRaw(ctx, input.SourceEvent); err != nil {
		return repository.PredictionDecisionSettlementResult{}, errors.Join(ErrAcceptedEconomicRollbackConfirmed, err)
	}
	result, err := writer.coordinator.SettlePredictionDecision(ctx, input)
	if err != nil {
		resolveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		result, err = writer.coordinator.SettlePredictionDecision(resolveCtx, input)
		cancel()
	}
	return result, err
}

func (writer *CoordinatedEconomicWriter) persistRaw(ctx context.Context, source *ledger.EconomicSourceEvent) error {
	if writer == nil || writer.raw == nil || source == nil {
		return fmt.Errorf("accepted economic raw evidence is required")
	}
	persisted, err := writer.raw.RecordEconomicSourceEvent(ctx, source)
	if err != nil {
		return err
	}
	if !ledger.SameEconomicSourceEventPayload(persisted, source) {
		return fmt.Errorf("raw economic store returned mismatched source event")
	}
	return nil
}

type AcceptedOrderFillWriter interface {
	ApplyAcceptedOrderFill(context.Context, ExecutionScope, repository.OrderFillInput) (repository.OrderFillResult, error)
}

type AcceptedOptionFillWriter interface {
	ApplyAcceptedOptionFills(context.Context, ExecutionScope, []repository.OptionFillInput) ([]repository.OptionFillResult, error)
}

type AcceptedPredictionSettlementWriter interface {
	SettleAcceptedPredictionDecision(context.Context, ExecutionScope, repository.PredictionDecisionSettlementInput) (repository.PredictionDecisionSettlementResult, error)
}

type AcceptedEconomicWriter interface {
	AcceptedOrderFillWriter
	AcceptedOptionFillWriter
	AcceptedPredictionSettlementWriter
}

// ExecutionScopeView is the immutable subset required by accepted economic
// writes. ExecutionScope implements it; venue adapters can depend on the view
// without importing repository implementations.
type ExecutionScopeView interface {
	AccountID() uuid.UUID
	Environment() domain.AccountEnvironment
	Origin() (ledger.ExecutionOriginType, string)
	CopyOriginRunID() uuid.UUID
}

func (input AcceptedFillInput) Validate() error {
	if err := validateAcceptedEconomicGraph(input.Scope, input.SourceEvent, input.Instrument, input.VenueContract, input.Normalization, input.LedgerTransaction); err != nil {
		return err
	}
	if input.PriorLifecycle == nil || input.Transition == nil || input.AcceptedFill == nil {
		return fmt.Errorf("accepted fill requires prior lifecycle, transition, and fill")
	}
	if input.Transition.Fill == nil || !lifecycle.SameFillPayload(input.Transition.Fill, input.AcceptedFill) {
		return fmt.Errorf("accepted fill differs from lifecycle transition")
	}
	if input.Transition.Normalization == nil || !ledger.SameEconomicNormalizationPayload(input.Transition.Normalization, input.Normalization) {
		return fmt.Errorf("accepted fill normalization differs from lifecycle transition")
	}
	if _, err := lifecycle.ApplyTransition(input.PriorLifecycle, input.Transition); err != nil {
		return fmt.Errorf("accepted fill is not a valid lifecycle transition: %w", err)
	}
	if input.Mutation.Order != nil && input.OptionMutation != nil {
		return fmt.Errorf("accepted fill cannot carry both stock and option mutations")
	}
	originType, originID := input.Scope.Origin()
	if input.Mutation.Order != nil && (input.AcceptedFill.OrderID != input.Mutation.Order.ID ||
		input.Mutation.Order.AccountID != input.Scope.AccountID() || input.Mutation.Order.Environment != input.Scope.Environment() ||
		input.Mutation.Order.OriginType != string(originType) || input.Mutation.Order.OriginID != originID) {
		return fmt.Errorf("accepted fill mutation order differs from lifecycle fill or execution scope")
	}
	if input.OptionMutation != nil && (input.OptionMutation.Order == nil || input.AcceptedFill.OrderID != input.OptionMutation.Order.ID ||
		input.OptionMutation.AccountID != input.Scope.AccountID() || input.OptionMutation.Environment != input.Scope.Environment() ||
		input.OptionMutation.OriginType != string(originType) || input.OptionMutation.OriginID != originID) {
		return fmt.Errorf("accepted option mutation order differs from lifecycle fill or execution scope")
	}
	if input.AcceptedFill.AccountID != input.Scope.AccountID() ||
		input.AcceptedFill.EconomicSourceEventID != input.SourceEvent.ID || input.AcceptedFill.InstrumentID != input.Instrument.ID ||
		input.AcceptedFill.VenueContractID != input.VenueContract.ID || input.AcceptedFill.NormalizationID != input.Normalization.ID ||
		input.AcceptedFill.LedgerTransactionID != input.LedgerTransaction.ID {
		return fmt.Errorf("accepted fill identifiers do not describe one economic graph")
	}
	if input.PriorLifecycle.Intent.AccountID != input.Scope.AccountID() || input.PriorLifecycle.Intent.ID != input.AcceptedFill.IntentID ||
		input.PriorLifecycle.Intent.Environment != input.Scope.Environment() || input.PriorLifecycle.Intent.OriginType != originType ||
		input.PriorLifecycle.Intent.OriginID != originID || input.PriorLifecycle.Intent.CopyOriginRebalanceRunID != input.Scope.CopyOriginRunID() {
		return fmt.Errorf("accepted fill lifecycle scope does not match execution scope")
	}
	return nil
}

func (input AcceptedPredictionSettlementInput) Validate() error {
	if err := validateAcceptedEconomicGraph(input.Scope, input.SourceEvent, input.Instrument, input.VenueContract, input.Normalization, input.LedgerTransaction); err != nil {
		return err
	}
	originType, originID := input.Scope.Origin()
	if input.Mutation.AccountID != input.Scope.AccountID() || input.Mutation.Environment != input.Scope.Environment() ||
		input.Mutation.OriginType != string(originType) || input.Mutation.OriginID != originID || input.Mutation.Decision == nil {
		return fmt.Errorf("prediction settlement mutation does not match execution scope")
	}
	evidence := input.Mutation.Resolution
	if evidence.Source == "" || evidence.SourceNamespace == "" || evidence.SourceEventID == "" || evidence.ObservedAt.IsZero() || len(evidence.RawPayload) == 0 {
		return fmt.Errorf("prediction settlement requires exact provider resolution evidence")
	}
	if input.SourceEvent.Source != evidence.Source || input.SourceEvent.SourceNamespace != evidence.SourceNamespace ||
		input.SourceEvent.SourceEventID != evidence.SourceEventID || input.SourceEvent.SourceRevision != evidence.SourceRevision ||
		!input.SourceEvent.ObservedAt.Equal(evidence.ObservedAt) || !bytes.Equal(input.SourceEvent.RawPayload, evidence.RawPayload) {
		return fmt.Errorf("prediction settlement source event differs from provider resolution evidence")
	}
	return nil
}

func validateAcceptedEconomicGraph(scope ExecutionScopeView, source *ledger.EconomicSourceEvent, canonicalInstrument *instrument.Instrument, venueContract *instrument.VenueContract, normalization *ledger.EconomicNormalization, transaction *ledger.Transaction) error {
	if scope == nil || scope.AccountID() == uuid.Nil || source == nil || canonicalInstrument == nil || venueContract == nil || normalization == nil || transaction == nil {
		return fmt.Errorf("accepted economic graph and execution scope are required")
	}
	if err := source.Validate(); err != nil {
		return fmt.Errorf("accepted economic source event: %w", err)
	}
	if err := canonicalInstrument.Validate(); err != nil {
		return fmt.Errorf("accepted canonical instrument: %w", err)
	}
	if err := venueContract.Validate(); err != nil {
		return fmt.Errorf("accepted venue contract: %w", err)
	}
	if err := normalization.Validate(); err != nil {
		return fmt.Errorf("accepted economic normalization: %w", err)
	}
	if err := transaction.Validate(); err != nil {
		return fmt.Errorf("accepted ledger transaction: %w", err)
	}
	originType, originID := scope.Origin()
	if source.AccountID != scope.AccountID() || normalization.Account == nil || normalization.Account.ID != scope.AccountID() ||
		normalization.ExecutionOriginType != originType || normalization.ExecutionOriginID != originID ||
		normalization.SourceEvent == nil || !ledger.SameEconomicSourceEventPayload(normalization.SourceEvent, source) ||
		normalization.Instrument == nil || !reflect.DeepEqual(normalization.Instrument, canonicalInstrument) ||
		normalization.VenueContract == nil || !reflect.DeepEqual(normalization.VenueContract, venueContract) ||
		normalization.Transaction == nil || !reflect.DeepEqual(normalization.Transaction, transaction) ||
		venueContract.InstrumentID != canonicalInstrument.ID || transaction.AccountID != scope.AccountID() {
		return fmt.Errorf("accepted economic payloads do not describe one scoped graph")
	}
	return nil
}
