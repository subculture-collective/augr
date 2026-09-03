package generativestrategy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/economicid"
	"github.com/PatrickFanella/get-rich-quick/internal/experimentrun"
)

func programFixture(t *testing.T) (*Program, experimentrun.ProgramInput) {
	t.Helper()
	spec, scenarioInput, _ := scenarioFixture(t)
	scenario, err := NewScenario(scenarioInput)
	if err != nil {
		t.Fatal(err)
	}
	version, _, err := Compile(spec, strings.Repeat("b", 40), strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := experimentrun.NewProgramIdentity(experimentrun.ProgramIdentityInput{
		VersionID: version.ID(), VersionSHA256: version.Digest(), CompilerKind: version.CompilerKind(), CompilerVersion: version.CompilerVersion(),
		SourceCommit: version.SourceCommit(), SourceTreeSHA256: version.SourceTreeSHA256(), DecisionContract: version.DecisionContract(),
		AdapterKind: ScenarioAdapterKindV1, AdapterVersion: ScenarioAdapterVersionV1, AdapterSHA256: ScenarioAdapterSHA256(spec, scenario), RunnerContract: experimentrun.RunnerContractV1,
	})
	if err != nil {
		t.Fatal(err)
	}
	program, err := NewProgram(identity, spec, version, scenario)
	if err != nil {
		t.Fatal(err)
	}
	capitalBytes := json.RawMessage(`{"equity":"10000"}`)
	sum := sha256.Sum256(capitalBytes)
	capitalDigest := hex.EncodeToString(sum[:])
	input := experimentrun.ProgramInput{
		ExperimentID: uuid.New(), AccountID: uuid.New(), CapitalStateID: economicid.DeterministicUUID("capital-state", capitalDigest), CapitalStateSHA256: capitalDigest,
		CapitalProjectionCheckpointID: uuid.New(), CapitalStateBytes: capitalBytes, ManifestID: uuid.New(), ManifestSHA256: strings.Repeat("e", 64),
		EvaluationStart: scenarioFormatTime(scenario.EvaluationStart()), EvaluationEnd: scenarioFormatTime(scenario.EvaluationEnd()), Seed: 42, Mode: scenario.Mode(), Evidence: program.expectedEvidence(),
	}
	return program, input
}

func TestGeneratedProgramBuildsDeterministicBuyAndExitPlan(t *testing.T) {
	t.Parallel()
	program, input := programFixture(t)
	plan, err := program.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	steps := plan.Steps()
	if len(steps) != 2 || steps[0].Action != experimentrun.ActionExecute || steps[1].Action != experimentrun.ActionExecute ||
		steps[0].Intent == nil || steps[1].Intent == nil || steps[0].Intent.Side != "buy" || steps[1].Intent.Side != "sell" ||
		steps[0].Intent.Quantity != "9.90099009" || steps[1].Intent.Quantity != steps[0].Intent.Quantity {
		t.Fatalf("steps = %+v", steps)
	}
	second, err := program.Plan(context.Background(), input)
	if err != nil || second.Digest() != plan.Digest() {
		t.Fatalf("second plan = %v, %v", second, err)
	}
}

func TestGeneratedProgramRejectsManifestOrCapitalSubstitution(t *testing.T) {
	t.Parallel()
	program, input := programFixture(t)
	input.Evidence[0].ContentSHA256 = strings.Repeat("f", 64)
	if _, err := program.Plan(context.Background(), input); err == nil {
		t.Fatal("changed manifest evidence accepted")
	}
	_, input = programFixture(t)
	input.CapitalStateBytes = json.RawMessage(`{"equity":"0"}`)
	if _, err := program.Plan(context.Background(), input); err == nil {
		t.Fatal("invalid capital accepted")
	}
}
