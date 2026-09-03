package promotion

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestActivationCanonicalIdentityAndValidation(t *testing.T) {
	input := ActivationInput{
		Action: ActivationAction, DeploymentID: uuid.New(), DeploymentSHA256: strings.Repeat("a", 64),
		DecisionID: uuid.New(), DecisionSHA256: strings.Repeat("b", 64), StrategyID: uuid.New(),
		SourceVersionID: uuid.New(), RuntimeVersionID: uuid.New(), RuntimeVersionSHA256: strings.Repeat("c", 64),
		AccountID: uuid.New(), ScopeID: uuid.New(), CapitalBindingID: uuid.New(), ScheduleCron: "0 14 * * 1-5",
		Timezone: "America/Chicago", RiskPolicyVersion: "portfolio-risk-policy-v1@sha256:" + strings.Repeat("d", 64),
	}
	first, err := NewActivation(input)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := NewActivation(input)
	if err != nil || retry.ID() != first.ID() || retry.Digest() != first.Digest() || !bytes.Equal(retry.CanonicalBytes(), first.CanonicalBytes()) {
		t.Fatalf("activation retry diverged: %v", err)
	}
	rebuilt, err := ActivationFromCanonical(first.ID(), first.Digest(), first.CanonicalBytes())
	if err != nil || rebuilt.RuntimeVersionID() != input.RuntimeVersionID {
		t.Fatalf("rebuild=%v err=%v", rebuilt, err)
	}
	changed := input
	changed.ScheduleCron = "30 14 * * 1-5"
	second, err := NewActivation(changed)
	if err != nil || second.ID() == first.ID() {
		t.Fatalf("changed activation=%v err=%v", second, err)
	}
	invalid := input
	invalid.Action = SuspensionAction
	if _, err := NewActivation(invalid); err == nil {
		t.Fatal("suspension retained a schedule")
	}
}
