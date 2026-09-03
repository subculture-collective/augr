package generativestrategy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/economicid"
	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

const ScenarioSchemaV1 = "typed-generative-strategy-scenario-v1"

type ScenarioAction string

const (
	ScenarioNoop ScenarioAction = "noop"
	ScenarioBuy  ScenarioAction = "buy"
	ScenarioSell ScenarioAction = "sell"
)

type DecisionFrameInput struct {
	InstrumentID    uuid.UUID
	VenueContractID uuid.UUID
	DecisionAt      time.Time
	RouteAt         time.Time
	ExecutionInput  string
	PayloadsByInput map[string]*dataset.MarketPayload
}

type ScenarioInput struct {
	Spec            *Spec
	Mode            strategycatalog.ExperimentMode
	EvaluationStart time.Time
	EvaluationEnd   time.Time
	Frames          []DecisionFrameInput
}

type scenarioBindingCanonical struct {
	Name          string       `json:"name"`
	DatasetKind   dataset.Kind `json:"dataset_kind"`
	Field         string       `json:"field"`
	PayloadID     string       `json:"payload_id"`
	PayloadSHA256 string       `json:"payload_sha256"`
	AvailableAt   string       `json:"available_at"`
	Value         string       `json:"value"`
}

type scenarioFrameCanonical struct {
	Sequence        int                        `json:"sequence"`
	InstrumentID    string                     `json:"instrument_id"`
	VenueContractID string                     `json:"venue_contract_id"`
	DecisionAt      string                     `json:"decision_at"`
	RouteAt         string                     `json:"route_at"`
	ExecutionInput  string                     `json:"execution_input"`
	Bindings        []scenarioBindingCanonical `json:"bindings"`
	Entry           bool                       `json:"entry"`
	Exit            bool                       `json:"exit"`
	Action          ScenarioAction             `json:"action"`
	ExecutionPrice  string                     `json:"execution_price"`
}

type scenarioCanonical struct {
	Schema          string                         `json:"schema"`
	State           string                         `json:"state"`
	SpecID          string                         `json:"spec_id"`
	SpecSHA256      string                         `json:"spec_sha256"`
	Mode            strategycatalog.ExperimentMode `json:"mode"`
	EvaluationStart string                         `json:"evaluation_start"`
	EvaluationEnd   string                         `json:"evaluation_end"`
	Frames          []scenarioFrameCanonical       `json:"frames"`
}

type Scenario struct {
	canonical scenarioCanonical
	bytes     json.RawMessage
	digest    string
	id        uuid.UUID
}

func NewScenario(input ScenarioInput) (*Scenario, error) {
	if input.Spec == nil || (input.Mode != strategycatalog.ExperimentPaperScored && input.Mode != strategycatalog.ExperimentPaperStress) ||
		!scenarioTime(input.EvaluationStart) || !scenarioTime(input.EvaluationEnd) || !input.EvaluationStart.Before(input.EvaluationEnd) ||
		len(input.Frames) == 0 || len(input.Frames) > 100000 {
		return nil, fmt.Errorf("generated strategy scenario identity is invalid")
	}
	allowedInstruments := make(map[uuid.UUID]struct{}, len(input.Spec.canonical.Universe.Instruments))
	for _, raw := range input.Spec.canonical.Universe.Instruments {
		allowedInstruments[uuid.MustParse(raw)] = struct{}{}
	}
	frames := make([]scenarioFrameCanonical, len(input.Frames))
	open := map[uuid.UUID]bool{}
	lastDecision := time.Time{}
	for sequence, source := range input.Frames {
		if source.InstrumentID == uuid.Nil || source.VenueContractID == uuid.Nil || !scenarioTime(source.DecisionAt) || !scenarioTime(source.RouteAt) ||
			source.DecisionAt.Before(input.EvaluationStart) || !source.DecisionAt.Before(input.EvaluationEnd) || source.RouteAt.Before(source.DecisionAt) || source.RouteAt.After(input.EvaluationEnd) ||
			(!lastDecision.IsZero() && source.DecisionAt.Before(lastDecision)) {
			return nil, fmt.Errorf("generated strategy scenario frame %d timing or identity is invalid", sequence)
		}
		if _, ok := allowedInstruments[source.InstrumentID]; !ok {
			return nil, fmt.Errorf("generated strategy scenario frame %d escapes the declared universe", sequence)
		}
		lastDecision = source.DecisionAt
		if len(source.PayloadsByInput) != len(input.Spec.canonical.Inputs) {
			return nil, fmt.Errorf("generated strategy scenario frame %d requires every declared input", sequence)
		}
		values := make(map[string]string, len(input.Spec.canonical.Inputs))
		bindings := make([]scenarioBindingCanonical, 0, len(input.Spec.canonical.Inputs))
		for _, declaration := range input.Spec.canonical.Inputs {
			payload := source.PayloadsByInput[declaration.Name]
			if payload == nil || payload.InstrumentID() != source.InstrumentID || payload.AvailableAt().After(source.DecisionAt) ||
				source.DecisionAt.Sub(payload.AvailableAt()) > time.Duration(declaration.FreshnessSeconds)*time.Second {
				return nil, fmt.Errorf("generated strategy scenario input %q is missing, stale, future, or cross-instrument", declaration.Name)
			}
			value, err := payload.Field(declaration.DatasetKind, declaration.Field)
			if err != nil {
				return nil, fmt.Errorf("generated strategy scenario input %q: %w", declaration.Name, err)
			}
			if declaration.Type == "decimal" {
				if _, err = exactDecimal(value); err != nil {
					return nil, fmt.Errorf("generated strategy scenario input %q is not canonical decimal", declaration.Name)
				}
			} else if value != "true" && value != "false" {
				return nil, fmt.Errorf("generated strategy scenario input %q is not canonical boolean", declaration.Name)
			}
			values[declaration.Name] = value
			bindings = append(bindings, scenarioBindingCanonical{Name: declaration.Name, DatasetKind: declaration.DatasetKind, Field: declaration.Field,
				PayloadID: payload.ID().String(), PayloadSHA256: payload.Digest(), AvailableAt: scenarioFormatTime(payload.AvailableAt()), Value: value})
		}
		entry, exit, err := input.Spec.Evaluate(values)
		if err != nil {
			return nil, fmt.Errorf("generated strategy scenario frame %d: %w", sequence, err)
		}
		executionIndex := sort.Search(len(bindings), func(i int) bool { return bindings[i].Name >= source.ExecutionInput })
		if executionIndex == len(bindings) || bindings[executionIndex].Name != source.ExecutionInput {
			return nil, fmt.Errorf("generated strategy scenario frame %d execution input is not declared", sequence)
		}
		executionPrice, err := exactDecimal(bindings[executionIndex].Value)
		if err != nil || !executionPrice.IsPositive() {
			return nil, fmt.Errorf("generated strategy scenario frame %d execution price is invalid", sequence)
		}
		action := ScenarioNoop
		if entry && !exit && !open[source.InstrumentID] {
			action, open[source.InstrumentID] = ScenarioBuy, true
		} else if exit && !entry && open[source.InstrumentID] {
			action, open[source.InstrumentID] = ScenarioSell, false
		}
		frames[sequence] = scenarioFrameCanonical{Sequence: sequence, InstrumentID: source.InstrumentID.String(), VenueContractID: source.VenueContractID.String(),
			DecisionAt: scenarioFormatTime(source.DecisionAt), RouteAt: scenarioFormatTime(source.RouteAt), ExecutionInput: source.ExecutionInput,
			Bindings: bindings, Entry: entry, Exit: exit, Action: action, ExecutionPrice: executionPrice.String()}
	}
	canonical := scenarioCanonical{Schema: ScenarioSchemaV1, State: "derived", SpecID: input.Spec.ID().String(), SpecSHA256: input.Spec.Digest(), Mode: input.Mode,
		EvaluationStart: scenarioFormatTime(input.EvaluationStart), EvaluationEnd: scenarioFormatTime(input.EvaluationEnd), Frames: frames}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, err
	}
	digest := hash(encoded)
	return &Scenario{canonical: canonical, bytes: encoded, digest: digest, id: economicid.DeterministicUUID("typed-generative-strategy-scenario", ScenarioSchemaV1+"@sha256:"+digest)}, nil
}

func ScenarioFromCanonical(id uuid.UUID, digest string, raw []byte, spec *Spec, payloads map[uuid.UUID]*dataset.MarketPayload) (*Scenario, error) {
	if id == uuid.Nil || spec == nil || !digestPattern.MatchString(digest) || hash(raw) != digest {
		return nil, fmt.Errorf("generated strategy scenario envelope is invalid")
	}
	var canonical scenarioCanonical
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&canonical); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("generated strategy scenario has extra JSON")
	}
	input := ScenarioInput{Spec: spec, Mode: canonical.Mode, EvaluationStart: scenarioParseTime(canonical.EvaluationStart), EvaluationEnd: scenarioParseTime(canonical.EvaluationEnd)}
	for sequence, frame := range canonical.Frames {
		if frame.Sequence != sequence {
			return nil, fmt.Errorf("generated strategy scenario frame sequence is invalid")
		}
		instrumentID, instrumentErr := uuid.Parse(frame.InstrumentID)
		contractID, contractErr := uuid.Parse(frame.VenueContractID)
		if instrumentErr != nil || contractErr != nil {
			return nil, fmt.Errorf("generated strategy scenario frame identity is invalid")
		}
		rebuilt := DecisionFrameInput{InstrumentID: instrumentID, VenueContractID: contractID, DecisionAt: scenarioParseTime(frame.DecisionAt), RouteAt: scenarioParseTime(frame.RouteAt), ExecutionInput: frame.ExecutionInput, PayloadsByInput: map[string]*dataset.MarketPayload{}}
		for _, binding := range frame.Bindings {
			payloadID, err := uuid.Parse(binding.PayloadID)
			payload := payloads[payloadID]
			if err != nil || payload == nil || payload.Digest() != binding.PayloadSHA256 {
				return nil, fmt.Errorf("generated strategy scenario payload does not reconstruct")
			}
			rebuilt.PayloadsByInput[binding.Name] = payload
		}
		input.Frames = append(input.Frames, rebuilt)
	}
	rebuilt, err := NewScenario(input)
	if err != nil || canonical.Schema != ScenarioSchemaV1 || canonical.State != "derived" || canonical.SpecID != spec.ID().String() || canonical.SpecSHA256 != spec.Digest() ||
		rebuilt == nil || rebuilt.ID() != id || rebuilt.Digest() != digest || !bytes.Equal(rebuilt.bytes, raw) {
		return nil, fmt.Errorf("generated strategy scenario does not reconstruct")
	}
	return rebuilt, nil
}

func (scenario *Scenario) ID() uuid.UUID {
	if scenario == nil {
		return uuid.Nil
	}
	return scenario.id
}
func (scenario *Scenario) Digest() string {
	if scenario == nil {
		return ""
	}
	return scenario.digest
}
func (scenario *Scenario) CanonicalBytes() json.RawMessage {
	if scenario == nil {
		return nil
	}
	return append(json.RawMessage(nil), scenario.bytes...)
}
func (scenario *Scenario) SpecID() uuid.UUID {
	if scenario == nil {
		return uuid.Nil
	}
	return uuid.MustParse(scenario.canonical.SpecID)
}
func (scenario *Scenario) Mode() strategycatalog.ExperimentMode {
	if scenario == nil {
		return ""
	}
	return scenario.canonical.Mode
}
func (scenario *Scenario) EvaluationStart() time.Time {
	if scenario == nil {
		return time.Time{}
	}
	return scenarioParseTime(scenario.canonical.EvaluationStart)
}
func (scenario *Scenario) EvaluationEnd() time.Time {
	if scenario == nil {
		return time.Time{}
	}
	return scenarioParseTime(scenario.canonical.EvaluationEnd)
}

func scenarioTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC && value.Equal(value.Truncate(time.Microsecond))
}
func scenarioFormatTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000000Z")
}
func scenarioParseTime(value string) time.Time {
	parsed, _ := time.Parse("2006-01-02T15:04:05.000000Z", value)
	return parsed
}
