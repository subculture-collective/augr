package evaluation

import (
	"bytes"
	"testing"
	"time"
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

func TestReviewedDailyPolicyAllowsClosedSessionsButRejectsOffGridEvidence(t *testing.T) {
	policy, err := ReviewedPolicyV1()
	if err != nil {
		t.Fatal(err)
	}
	friday := time.Date(2026, time.September, 4, 21, 0, 0, 0, time.UTC)
	canonical := func(at time.Time) observationCanonical { return observationCanonical{ObservedAt: formatTime(at)} }
	if err := validateFrequency(policy, []observationCanonical{canonical(friday), canonical(friday.Add(72 * time.Hour))}); err != nil {
		t.Fatalf("weekend market closure rejected: %v", err)
	}
	if err := validateFrequency(policy, []observationCanonical{canonical(friday), canonical(friday.Add(25 * time.Hour))}); err == nil {
		t.Fatal("off-grid daily observation unexpectedly passed")
	}
}
