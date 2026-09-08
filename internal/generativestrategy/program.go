package generativestrategy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/experimentrun"
	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

const (
	ScenarioAdapterKindV1    = "typed-generative-scenario"
	ScenarioAdapterVersionV1 = "v1"
	scenarioAdapterSchemaV1  = "typed-generative-scenario-adapter-v1"
)

type scenarioAdapterCanonical struct {
	Schema         string `json:"schema"`
	SpecID         string `json:"spec_id"`
	SpecSHA256     string `json:"spec_sha256"`
	ScenarioID     string `json:"scenario_id"`
	ScenarioSHA256 string `json:"scenario_sha256"`
}

type Program struct {
	identity *experimentrun.ProgramIdentity
	spec     *Spec
	version  *strategycatalog.Version
	scenario *Scenario
}

func ScenarioAdapterSHA256(spec *Spec, scenario *Scenario) string {
	if spec == nil || scenario == nil {
		return ""
	}
	raw, _ := json.Marshal(scenarioAdapterCanonical{Schema: scenarioAdapterSchemaV1, SpecID: spec.ID().String(), SpecSHA256: spec.Digest(), ScenarioID: scenario.ID().String(), ScenarioSHA256: scenario.Digest()})
	return hash(raw)
}

func NewProgram(identity *experimentrun.ProgramIdentity, spec *Spec, version *strategycatalog.Version, scenario *Scenario) (*Program, error) {
	if identity == nil || spec == nil || version == nil || scenario == nil || scenario.SpecID() != spec.ID() ||
		identity.VersionID() != version.ID() || identity.VersionSHA256() != version.Digest() || identity.CompilerKind() != version.CompilerKind() ||
		identity.CompilerVersion() != version.CompilerVersion() || identity.SourceCommit() != version.SourceCommit() || identity.SourceTreeSHA256() != version.SourceTreeSHA256() ||
		identity.DecisionContract() != version.DecisionContract() || identity.AdapterKind() != ScenarioAdapterKindV1 || identity.AdapterVersion() != ScenarioAdapterVersionV1 ||
		identity.AdapterSHA256() != ScenarioAdapterSHA256(spec, scenario) || identity.RunnerContract() != experimentrun.RunnerContractV1 {
		return nil, fmt.Errorf("generated strategy experiment program identity is invalid")
	}
	return &Program{identity: identity, spec: spec, version: version, scenario: scenario}, nil
}

func (program *Program) Identity() *experimentrun.ProgramIdentity {
	if program == nil {
		return nil
	}
	return program.identity
}

func (program *Program) Plan(ctx context.Context, input experimentrun.ProgramInput) (*experimentrun.Plan, error) {
	if program == nil || program.identity == nil || program.spec == nil || program.version == nil || program.scenario == nil {
		return nil, fmt.Errorf("generated strategy experiment program is incomplete")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if input.ExperimentID == uuid.Nil || input.AccountID == uuid.Nil || input.ManifestID != program.scenario.ManifestID() || input.EvaluationStart != scenarioFormatTime(program.scenario.EvaluationStart()) ||
		input.EvaluationEnd != scenarioFormatTime(program.scenario.EvaluationEnd()) || input.Mode != program.scenario.Mode() {
		return nil, fmt.Errorf("generated strategy program input does not match scenario")
	}
	expected := program.expectedEvidence()
	if len(input.Evidence) != len(expected) {
		return nil, fmt.Errorf("generated strategy program requires the exact scenario manifest evidence")
	}
	for index := range expected {
		if input.Evidence[index] != expected[index] {
			return nil, fmt.Errorf("generated strategy program requires exact ordered manifest evidence")
		}
	}
	equity, err := capitalEquity(input.CapitalStateBytes)
	if err != nil {
		return nil, err
	}
	steps := make([]experimentrun.StepInput, len(program.scenario.canonical.Frames))
	openQuantity := map[string]decimal.Decimal{}
	for index, frame := range program.scenario.canonical.Frames {
		execution := frame.Bindings[0]
		for _, binding := range frame.Bindings {
			if binding.Name == frame.ExecutionInput {
				execution = binding
				break
			}
		}
		decision, marshalErr := canonicalGeneratedDecision(map[string]any{
			"schema": "typed-generative-experiment-decision-v1", "spec_id": program.spec.ID().String(), "scenario_id": program.scenario.ID().String(),
			"scenario_sha256": program.scenario.Digest(), "sequence": frame.Sequence, "entry": frame.Entry, "exit": frame.Exit, "action": frame.Action,
			"execution_input": frame.ExecutionInput, "execution_price": frame.ExecutionPrice, "bindings": frame.Bindings,
		})
		if marshalErr != nil {
			return nil, marshalErr
		}
		step := experimentrun.StepInput{
			PartitionContentSHA256: execution.PartitionContentSHA256, ObservationSourceKey: execution.SourceKey,
			ObservationContentSHA256: execution.PayloadSHA256, AvailableAt: scenarioParseTime(execution.AvailableAt), Decision: decision, Action: experimentrun.ActionNoop,
		}
		if frame.Action != ScenarioNoop {
			price := decimal.RequireFromString(frame.ExecutionPrice)
			quantity := openQuantity[frame.InstrumentID]
			switch frame.Action {
			case ScenarioBuy:
				notional := decimal.RequireFromString(program.spec.canonical.Sizing.Value)
				maximum := decimal.RequireFromString(program.spec.canonical.Sizing.MaxPosition)
				if program.spec.canonical.Sizing.Mode == "fixed_fraction" {
					notional, maximum = equity.Mul(notional), equity.Mul(maximum)
				}
				if notional.GreaterThan(maximum) {
					notional = maximum
				}
				quantity = notional.Div(price).Truncate(8)
				if !quantity.IsPositive() {
					return nil, fmt.Errorf("generated strategy frame %d has zero executable quantity", index)
				}
				openQuantity[frame.InstrumentID] = quantity
			case ScenarioSell:
				if !quantity.IsPositive() {
					return nil, fmt.Errorf("generated strategy frame %d exits without an open quantity", index)
				}
				delete(openQuantity, frame.InstrumentID)
			default:
				return nil, fmt.Errorf("generated strategy frame %d has unsupported action %q", index, frame.Action)
			}
			side := string(frame.Action)
			instrumentID, _ := uuid.Parse(frame.InstrumentID)
			contractID, _ := uuid.Parse(frame.VenueContractID)
			limit := price.String()
			step.Action = experimentrun.ActionExecute
			step.Intent = &experimentrun.IntentSpecInput{
				InstrumentID: instrumentID, VenueContractID: contractID, Side: side, OrderType: "limit", TimeInForce: "day", Quantity: quantity.String(), LimitPrice: &limit,
				DecisionAt: scenarioParseTime(frame.DecisionAt), RouteAt: scenarioParseTime(frame.RouteAt),
			}
		}
		steps[index] = step
	}
	return experimentrun.NewPlan(experimentrun.PlanInput{
		ExperimentID: input.ExperimentID, ProgramID: program.identity.ID(), AccountID: input.AccountID,
		CapitalStateID: input.CapitalStateID, CapitalStateSHA256: input.CapitalStateSHA256, CapitalProjectionCheckpointID: input.CapitalProjectionCheckpointID,
		CapitalStateBytes: input.CapitalStateBytes, ManifestID: input.ManifestID, ManifestSHA256: input.ManifestSHA256,
		EvaluationStart: program.scenario.EvaluationStart(), EvaluationEnd: program.scenario.EvaluationEnd(), Seed: input.Seed, Mode: input.Mode, Steps: steps,
	})
}

func canonicalGeneratedDecision(value any) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var normalized map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

func (program *Program) expectedEvidence() []experimentrun.ObservationEvidence {
	seen := map[string]struct{}{}
	result := []experimentrun.ObservationEvidence{}
	for _, frame := range program.scenario.canonical.Frames {
		for _, binding := range frame.Bindings {
			key := binding.PartitionContentSHA256 + "\x00" + binding.SourceKey + "\x00" + binding.PayloadSHA256
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, experimentrun.ObservationEvidence{
				PartitionContentSHA256: binding.PartitionContentSHA256, SourceKey: binding.SourceKey,
				ContentSHA256: binding.PayloadSHA256, AvailableAt: binding.AvailableAt,
			})
		}
	}
	return result
}

func capitalEquity(raw json.RawMessage) (decimal.Decimal, error) {
	var state struct {
		Equity string `json:"equity"`
	}
	if err := json.Unmarshal(raw, &state); err != nil || state.Equity == "" {
		return decimal.Zero, fmt.Errorf("generated strategy program capital state lacks equity")
	}
	equity, err := exactDecimal(state.Equity)
	if err != nil || !equity.IsPositive() {
		return decimal.Zero, fmt.Errorf("generated strategy program capital equity is invalid")
	}
	return equity, nil
}

var _ experimentrun.Program = (*Program)(nil)
