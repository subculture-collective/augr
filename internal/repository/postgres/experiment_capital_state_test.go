package postgres

import (
	"crypto/sha256"
	"testing"
	"time"
)

func TestCanonicalExperimentCapitalStateSourceRequiresProtectedBoundary(t *testing.T) {
	attestor := ProjectionCheckpointAttestor{KeyID: "test", Secret: make([]byte, sha256.Size)}
	for _, testCase := range []struct {
		name     string
		attestor ProjectionCheckpointAttestor
		maxAge   time.Duration
	}{
		{name: "missing attestor", maxAge: time.Minute},
		{name: "missing freshness", attestor: attestor},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if source, err := NewCanonicalExperimentCapitalStateSource(nil, testCase.attestor, testCase.maxAge); err == nil || source != nil {
				t.Fatalf("NewCanonicalExperimentCapitalStateSource() = %+v/%v", source, err)
			}
		})
	}
}
