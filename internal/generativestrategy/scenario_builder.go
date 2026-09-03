package generativestrategy

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

type ScenarioBuildRequest struct {
	Spec             *Spec
	Dataset          *dataset.BoundMarketDataset
	Mode             strategycatalog.ExperimentMode
	EvaluationStart  time.Time
	EvaluationEnd    time.Time
	ExecutionInput   string
	VenueContractIDs map[uuid.UUID]uuid.UUID
	MaximumFrames    int
}

type boundScenarioPayload struct {
	payload   *dataset.MarketPayload
	evidence  ScenarioEvidenceInput
	available time.Time
}

// BuildScenarioFromDataset derives decision frames only from one explicitly
// supplied, fully bound immutable dataset. It never fetches data, reads a
// mutable cache, or chooses a manifest by recency.
func BuildScenarioFromDataset(request ScenarioBuildRequest) (*Scenario, error) {
	if request.Spec == nil || request.Dataset == nil || request.Dataset.Manifest() == nil || request.ExecutionInput == "" ||
		request.MaximumFrames <= 0 || len(request.VenueContractIDs) == 0 ||
		(request.Spec.canonical.Universe.AssetClass != instrument.AssetClassEquity && request.Spec.canonical.Universe.AssetClass != instrument.AssetClassETF) {
		return nil, fmt.Errorf("generated strategy scenario builder requires an exact equity dataset, execution input, contracts, and frame bound")
	}
	executionDeclaration, ok := scenarioInputDeclaration(request.Spec, request.ExecutionInput)
	if !ok || executionDeclaration.Type != "decimal" {
		return nil, fmt.Errorf("generated strategy execution input is not a declared decimal")
	}
	manifest := request.Dataset.Manifest()
	byPayloadID, err := scenarioPayloadEvidence(request.Dataset)
	if err != nil {
		return nil, err
	}
	allowed := map[uuid.UUID]struct{}{}
	for _, raw := range request.Spec.canonical.Universe.Instruments {
		allowed[uuid.MustParse(raw)] = struct{}{}
	}
	available := make([]boundScenarioPayload, 0, len(byPayloadID))
	for _, payload := range request.Dataset.Payloads() {
		if payload == nil {
			return nil, fmt.Errorf("generated strategy bound dataset contains a nil payload")
		}
		if _, inUniverse := allowed[payload.InstrumentID()]; !inUniverse || payload.AvailableAt().Before(request.EvaluationStart) || !payload.AvailableAt().Before(request.EvaluationEnd) {
			continue
		}
		available = append(available, boundScenarioPayload{payload: payload, evidence: byPayloadID[payload.ID()], available: payload.AvailableAt()})
	}
	sort.Slice(available, func(i, j int) bool {
		if !available[i].available.Equal(available[j].available) {
			return available[i].available.Before(available[j].available)
		}
		if available[i].payload.InstrumentID() != available[j].payload.InstrumentID() {
			return available[i].payload.InstrumentID().String() < available[j].payload.InstrumentID().String()
		}
		return available[i].payload.Digest() < available[j].payload.Digest()
	})
	frames := make([]DecisionFrameInput, 0)
	for _, candidate := range available {
		if _, fieldErr := candidate.payload.Field(executionDeclaration.DatasetKind, executionDeclaration.Field); fieldErr != nil {
			continue
		}
		contractID := request.VenueContractIDs[candidate.payload.InstrumentID()]
		if contractID == uuid.Nil {
			return nil, fmt.Errorf("generated strategy instrument %s lacks an exact venue contract", candidate.payload.InstrumentID())
		}
		frame := DecisionFrameInput{
			InstrumentID: candidate.payload.InstrumentID(), VenueContractID: contractID, DecisionAt: candidate.available, RouteAt: candidate.available,
			ExecutionInput: request.ExecutionInput, EvidenceByInput: map[string]ScenarioEvidenceInput{},
		}
		for _, declaration := range request.Spec.canonical.Inputs {
			selected, selectErr := selectScenarioInput(available, candidate.payload.InstrumentID(), candidate.available, declaration)
			if selectErr != nil {
				return nil, fmt.Errorf("generated strategy frame %s/%s input %q: %w", candidate.payload.InstrumentID(), scenarioFormatTime(candidate.available), declaration.Name, selectErr)
			}
			frame.EvidenceByInput[declaration.Name] = selected.evidence
		}
		frames = append(frames, frame)
		if len(frames) > request.MaximumFrames {
			return nil, fmt.Errorf("generated strategy scenario exceeds maximum frame count %d", request.MaximumFrames)
		}
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("generated strategy exact dataset produced no executable decision frames")
	}
	return NewScenario(ScenarioInput{Spec: request.Spec, Manifest: manifest, Mode: request.Mode, EvaluationStart: request.EvaluationStart, EvaluationEnd: request.EvaluationEnd, Frames: frames})
}

func scenarioInputDeclaration(spec *Spec, name string) (inputCanonical, bool) {
	if spec == nil {
		return inputCanonical{}, false
	}
	index := sort.Search(len(spec.canonical.Inputs), func(i int) bool { return spec.canonical.Inputs[i].Name >= name })
	if index == len(spec.canonical.Inputs) || spec.canonical.Inputs[index].Name != name {
		return inputCanonical{}, false
	}
	return spec.canonical.Inputs[index], true
}

func scenarioPayloadEvidence(bound *dataset.BoundMarketDataset) (map[uuid.UUID]ScenarioEvidenceInput, error) {
	manifest := bound.Manifest()
	result := make(map[uuid.UUID]ScenarioEvidenceInput, len(bound.Payloads()))
	payloads := map[uuid.UUID]*dataset.MarketPayload{}
	for _, payload := range bound.Payloads() {
		payloads[payload.ID()] = payload
	}
	for _, binding := range bound.Bindings() {
		partitions := manifest.Partitions()
		if binding.PartitionSequence < 0 || binding.PartitionSequence >= len(partitions) {
			return nil, fmt.Errorf("generated strategy dataset binding partition is invalid")
		}
		partition := partitions[binding.PartitionSequence]
		if binding.ObservationSequence < 0 || binding.ObservationSequence >= len(partition.Observations) {
			return nil, fmt.Errorf("generated strategy dataset binding observation is invalid")
		}
		observation := partition.Observations[binding.ObservationSequence]
		payload := payloads[binding.PayloadID]
		if payload == nil || observation.ContentSHA256 != payload.Digest() || binding.ContentSHA256 != payload.Digest() {
			return nil, fmt.Errorf("generated strategy dataset binding does not reconstruct")
		}
		result[payload.ID()] = ScenarioEvidenceInput{Payload: payload, PartitionContentSHA256: partition.ContentSHA256, SourceKey: observation.SourceKey}
	}
	if len(result) != len(payloads) {
		return nil, fmt.Errorf("generated strategy dataset contains an unbound payload")
	}
	return result, nil
}

func selectScenarioInput(available []boundScenarioPayload, instrumentID uuid.UUID, decisionAt time.Time, declaration inputCanonical) (boundScenarioPayload, error) {
	var selected *boundScenarioPayload
	for index := range available {
		candidate := &available[index]
		if candidate.payload.InstrumentID() != instrumentID || candidate.available.After(decisionAt) || decisionAt.Sub(candidate.available) > time.Duration(declaration.FreshnessSeconds)*time.Second {
			continue
		}
		if _, err := candidate.payload.Field(declaration.DatasetKind, declaration.Field); err != nil {
			continue
		}
		if selected == nil || candidate.available.After(selected.available) {
			selected = candidate
			continue
		}
		if candidate.available.Equal(selected.available) && candidate.payload.ID() != selected.payload.ID() {
			return boundScenarioPayload{}, fmt.Errorf("multiple immutable revisions are equally current")
		}
	}
	if selected == nil {
		return boundScenarioPayload{}, fmt.Errorf("no complete fresh immutable payload exists")
	}
	return *selected, nil
}
