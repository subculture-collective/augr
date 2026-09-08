package main

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidateInputRequiresExplicitUniqueSourcesAndCounts(t *testing.T) {
	cutoff := time.Date(2026, 1, 2, 14, 30, 0, 0, time.UTC)
	first, second := uuid.New(), uuid.New()
	valid := composeInput{ManifestIDs: []uuid.UUID{first, second}, DecisionCutoff: cutoff, ExpectedPayloadCount: 2, ExpectedPartitionCount: 2}
	if err := validateInput(valid); err != nil {
		t.Fatalf("validateInput(valid) error = %v", err)
	}
	for name, input := range map[string]composeInput{
		"one source":       {ManifestIDs: []uuid.UUID{first}, DecisionCutoff: cutoff, ExpectedPayloadCount: 1, ExpectedPartitionCount: 1},
		"duplicate source": {ManifestIDs: []uuid.UUID{first, first}, DecisionCutoff: cutoff, ExpectedPayloadCount: 2, ExpectedPartitionCount: 2},
		"missing counts":   {ManifestIDs: []uuid.UUID{first, second}, DecisionCutoff: cutoff},
		"local cutoff":     {ManifestIDs: []uuid.UUID{first, second}, DecisionCutoff: cutoff.In(time.FixedZone("offset", 3600)), ExpectedPayloadCount: 2, ExpectedPartitionCount: 2},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateInput(input); err == nil {
				t.Fatal("validateInput() accepted invalid composition")
			}
		})
	}
}
