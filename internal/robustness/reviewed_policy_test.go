package robustness

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
	want := `{"schema":"robustness-policy-v1","version":"robustness-policy-v1@reviewed","fold_count":2,"purge_seconds":86400,"embargo_seconds":86400,"bootstrap_algorithm":"xorshift64star-iid-percentile-v1","bootstrap_seed":305,"bootstrap_iterations":1000,"confidence_level":"0.95","family_wise_alpha":"0.05","multiple_testing_correction":"holm_bonferroni","max_largest_positive_share":"0.4","max_top_decile_positive_share":"0.4","max_perturbation_degradation":"0.005","required_perturbations":["cost_up"],"decimal_scale":12}`
	if first.ID() != second.ID() || first.Digest() != second.Digest() || !bytes.Equal(first.CanonicalBytes(), []byte(want)) {
		t.Fatalf("reviewed robustness policy diverged: %s", first.CanonicalBytes())
	}
	input := ReviewedPolicyV1Input()
	input.RequiredPerturbations[0] = "changed"
	third, err := ReviewedPolicyV1()
	if err != nil || third.ID() != first.ID() {
		t.Fatal("caller mutation changed package-owned reviewed policy")
	}
}
