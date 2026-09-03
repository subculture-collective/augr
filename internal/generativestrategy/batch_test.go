package generativestrategy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/experimentrun"
)

type eligibleResearchSourceStub struct {
	items []EligibleResearch
	err   error
}

func (stub eligibleResearchSourceStub) ListEligibleGeneratedResearch(context.Context, uuid.UUID, uuid.UUID, int, time.Time) ([]EligibleResearch, error) {
	return stub.items, stub.err
}

func TestBatchServiceExecutesExactAccountScopeItems(t *testing.T) {
	preparer, request, _ := researchFixture(t)
	prepared, err := preparer.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	plan := mustPreparedPlan(t, prepared)
	result := mustResultForPlan(t, prepared, plan)
	runner := &experimentRunnerStub{result: result}
	executor, _ := NewExecutor(runner)
	now := request.EvaluationEnd.Add(2 * time.Second).UTC().Truncate(time.Microsecond)
	scopeID := uuid.New()
	item := EligibleResearch{ScopeID: scopeID, Prepared: prepared, AttemptID: uuid.New(), StartedAt: now.Add(-time.Second), FinishedAt: now}
	service, err := NewBatchService(eligibleResearchSourceStub{items: []EligibleResearch{item}}, executor)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := service.RunEligible(context.Background(), request.AccountID, scopeID, 10, now)
	if err != nil || summary != (BatchSummary{Eligible: 1, Completed: 1}) || runner.request.ExperimentID != prepared.Experiment.ID() {
		t.Fatalf("RunEligible() = %+v/%v request=%+v", summary, err, runner.request)
	}
}

func TestBatchServiceFailsClosedOnScopeDuplicateAndExecutionFailure(t *testing.T) {
	preparer, request, _ := researchFixture(t)
	prepared, err := preparer.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	now := request.EvaluationEnd.Add(2 * time.Second).UTC().Truncate(time.Microsecond)
	scopeID, attemptID := uuid.New(), uuid.New()
	item := EligibleResearch{ScopeID: scopeID, Prepared: prepared, AttemptID: attemptID, StartedAt: now, FinishedAt: now}
	tests := []struct {
		name   string
		items  []EligibleResearch
		runner *experimentRunnerStub
	}{
		{name: "wrong scope", items: []EligibleResearch{func() EligibleResearch { changed := item; changed.ScopeID = uuid.New(); return changed }()}, runner: &experimentRunnerStub{}},
		{name: "duplicate", items: []EligibleResearch{item, item}, runner: &experimentRunnerStub{result: mustResultForPlan(t, prepared, mustPreparedPlan(t, prepared))}},
		{name: "execution failure", items: []EligibleResearch{item}, runner: &experimentRunnerStub{err: errors.New("injected")}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			executor, _ := NewExecutor(testCase.runner)
			service, _ := NewBatchService(eligibleResearchSourceStub{items: testCase.items}, executor)
			summary, runErr := service.RunEligible(context.Background(), request.AccountID, scopeID, 10, now)
			if runErr == nil || summary.Failed != 1 {
				t.Fatalf("RunEligible() = %+v/%v", summary, runErr)
			}
		})
	}
}

func mustResultForPlan(t *testing.T, prepared *PreparedResearch, plan *experimentrun.Plan) *experimentrun.Result {
	t.Helper()
	result, err := experimentrun.NewResult(experimentrun.ResultInput{
		Plan: plan, AccountID: prepared.Experiment.AccountID(), QualityResultID: prepared.Experiment.QualityResultID(),
		SimulationPolicyVersion: prepared.Experiment.SimulationPolicyVersion(), CapitalPolicyVersion: prepared.Experiment.CapitalPolicyVersion(),
		Outcomes: []experimentrun.StepOutcomeInput{{Action: experimentrun.ActionNoop, DecisionSHA256: plan.DecisionSHA256(0), FilledQuantity: "0", FeeTotal: "0"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
