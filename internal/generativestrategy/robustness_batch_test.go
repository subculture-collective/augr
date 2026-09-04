package generativestrategy

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/robustness"
)

type robustnessSourceFixture struct {
	items []EligibleRobustnessAssessment
	err   error
}

func (source robustnessSourceFixture) ListEligibleGeneratedRobustness(context.Context, uuid.UUID, uuid.UUID, int) ([]EligibleRobustnessAssessment, error) {
	return source.items, source.err
}

type robustnessStoreFixture struct{}

func (robustnessStoreFixture) RegisterPolicy(_ context.Context, value *robustness.Policy) (*robustness.Policy, error) {
	return value, nil
}

func (robustnessStoreFixture) RegisterFamily(_ context.Context, value *robustness.Family) (*robustness.Family, error) {
	return value, nil
}

func (robustnessStoreFixture) RecordAssessment(_ context.Context, value *robustness.Assessment) (*robustness.Assessment, error) {
	return value, nil
}

func TestRobustnessBatchNoopsUntilCompleteEvidenceExists(t *testing.T) {
	t.Parallel()
	service, err := NewRobustnessBatchService(robustnessSourceFixture{}, robustnessStoreFixture{})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := service.RunEligible(context.Background(), uuid.New(), uuid.New(), 1)
	if err != nil || summary != (BatchSummary{}) {
		t.Fatalf("summary=%+v error=%v", summary, err)
	}
}

func TestRobustnessBatchRejectsInvalidSourceAndBounds(t *testing.T) {
	t.Parallel()
	service, err := NewRobustnessBatchService(robustnessSourceFixture{items: []EligibleRobustnessAssessment{{Key: "incomplete"}}}, robustnessStoreFixture{})
	if err != nil {
		t.Fatal(err)
	}
	accountID, scopeID := uuid.New(), uuid.New()
	if summary, runErr := service.RunEligible(context.Background(), accountID, scopeID, 1); runErr == nil || summary.Eligible != 1 || summary.Failed != 1 {
		t.Fatalf("invalid source summary=%+v error=%v", summary, runErr)
	}
	if summary, runErr := service.RunEligible(context.Background(), accountID, scopeID, MaximumResearchBatchSize+1); runErr == nil || summary.Completed != 0 {
		t.Fatalf("oversized summary=%+v error=%v", summary, runErr)
	}
}
