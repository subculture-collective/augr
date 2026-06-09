package latency

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestSimulateRejectsHighLatency(t *testing.T) {
	got := Simulate(SimulationInput{
		LatencyMS:            95,
		ResolutionWindowMS:   100,
		StaleBookProbability: 0,
		ReversalProbability:  0,
		Stake:                100,
		EdgeBeforeLatency:    1,
		MaxLatencyMS:         80,
		MaxExpectedLoss:      1000,
	})

	if got.Accepted {
		t.Fatal("Accepted = true, want false")
	}
	if !hasReason(got.Reasons, "high_latency") {
		t.Fatalf("Reasons = %v, want high_latency", got.Reasons)
	}
	if got.ExpectedLoss != 0 || got.NetEdgeAfterLatency != 100 || got.StalePenalty != 0 || got.ReversalPenalty != 0 {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestSimulateAppliesStaleBookPenalty(t *testing.T) {
	got := Simulate(SimulationInput{
		LatencyMS:            40,
		ResolutionWindowMS:   100,
		StaleBookProbability: 0.6,
		ReversalProbability:  0,
		Stake:                50,
		EdgeBeforeLatency:    0.5,
		MaxLatencyMS:         200,
		MaxExpectedLoss:      1000,
	})

	if got.StalePenalty <= 0 {
		t.Fatalf("StalePenalty = %v, want > 0", got.StalePenalty)
	}
	if !hasReason(got.Reasons, "stale_book_penalty") {
		t.Fatalf("Reasons = %v, want stale_book_penalty", got.Reasons)
	}
	if got.Accepted {
		t.Fatal("Accepted = true, want false")
	}
}

func TestSimulateRejectsNonFiniteInput(t *testing.T) {
	got := Simulate(SimulationInput{
		LatencyMS:            math.NaN(),
		ResolutionWindowMS:   100,
		StaleBookProbability: 0.1,
		ReversalProbability:  0.1,
		Stake:                math.Inf(1),
		EdgeBeforeLatency:    0.2,
		MaxLatencyMS:         100,
		MaxExpectedLoss:      10,
	})

	if got.Accepted {
		t.Fatal("Accepted = true, want false")
	}
	if !hasReason(got.Reasons, "invalid_input") {
		t.Fatalf("Reasons = %v, want invalid_input", got.Reasons)
	}
	if got.ExpectedLoss != 0 || got.NetEdgeAfterLatency != 0 || got.StalePenalty != 0 || got.ReversalPenalty != 0 {
		t.Fatalf("unexpected zero-safe result: %+v", got)
	}
	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
}

func TestSimulateIsDeterministicAndJSONSafe(t *testing.T) {
	input := SimulationInput{
		LatencyMS:            10,
		ResolutionWindowMS:   100,
		StaleBookProbability: 0,
		ReversalProbability:  0,
		Stake:                10,
		EdgeBeforeLatency:    0.1,
		MaxLatencyMS:         50,
		MaxExpectedLoss:      10,
	}

	got1 := Simulate(input)
	got2 := Simulate(input)

	if !reflect.DeepEqual(got1, got2) {
		t.Fatalf("results differ: %+v vs %+v", got1, got2)
	}
	for _, v := range []float64{got1.ExpectedLoss, got1.NetEdgeAfterLatency, got1.StalePenalty, got1.ReversalPenalty} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("non-finite value: %v", v)
		}
	}
	if _, err := json.Marshal(got1); err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
}

func TestSimulatorSourceHasNoForbiddenImportsOrTerms(t *testing.T) {
	data, err := os.ReadFile("simulator.go")
	if err != nil {
		t.Fatalf("ReadFile(simulator.go) error = %v", err)
	}

	text := string(data)
	for _, forbidden := range []string{
		"internal/execution",
		"internal/order",
		"live-trading",
		"execution",
		"order",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("simulator.go contains forbidden text %q", forbidden)
		}
	}

	fset := token.NewFileSet()
	fileNode, err := parser.ParseFile(fset, "simulator.go", data, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("ParseFile(simulator.go) error = %v", err)
	}
	if len(fileNode.Imports) != 1 {
		t.Fatalf("len(imports) = %d, want 1", len(fileNode.Imports))
	}
	path, err := strconv.Unquote(fileNode.Imports[0].Path.Value)
	if err != nil {
		t.Fatalf("unquote import %q: %v", fileNode.Imports[0].Path.Value, err)
	}
	if path != "math" {
		t.Fatalf("import path = %q, want math", path)
	}
}

func hasReason(reasons []string, want string) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}
