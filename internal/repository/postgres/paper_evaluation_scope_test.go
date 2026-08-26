package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/google/uuid"
)

func TestDiscoveryDeploymentReadinessRejectsValidScopeWithoutLoaderBinding(t *testing.T) {
	scope, err := NewPaperEvaluationScope(PaperEvaluationScope{
		AccountID: uuid.New(), CapitalBindingID: uuid.New(), ManifestSHA256: strings.Repeat("1", 64),
		QualitySHA256: strings.Repeat("2", 64), SimulationPolicySHA256: strings.Repeat("3", 64), CapitalPolicySHA256: strings.Repeat("4", 64),
		EvaluationStart: time.Now().Add(-time.Hour), EvaluationEnd: time.Now(),
	})
	if err != nil || scope.CanonicalSHA256 == "" {
		t.Fatalf("valid scope = %+v, err = %v", scope, err)
	}
	ready, reason, err := (&ReportArtifactRepo{}).DiscoveryDeploymentReadiness(context.Background())
	var lock repository.ImmutableBindingLock
	if !errors.Is(err, ErrDiscoveryDeploymentImmutableBinding) || !errors.As(err, &lock) || ready || reason != DiscoveryDeploymentUnavailableReason || lock.Reason() != reason {
		t.Fatalf("readiness = %t, %q, %v", ready, reason, err)
	}
}

func TestNewPaperEvaluationScopeCanonicalIdentityCoversEveryField(t *testing.T) {
	input := PaperEvaluationScope{
		AccountID: uuid.New(), CapitalBindingID: uuid.New(), ManifestSHA256: strings.Repeat("1", 64),
		QualitySHA256: strings.Repeat("2", 64), SimulationPolicySHA256: strings.Repeat("3", 64),
		CapitalPolicySHA256: strings.Repeat("4", 64),
		EvaluationStart:     time.Date(2026, 1, 1, 0, 0, 0, 123456789, time.FixedZone("offset", 3600)),
		EvaluationEnd:       time.Date(2026, 2, 1, 0, 0, 0, 123456789, time.FixedZone("offset", 3600)),
	}
	first, err := NewPaperEvaluationScope(input)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(first.CanonicalBytes)
	if first.CanonicalSHA256 != fmt.Sprintf("%x", digest) {
		t.Fatal("canonical digest does not hash canonical bytes")
	}
	for _, want := range []string{
		input.AccountID.String(), input.CapitalBindingID.String(), input.ManifestSHA256,
		input.QualitySHA256, input.SimulationPolicySHA256, input.CapitalPolicySHA256, "paper-evaluation-scope-v1",
	} {
		if !bytes.Contains(first.CanonicalBytes, []byte(want)) {
			t.Fatalf("canonical bytes omit %q: %s", want, first.CanonicalBytes)
		}
	}
	changed := input
	changed.CapitalBindingID = uuid.New()
	second, err := NewPaperEvaluationScope(changed)
	if err != nil {
		t.Fatal(err)
	}
	if first.CanonicalSHA256 == second.CanonicalSHA256 || bytes.Equal(first.CanonicalBytes, second.CanonicalBytes) {
		t.Fatal("capital binding did not change scope identity")
	}
	if first.EvaluationStart.Location() != time.UTC || first.EvaluationStart.Nanosecond()%1000 != 0 {
		t.Fatalf("time not normalized: %v", first.EvaluationStart)
	}
}
