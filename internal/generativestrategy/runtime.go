package generativestrategy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

const RuntimeBindingSchemaV1 = "typed-generative-runtime-binding-v1"

type runtimeBindingCanonical struct {
	Schema                   string `json:"schema"`
	SourceVersionID          string `json:"source_version_id"`
	SourceVersionSHA256      string `json:"source_version_sha256"`
	SourceCommit             string `json:"source_commit"`
	SourceTreeSHA256         string `json:"source_tree_sha256"`
	SpecID                   string `json:"spec_id"`
	SpecSHA256               string `json:"spec_sha256"`
	SpecCanonical            string `json:"spec_canonical"`
	TargetInstrumentID       string `json:"target_instrument_id"`
	ExpectedEdge             string `json:"expected_edge"`
	Confidence               string `json:"confidence"`
	DeploymentBudgetFraction string `json:"deployment_budget_fraction"`
}

type runtimeConfigEnvelope struct {
	GeneratedStrategy json.RawMessage `json:"generated_strategy"`
}

func HasRuntimeBinding(config json.RawMessage) bool {
	var envelope runtimeConfigEnvelope
	return len(config) != 0 && json.Unmarshal(config, &envelope) == nil && len(envelope.GeneratedStrategy) != 0
}

// RuntimeBinding is the reconstructable bridge between an approved typed
// strategy version and its deterministic scheduled evaluator.
type RuntimeBinding struct {
	canonical runtimeBindingCanonical
	spec      *Spec
	version   *strategycatalog.Version
}

func NewRuntimeBinding(spec *Spec, version *strategycatalog.Version, expectedEdge, confidence string) (*RuntimeBinding, error) {
	if spec == nil || version == nil || version.ID() == uuid.Nil || version.FamilyID() != spec.FamilyID() ||
		version.CompilerKind() != CompilerKindV1 || version.CompilerVersion() != CompilerVersionV1 ||
		version.ConfigSchema() != ConfigSchemaV1 || version.DecisionContract() != DecisionContractV1 {
		return nil, fmt.Errorf("generated runtime binding requires an exact typed source version")
	}
	rebuilt, _, err := Compile(spec, version.SourceCommit(), version.SourceTreeSHA256())
	if err != nil || rebuilt.ID() != version.ID() || rebuilt.Digest() != version.Digest() || !bytes.Equal(rebuilt.CanonicalBytes(), version.CanonicalBytes()) {
		return nil, fmt.Errorf("generated runtime source version does not reconstruct")
	}
	universe := spec.Universe()
	if len(universe.Instruments) != 1 {
		return nil, fmt.Errorf("generated runtime requires exactly one target instrument")
	}
	edge, edgeErr := exactDecimal(expectedEdge)
	confidenceValue, confidenceErr := exactDecimal(confidence)
	if edgeErr != nil || confidenceErr != nil || !edge.IsPositive() || confidenceValue.IsNegative() || confidenceValue.GreaterThan(decimal.NewFromInt(1)) {
		return nil, fmt.Errorf("generated runtime promotion statistics are invalid")
	}
	return &RuntimeBinding{canonical: runtimeBindingCanonical{
		Schema: RuntimeBindingSchemaV1, SourceVersionID: version.ID().String(), SourceVersionSHA256: version.Digest(),
		SourceCommit: version.SourceCommit(), SourceTreeSHA256: version.SourceTreeSHA256(), SpecID: spec.ID().String(), SpecSHA256: spec.Digest(),
		SpecCanonical: string(spec.CanonicalBytes()), TargetInstrumentID: universe.Instruments[0].String(), ExpectedEdge: edge.String(), Confidence: confidenceValue.String(),
		DeploymentBudgetFraction: "0.02",
	}, spec: spec, version: version}, nil
}

func ParseRuntimeBinding(config json.RawMessage) (*RuntimeBinding, error) {
	var envelope runtimeConfigEnvelope
	decoder := json.NewDecoder(bytes.NewReader(config))
	if len(config) == 0 || decoder.Decode(&envelope) != nil || len(envelope.GeneratedStrategy) == 0 {
		return nil, fmt.Errorf("generated runtime configuration is missing")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("generated runtime configuration has trailing content")
	}
	var canonical runtimeBindingCanonical
	decoder = json.NewDecoder(bytes.NewReader(envelope.GeneratedStrategy))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&canonical) != nil || canonical.Schema != RuntimeBindingSchemaV1 {
		return nil, fmt.Errorf("generated runtime binding is invalid")
	}
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("generated runtime binding has trailing content")
	}
	family, err := ReviewedDailyStockFamily()
	if err != nil {
		return nil, err
	}
	specID, specIDErr := uuid.Parse(canonical.SpecID)
	versionID, versionIDErr := uuid.Parse(canonical.SourceVersionID)
	targetID, targetIDErr := uuid.Parse(canonical.TargetInstrumentID)
	if specIDErr != nil || versionIDErr != nil || targetIDErr != nil || !digestPattern.MatchString(canonical.SpecSHA256) || !digestPattern.MatchString(canonical.SourceVersionSHA256) {
		return nil, fmt.Errorf("generated runtime binding identities are invalid")
	}
	spec, err := SpecFromCanonical(specID, canonical.SpecSHA256, []byte(canonical.SpecCanonical), family)
	if err != nil {
		return nil, fmt.Errorf("generated runtime spec: %w", err)
	}
	version, _, err := Compile(spec, canonical.SourceCommit, canonical.SourceTreeSHA256)
	if err != nil || version.ID() != versionID || version.Digest() != canonical.SourceVersionSHA256 || len(spec.Universe().Instruments) != 1 || spec.Universe().Instruments[0] != targetID {
		return nil, fmt.Errorf("generated runtime version binding does not reconstruct")
	}
	rebuilt, err := NewRuntimeBinding(spec, version, canonical.ExpectedEdge, canonical.Confidence)
	if err != nil || !bytes.Equal(mustJSON(rebuilt.canonical), mustJSON(canonical)) {
		return nil, fmt.Errorf("generated runtime binding is not canonical")
	}
	return rebuilt, nil
}

func (binding *RuntimeBinding) CanonicalBytes() json.RawMessage {
	if binding == nil {
		return nil
	}
	return mustJSON(binding.canonical)
}

func (binding *RuntimeBinding) SourceVersionID() uuid.UUID  { return binding.version.ID() }
func (binding *RuntimeBinding) SourceVersionDigest() string { return binding.version.Digest() }
func (binding *RuntimeBinding) Spec() *Spec                 { return binding.spec }
func (binding *RuntimeBinding) ExpectedEdge() float64 {
	value, _ := decimal.NewFromString(binding.canonical.ExpectedEdge)
	result, _ := value.Float64()
	return result
}

func (binding *RuntimeBinding) Confidence() float64 {
	value, _ := decimal.NewFromString(binding.canonical.Confidence)
	result, _ := value.Float64()
	return result
}
func (binding *RuntimeBinding) DeploymentBudgetFraction() float64 { return 0.02 }

type RuntimeBar struct {
	Timestamp time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
}

type RuntimeDecision struct {
	Action           string
	ExecutionPrice   float64
	ProposedNotional float64
	LiquidityUSD     float64
	SpreadPct        float64
}

func (binding *RuntimeBinding) EvaluateBar(now time.Time, bar RuntimeBar, holding bool, deploymentBudget float64) (RuntimeDecision, error) {
	if binding == nil || binding.spec == nil || now.IsZero() || bar.Timestamp.IsZero() || deploymentBudget <= 0 ||
		bar.Close <= 0 || bar.Open <= 0 || bar.High <= 0 || bar.Low <= 0 || bar.Volume < 0 {
		return RuntimeDecision{}, fmt.Errorf("generated runtime decision inputs are invalid")
	}
	fields := map[string]float64{"open": bar.Open, "high": bar.High, "low": bar.Low, "close": bar.Close, "volume": bar.Volume}
	values := make(map[string]string, len(binding.spec.canonical.Inputs))
	for _, input := range binding.spec.canonical.Inputs {
		value, ok := fields[input.Field]
		if !ok || input.Type != "decimal" || input.DatasetKind != "bars" || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) ||
			now.Before(bar.Timestamp) || now.Sub(bar.Timestamp) > time.Duration(input.FreshnessSeconds)*time.Second {
			return RuntimeDecision{}, fmt.Errorf("generated runtime input %q is unavailable or stale", input.Name)
		}
		values[input.Name] = decimal.NewFromFloat(value).String()
	}
	entry, exit, err := binding.spec.Evaluate(values)
	if err != nil {
		return RuntimeDecision{}, err
	}
	action := "noop"
	if entry && !exit && !holding {
		action = "buy"
	} else if exit && !entry && holding {
		action = "sell"
	}
	executionInput, err := binding.spec.PreferredExecutionInput()
	if err != nil {
		return RuntimeDecision{}, err
	}
	priceValue, err := decimal.NewFromString(values[executionInput])
	if err != nil {
		return RuntimeDecision{}, err
	}
	price, _ := priceValue.Float64()
	sizing := binding.spec.canonical.Sizing
	proposed := decimal.RequireFromString(sizing.Value)
	maximum := decimal.RequireFromString(sizing.MaxPosition)
	if sizing.Mode == "fixed_fraction" {
		equity := decimal.NewFromFloat(deploymentBudget).Div(decimal.RequireFromString(binding.canonical.DeploymentBudgetFraction))
		proposed, maximum = equity.Mul(proposed), equity.Mul(maximum)
	}
	if proposed.GreaterThan(maximum) {
		proposed = maximum
	}
	budget := decimal.NewFromFloat(deploymentBudget)
	if proposed.GreaterThan(budget) {
		proposed = budget
	}
	proposedFloat, _ := proposed.Float64()
	spreadBPS := decimal.RequireFromString(binding.spec.canonical.Costs.SpreadBPS)
	spread, _ := spreadBPS.Div(decimal.NewFromInt(10000)).Float64()
	return RuntimeDecision{Action: action, ExecutionPrice: price, ProposedNotional: proposedFloat, LiquidityUSD: bar.Close * bar.Volume, SpreadPct: spread}, nil
}

func mustJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}
