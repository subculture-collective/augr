package solverarb

import (
	"go/parser"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestEvaluateCompleteSetRejectsNegativeEdge(t *testing.T) {
	got := EvaluateCompleteSet(OpportunityInput{
		MarketID: "market-1",
		Outcomes: []OutcomeQuote{
			{Outcome: "YES", AskPrice: 0.62, Size: 1},
			{Outcome: "NO", AskPrice: 0.55, Size: 1},
		},
		FeeRate:            0,
		PartialFillHaircut: 0.5,
		MinNetEdge:         0.01,
	})

	if got.Accepted {
		t.Fatalf("Accepted = true, want false")
	}
	if got.GrossEdge >= 0 {
		t.Fatalf("GrossEdge = %v, want negative", got.GrossEdge)
	}
	if got.HaircutCost != 0 {
		t.Fatalf("HaircutCost = %v, want 0 for negative gross edge", got.HaircutCost)
	}
	if got.NetEdge != got.GrossEdge {
		t.Fatalf("NetEdge = %v, want unchanged negative gross edge %v", got.NetEdge, got.GrossEdge)
	}
	if !contains(got.Reasons, "insufficient_edge") {
		t.Fatalf("Reasons = %v, want insufficient_edge", got.Reasons)
	}
}

func TestEvaluateCompleteSetHaircutReducesEdge(t *testing.T) {
	base := EvaluateCompleteSet(OpportunityInput{
		MarketID: "market-2",
		Outcomes: []OutcomeQuote{
			{Outcome: "YES", AskPrice: 0.40, Size: 1},
			{Outcome: "NO", AskPrice: 0.40, Size: 1},
		},
		FeeRate:            0,
		PartialFillHaircut: 0,
		MinNetEdge:         0.01,
	})
	haircut := EvaluateCompleteSet(OpportunityInput{
		MarketID: "market-2",
		Outcomes: []OutcomeQuote{
			{Outcome: "YES", AskPrice: 0.40, Size: 1},
			{Outcome: "NO", AskPrice: 0.40, Size: 1},
		},
		FeeRate:            0,
		PartialFillHaircut: 0.25,
		MinNetEdge:         0.01,
	})

	if haircut.NetEdge >= base.NetEdge {
		t.Fatalf("NetEdge with haircut = %v, want less than base %v", haircut.NetEdge, base.NetEdge)
	}
	if !haircut.Accepted || !base.Accepted {
		t.Fatalf("expected both observations accepted: base=%+v haircut=%+v", base, haircut)
	}
}

func TestEvaluateCompleteSetRejectsNonFiniteInputsWithZeroSafeOutputs(t *testing.T) {
	got := EvaluateCompleteSet(OpportunityInput{
		MarketID: "market-3",
		Outcomes: []OutcomeQuote{
			{Outcome: "YES", AskPrice: math.NaN(), Size: 1},
			{Outcome: "NO", AskPrice: 0.45, Size: 1},
		},
		FeeRate:            math.Inf(1),
		PartialFillHaircut: 0,
		MinNetEdge:         0.01,
	})

	if got.Accepted {
		t.Fatalf("Accepted = true, want false")
	}
	for _, v := range []float64{got.CompleteSetCost, got.GrossEdge, got.FeeCost, got.HaircutCost, got.NetEdge} {
		if v != 0 {
			t.Fatalf("expected zero-safe output, got %v", v)
		}
	}
	if !contains(got.Reasons, "invalid_fee_rate") || !contains(got.Reasons, "outcome_0_invalid_ask_price") {
		t.Fatalf("Reasons = %v, want invalid input reasons", got.Reasons)
	}
}

func TestModelSourceHasNoExecutionDependencies(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	modelPath := filepath.Join(filepath.Dir(file), "model.go")
	data, err := os.ReadFile(modelPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", modelPath, err)
	}

	if strings.Contains(string(data), "internal/execution") || strings.Contains(string(data), "live_gate") || strings.Contains(string(data), "order") {
		t.Fatalf("model.go contains forbidden execution-related source text")
	}

	fset := token.NewFileSet()
	fileNode, err := parser.ParseFile(fset, modelPath, data, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("ParseFile(%q) error = %v", modelPath, err)
	}
	for _, imp := range fileNode.Imports {
		path, err := strconvUnquote(imp.Path.Value)
		if err != nil {
			t.Fatalf("unquote import %q: %v", imp.Path.Value, err)
		}
		if strings.Contains(path, "internal/") || strings.Contains(path, "github.com/") || strings.Contains(path, "live_gate") {
			t.Fatalf("forbidden non-stdlib import in model.go: %q", path)
		}
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func strconvUnquote(s string) (string, error) {
	if len(s) < 2 {
		return s, nil
	}
	return strconv.Unquote(s)
}
