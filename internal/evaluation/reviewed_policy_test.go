package evaluation

import (
	"bytes"
	"testing"
)

func TestReviewedPolicyV1IsStableAndDefensive(t *testing.T) {
	first, err := ReviewedPolicyV1()
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReviewedPolicyV1()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema":"evaluation-policy-v1","version":"evaluation-policy-v1@reviewed","frequency":"daily","periods_per_year":252,"return_kind":"simple","cash_convention":"explicit_per_period","lot_method":"fifo","recovery_definition":"first_equity_at_or_above_prior_peak","decimal_scale":12}`
	if first.ID() != second.ID() || first.Digest() != second.Digest() || !bytes.Equal(first.CanonicalBytes(), []byte(want)) {
		t.Fatalf("reviewed evaluation policy diverged: %s", first.CanonicalBytes())
	}
	input := ReviewedPolicyV1Input()
	input.Frequency = "weekly"
	third, err := ReviewedPolicyV1()
	if err != nil || third.ID() != first.ID() {
		t.Fatal("caller mutation changed package-owned reviewed policy")
	}
}
