package generativestrategy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/economicid"
	"github.com/PatrickFanella/get-rich-quick/internal/experimentrun"
)

type experimentRunnerStub struct {
	request experimentrun.RunRequest
	result  *experimentrun.Result
	err     error
}

func (stub *experimentRunnerStub) Run(_ context.Context, request experimentrun.RunRequest) (*experimentrun.Result, error) {
	stub.request = request
	return stub.result, stub.err
}

func TestExecutorRunsOnlyExactPreparedResearch(t *testing.T) {
	preparer, request, _ := researchFixture(t)
	prepared, err := preparer.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	plan := mustPreparedPlan(t, prepared)
	result, err := experimentrun.NewResult(experimentrun.ResultInput{
		Plan: plan, AccountID: prepared.Experiment.AccountID(), QualityResultID: prepared.Experiment.QualityResultID(),
		SimulationPolicyVersion: prepared.Experiment.SimulationPolicyVersion(), CapitalPolicyVersion: prepared.Experiment.CapitalPolicyVersion(),
		Outcomes: []experimentrun.StepOutcomeInput{{Action: experimentrun.ActionNoop, DecisionSHA256: plan.DecisionSHA256(0), FilledQuantity: "0", FeeTotal: "0"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	stub := &experimentRunnerStub{result: result}
	executor, err := NewExecutor(stub)
	if err != nil {
		t.Fatal(err)
	}
	started := request.EvaluationEnd.Add(time.Second).UTC().Truncate(time.Microsecond)
	finished := started.Add(time.Second)
	attemptID := uuid.New()
	got, err := executor.Execute(context.Background(), ExecutionRequest{Prepared: prepared, AttemptID: attemptID, StartedAt: started, FinishedAt: finished})
	if err != nil || got.ID() != result.ID() || stub.request.ExperimentID != prepared.Experiment.ID() || stub.request.AttemptID != attemptID || stub.request.Program != prepared.Program {
		t.Fatalf("Execute() = %+v/%v request=%+v", got, err, stub.request)
	}
}

func TestExecutorRejectsInvalidGraphAndMismatchedResult(t *testing.T) {
	preparer, request, _ := researchFixture(t)
	prepared, err := preparer.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	stub := &experimentRunnerStub{}
	executor, _ := NewExecutor(stub)
	now := request.EvaluationEnd.Add(time.Second).UTC().Truncate(time.Microsecond)
	bad := *prepared
	bad.Scenario = nil
	if result, executeErr := executor.Execute(context.Background(), ExecutionRequest{Prepared: &bad, AttemptID: uuid.New(), StartedAt: now, FinishedAt: now}); executeErr == nil || result != nil {
		t.Fatalf("invalid graph result = %+v/%v", result, executeErr)
	}
	stub.err = errors.New("runner failed")
	if result, executeErr := executor.Execute(context.Background(), ExecutionRequest{Prepared: prepared, AttemptID: uuid.New(), StartedAt: now, FinishedAt: now}); executeErr == nil || result != nil {
		t.Fatalf("runner failure result = %+v/%v", result, executeErr)
	}
}

func mustPreparedPlan(t *testing.T, prepared *PreparedResearch) *experimentrun.Plan {
	t.Helper()
	identity := prepared.Program.Identity()
	capitalState := []byte(`{"fixture":true}`)
	capitalStateDigest := hash(capitalState)
	availableAt := prepared.Experiment.EvaluationStart().Add(time.Microsecond)
	plan, err := experimentrun.NewPlan(experimentrun.PlanInput{
		ExperimentID: prepared.Experiment.ID(), ProgramID: identity.ID(), AccountID: prepared.Experiment.AccountID(),
		CapitalStateID: economicid.DeterministicUUID("capital-state", capitalStateDigest), CapitalStateSHA256: capitalStateDigest, CapitalProjectionCheckpointID: uuid.New(),
		CapitalStateBytes: capitalState, ManifestID: prepared.Experiment.ManifestID(), ManifestSHA256: prepared.Scenario.ManifestDigest(),
		EvaluationStart: prepared.Experiment.EvaluationStart(), EvaluationEnd: prepared.Experiment.EvaluationEnd(), Seed: prepared.Experiment.Seed(), Mode: prepared.Experiment.Mode(),
		Steps: []experimentrun.StepInput{{
			PartitionContentSHA256: stringOf("a", 64), ObservationSourceKey: "fixture", ObservationContentSHA256: stringOf("b", 64),
			AvailableAt: availableAt, Decision: []byte(`{"signal":"noop"}`), Action: experimentrun.ActionNoop,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func stringOf(value string, count int) string {
	result := ""
	for range count {
		result += value
	}
	return result
}
