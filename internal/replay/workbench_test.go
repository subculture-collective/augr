package replay

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestBuildWorkbenchSortsAndSummarizes(t *testing.T) {
	decision := domain.TradeDecision{
		ID:           uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"),
		ApprovedSize: math.Inf(1),
		NetEV:        math.NaN(),
		RiskReasons:  []string{"spread too wide", "low liquidity"},
		Status:       domain.TradeDecisionStatusLive,
		PaperOrderID: ptrUUID(uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")),
	}

	events := []domain.ReplayEvent{
		{ID: uuid.MustParse("33333333-3333-3333-3333-333333333333"), EventType: domain.ReplayEventTypeOutcomeResolved, OccurredAt: time.Date(2026, 6, 9, 12, 5, 0, 0, time.UTC), CreatedAt: time.Date(2026, 6, 9, 12, 5, 5, 0, time.UTC)},
		{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), EventType: domain.ReplayEventTypePaperOrdered, OccurredAt: time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC), CreatedAt: time.Date(2026, 6, 9, 12, 0, 5, 0, time.UTC)},
		{ID: uuid.MustParse("22222222-2222-2222-2222-222222222222"), EventType: domain.ReplayEventTypeFillObserved, OccurredAt: time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC), CreatedAt: time.Date(2026, 6, 9, 12, 0, 6, 0, time.UTC)},
	}

	got := BuildWorkbench(decision, events)
	if got.Summary.EventCount != 3 {
		t.Fatalf("EventCount = %d, want 3", got.Summary.EventCount)
	}
	if got.Summary.TotalApprovedSize != 0 || got.Summary.TotalNetEV != 0 {
		t.Fatalf("summary floats not sanitized: %+v", got.Summary)
	}
	if !got.Summary.HasPaperOrder || !got.Summary.HasFill || !got.Summary.HasOutcome {
		t.Fatalf("summary flags missing: %+v", got.Summary)
	}
	if got.Summary.FirstEventAt == nil || got.Summary.LastEventAt == nil {
		t.Fatal("expected first/last event times")
	}
	if got.Events[0].ID.String() != "11111111-1111-1111-1111-111111111111" || got.Events[1].ID.String() != "22222222-2222-2222-2222-222222222222" || got.Events[2].ID.String() != "33333333-3333-3333-3333-333333333333" {
		t.Fatalf("events not sorted deterministically: %+v", got.Events)
	}
	if got.Source.ApprovedSize != 0 || got.Source.NetEV != 0 {
		t.Fatalf("source floats not sanitized: %+v", got.Source)
	}
	if got.Summary.RejectionCount != 2 {
		t.Fatalf("RejectionCount = %d, want 2", got.Summary.RejectionCount)
	}
	if len(got.Summary.RejectionReasons) != 2 {
		t.Fatalf("RejectionReasons = %+v, want 2 items", got.Summary.RejectionReasons)
	}
	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("response should marshal: %v", err)
	}
}

func TestBuildWorkbenchHandlesEmptyEvents(t *testing.T) {
	got := BuildWorkbench(domain.TradeDecision{Status: domain.TradeDecisionStatusCandidate}, nil)
	if got.Events == nil {
		t.Fatal("expected empty events slice, got nil")
	}
	if got.Summary.EventCount != 0 || got.Summary.FirstEventAt != nil || got.Summary.LastEventAt != nil {
		t.Fatalf("unexpected empty summary: %+v", got.Summary)
	}
}

func ptrUUID(v uuid.UUID) *uuid.UUID { return &v }
