package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type economicPlannerStub struct{ input AcceptedFillInput }

func (stub economicPlannerStub) PlanAcceptedOrderFill(context.Context, ExecutionScope, repository.OrderFillInput) (AcceptedFillInput, error) {
	return stub.input, nil
}
func (stub economicPlannerStub) PlanAcceptedOptionFills(context.Context, ExecutionScope, []repository.OptionFillInput) ([]AcceptedFillInput, error) {
	return []AcceptedFillInput{stub.input}, nil
}
func (economicPlannerStub) PlanAcceptedPredictionSettlement(context.Context, ExecutionScope, repository.PredictionDecisionSettlementInput) (AcceptedPredictionSettlementInput, error) {
	return AcceptedPredictionSettlementInput{}, nil
}

type rawEconomicStoreStub struct {
	log      *[]string
	err      error
	mismatch bool
}

func (stub *rawEconomicStoreStub) WithExecutionAccountLock(_ context.Context, _ uuid.UUID, fn func() error) error {
	return fn()
}
func (stub *rawEconomicStoreStub) RecordEconomicSourceEvent(_ context.Context, event *ledger.EconomicSourceEvent) (*ledger.EconomicSourceEvent, error) {
	*stub.log = append(*stub.log, "raw")
	if stub.err != nil {
		return nil, stub.err
	}
	copy := *event
	if stub.mismatch {
		copy.SourceRevision += "-changed"
	}
	return &copy, nil
}

type economicCoordinatorStub struct {
	log   *[]string
	errs  []error
	calls int
}

func (stub *economicCoordinatorStub) ApplyAcceptedFill(_ context.Context, input AcceptedFillInput) (AcceptedFillResult, error) {
	*stub.log = append(*stub.log, "coordinator")
	index := stub.calls
	stub.calls++
	if index < len(stub.errs) && stub.errs[index] != nil {
		return AcceptedFillResult{}, stub.errs[index]
	}
	return AcceptedFillResult{Mutation: repository.OrderFillResult{OrderID: input.Mutation.Order.ID, TradeID: input.Mutation.Trade.ID}}, nil
}
func (*economicCoordinatorStub) ApplyAcceptedOptionFills(context.Context, []AcceptedFillInput) ([]AcceptedFillResult, error) {
	return nil, nil
}
func (*economicCoordinatorStub) SettlePredictionDecision(context.Context, AcceptedPredictionSettlementInput) (repository.PredictionDecisionSettlementResult, error) {
	return repository.PredictionDecisionSettlementResult{}, nil
}

func acceptedWriterFixture(t *testing.T) (ExecutionScope, repository.OrderFillInput, AcceptedFillInput) {
	t.Helper()
	accountID := uuid.New()
	scope, err := NewNonRunExecutionScope(accountID, domain.AccountEnvironmentPaperScored, ledger.ExecutionOriginOperator, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	order := &domain.Order{ID: uuid.New(), AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: string(ledger.ExecutionOriginOperator), OriginID: "fixture"}
	trade := &domain.Trade{ID: uuid.New(), OrderID: &order.ID}
	mutation := repository.OrderFillInput{IdempotencyKey: "fixture", Order: order, Trade: trade, Now: now}
	source, err := ledger.NewEconomicSourceEvent(ledger.EconomicSourceEventInput{AccountID: accountID, Source: "paper", SourceNamespace: "fills/paper", SourceEventID: order.ID.String(), SourceRevision: "1", ObservedAt: now, RawPayload: []byte(`{"filled":true}`), CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	return scope, mutation, AcceptedFillInput{Scope: scope, Mutation: mutation, SourceEvent: source}
}

func TestAcceptedFillInputRejectsIncompleteEconomicGraphBeforePersistence(t *testing.T) {
	t.Parallel()
	input := AcceptedFillInput{}
	if err := input.Validate(); err == nil {
		t.Fatal("Validate() accepted an empty fill graph")
	}

	scope, err := NewNonRunExecutionScope(uuid.New(), "paper_scored", "operator", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	input.Scope = scope
	if err := input.Validate(); err == nil {
		t.Fatal("Validate() accepted a scope without exact economic payloads")
	}
}

func TestAcceptedPredictionSettlementRejectsIncompleteEconomicGraphBeforePersistence(t *testing.T) {
	t.Parallel()
	scope, err := NewNonRunExecutionScope(uuid.New(), "paper_scored", "settlement", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := (AcceptedPredictionSettlementInput{Scope: scope}).Validate(); err == nil {
		t.Fatal("Validate() accepted a settlement without exact source, reference, normalization, and ledger payloads")
	}
}

func TestCoordinatedEconomicWriterPersistsRawBeforeCoordinatorAndResolvesAmbiguousCommit(t *testing.T) {
	t.Parallel()
	scope, mutation, planned := acceptedWriterFixture(t)
	log := []string{}
	raw := &rawEconomicStoreStub{log: &log}
	coordinator := &economicCoordinatorStub{log: &log, errs: []error{errors.New("commit acknowledgement lost"), nil}}
	writer, err := NewCoordinatedEconomicWriter(economicPlannerStub{input: planned}, raw, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ApplyAcceptedOrderFill(context.Background(), scope, mutation); err != nil {
		t.Fatal(err)
	}
	if want := []string{"raw", "coordinator", "coordinator"}; len(log) != len(want) || log[0] != want[0] || log[1] != want[1] || log[2] != want[2] {
		t.Fatalf("operation order = %v, want %v", log, want)
	}
}

func TestCoordinatedEconomicWriterStopsOnRawFailureOrPayloadMismatch(t *testing.T) {
	t.Parallel()
	scope, mutation, planned := acceptedWriterFixture(t)
	for _, test := range []struct {
		name string
		raw  *rawEconomicStoreStub
	}{
		{name: "insert failure", raw: &rawEconomicStoreStub{err: errors.New("raw unavailable")}},
		{name: "payload mismatch", raw: &rawEconomicStoreStub{mismatch: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			log := []string{}
			test.raw.log = &log
			coordinator := &economicCoordinatorStub{log: &log}
			writer, err := NewCoordinatedEconomicWriter(economicPlannerStub{input: planned}, test.raw, coordinator)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writer.ApplyAcceptedOrderFill(context.Background(), scope, mutation); !errors.Is(err, ErrAcceptedEconomicRollbackConfirmed) {
				t.Fatalf("error = %v, want rollback-confirmed marker", err)
			}
			if coordinator.calls != 0 {
				t.Fatalf("coordinator calls = %d, want 0", coordinator.calls)
			}
		})
	}
}
