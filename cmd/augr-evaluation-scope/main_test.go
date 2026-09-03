package main

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidateInputRequiresCanonicalExplicitScopeGraph(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	valid := scopeInput{
		ScopeID: uuid.New(), AccountID: uuid.New(), CapitalBindingID: uuid.New(), ManifestID: uuid.New(),
		SimulationPolicyVersion: "simulation-policy-v1@sha256:test", CapitalPolicyVersion: "capital-margin-policy-v1@sha256:test",
		EvaluationStart: start, EvaluationEnd: start.Add(9 * 30 * 24 * time.Hour),
	}
	if err := validateInput(valid); err != nil {
		t.Fatalf("validateInput(valid) error = %v", err)
	}
	missingScope := valid
	missingScope.ScopeID = uuid.Nil
	if err := validateInput(missingScope); err == nil {
		t.Fatal("validateInput() accepted missing scope ID")
	}
	localInterval := valid
	localInterval.EvaluationStart = start.In(time.FixedZone("offset", 3600))
	if err := validateInput(localInterval); err == nil {
		t.Fatal("validateInput() accepted non-UTC interval")
	}
}
