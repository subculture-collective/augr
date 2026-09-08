package generativestrategy

import (
	"strings"
	"testing"
)

func evaluationSpec(t *testing.T) *Spec {
	t.Helper()
	_, input := specFixture(t)
	spec, err := NewSpec(input)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestSpecEvaluateUsesExactDeterministicExpressions(t *testing.T) {
	t.Parallel()
	spec := evaluationSpec(t)
	entry, exit, err := spec.Evaluate(map[string]string{"average": "100", "eligible": "true", "price": "101"})
	if err != nil || !entry || exit {
		t.Fatalf("Evaluate() = %v, %v, %v; want true, false", entry, exit, err)
	}
	entry, exit, err = spec.Evaluate(map[string]string{"average": "100", "eligible": "true", "price": "99"})
	if err != nil || entry || !exit {
		t.Fatalf("Evaluate() = %v, %v, %v; want false, true", entry, exit, err)
	}
}

func TestSpecEvaluateRejectsIncompleteOrNoncanonicalBindings(t *testing.T) {
	t.Parallel()
	spec := evaluationSpec(t)
	for name, values := range map[string]map[string]string{
		"missing": {"average": "100", "price": "101"},
		"extra":   {"average": "100", "eligible": "true", "price": "101", "other": "1"},
		"decimal": {"average": "100", "eligible": "true", "price": "101.0"},
		"boolean": {"average": "100", "eligible": "TRUE", "price": "101"},
	} {
		name, values := name, values
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := spec.Evaluate(values); err == nil {
				t.Fatal("Evaluate() error = nil, want fail-closed binding error")
			}
		})
	}
}

func TestNewSpecRejectsFalseExampleClaim(t *testing.T) {
	t.Parallel()
	_, input := specFixture(t)
	input.ExampleTests[0].ExpectedEntry = false
	_, err := NewSpec(input)
	if err == nil || !strings.Contains(err.Error(), "does not satisfy") {
		t.Fatalf("NewSpec() error = %v, want example decision mismatch", err)
	}
}
