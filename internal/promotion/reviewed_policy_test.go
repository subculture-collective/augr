package promotion

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
	want := `{"schema":"promotion-policy-v1","version":"promotion-policy-v1@reviewed","required_gates":["multiple_testing_adjustment","overall_robustness"],"pass_action":"shadow","failure_action":"hold"}`
	if first.ID() != second.ID() || first.Digest() != second.Digest() || !bytes.Equal(first.CanonicalBytes(), []byte(want)) {
		t.Fatalf("reviewed promotion policy diverged: %s", first.CanonicalBytes())
	}
	input := ReviewedPolicyV1Input()
	input.RequiredGates[0] = "changed"
	third, err := ReviewedPolicyV1()
	if err != nil || third.ID() != first.ID() {
		t.Fatal("caller mutation changed package-owned reviewed policy")
	}
}
