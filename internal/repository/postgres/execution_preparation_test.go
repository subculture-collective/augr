package postgres

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/execution/lifecycle"
	"github.com/PatrickFanella/get-rich-quick/internal/marketdata"
)

func TestPrepareExecutionOrderPersistsAndReplaysExactEvidence(t *testing.T) {
	fixture := newExecutionLifecycleFixture(t)
	input := executionPreparationFixture(t, fixture, "prepared-route")
	// A previously committed proposal models interruption between stages.
	proposed, err := lifecycle.Propose(input.Proposal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.ProposeExecutionIntent(fixture.ctx, proposed); err != nil {
		t.Fatal(err)
	}
	first, err := fixture.repo.PrepareExecutionOrder(fixture.ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.repo.PrepareExecutionOrder(fixture.ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Order == nil || second.Order == nil || first.Order.ID != second.Order.ID ||
		second.Order.ClientOrderID != second.Order.ID.String() || len(second.Events) != 4 || second.State != lifecycle.StateRouted {
		t.Fatalf("preparation did not converge on one route: %+v", second)
	}
	changed := input
	changed.Route.LimitPrice = decimalExecutionPointer("10.30")
	if _, err := fixture.repo.PrepareExecutionOrder(fixture.ctx, changed); err == nil {
		t.Fatal("changed route accepted under existing idempotency key")
	}
	stored, err := fixture.repo.GetExecutionLifecycle(fixture.ctx, fixture.account.ID, first.Intent.ID)
	if err != nil || len(stored.Events) != 4 || !stored.Order.LimitPrice.Equal(*input.Route.LimitPrice) {
		t.Fatalf("conflicting retry changed route: %+v, %v", stored, err)
	}
}

func TestPrepareExecutionOrderValidatesRouteBeforeWriting(t *testing.T) {
	fixture := newExecutionLifecycleFixture(t)
	input := executionPreparationFixture(t, fixture, "invalid-prepared-route")
	input.Route.VenueContract.ID = input.Proposal.Account.ID
	if _, err := fixture.repo.PrepareExecutionOrder(fixture.ctx, input); err == nil {
		t.Fatal("invalid venue binding accepted")
	}
	var count int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM execution_intents WHERE account_id=$1 AND idempotency_key=$2`, fixture.account.ID, input.Proposal.IdempotencyKey).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("invalid route left a persisted proposal")
	}
}

func executionPreparationFixture(t *testing.T, fixture executionLifecycleFixture, key string) ExecutionOrderPreparation {
	t.Helper()
	input := ExecutionOrderPreparation{Proposal: fixture.proposeInput(key), AllocatedQuantity: decimal.NewFromInt(8)}
	current := fixture.propose(t, key)
	input.Allocation = fixture.nextEvent(current, "allocation-"+key, "allocator", "allocation", "allocated", []byte(`{"quantity":"8"}`))
	allocation, err := lifecycle.Allocate(current, input.AllocatedQuantity, input.Allocation, input.Allocation.ReceivedAt)
	if err != nil {
		t.Fatal(err)
	}
	current, err = lifecycle.ApplyTransition(current, allocation)
	if err != nil {
		t.Fatal(err)
	}
	input.RiskApproval = fixture.nextEvent(current, "risk-"+key, "risk", "risk-policy-v1", "approved", []byte(`{"approved":true}`))
	approval, err := lifecycle.ApproveRisk(current, input.RiskApproval, input.RiskApproval.ReceivedAt)
	if err != nil {
		t.Fatal(err)
	}
	current, err = lifecycle.ApplyTransition(current, approval)
	if err != nil {
		t.Fatal(err)
	}
	artifact := newSimulationPolicyArtifact(t, "0")
	if _, err := NewSimulationPolicyRepo(fixture.pool).RegisterSimulationPolicy(fixture.ctx, artifact); err != nil {
		t.Fatal(err)
	}
	event := fixture.nextEvent(current, "route-"+key, "router", artifact.Version, "order_routed", []byte(`{"route":"simulation"}`))
	input.Route = lifecycle.RouteInput{
		OrderIdempotencyKey: "order-" + key, Instrument: *fixture.instrument, VenueContract: *fixture.contract,
		RouteSnapshot: *fixture.snapshot, QuoteRequirements: marketdata.QuoteRequirements{RequireSource: true, RequireVenueContract: true, RequireBid: true, RequireAsk: true},
		OrderType: lifecycle.OrderLimit, TimeInForce: lifecycle.TimeInForceDay, LimitPrice: decimalExecutionPointer("10.25"),
		PolicyKind: lifecycle.PolicySimulation, PolicyVersion: artifact.Version, Event: event, RoutedAt: event.ReceivedAt, CreatedAt: event.ReceivedAt,
	}
	return input
}
