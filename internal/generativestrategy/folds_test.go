package generativestrategy

import (
	"testing"
	"time"
)

func TestPlanReviewedResearchFoldsIsPurgedEmbargoedAndDeterministic(t *testing.T) {
	t.Parallel()
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(270 * 24 * time.Hour)
	first, err := PlanReviewedResearchFolds(start, end)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PlanReviewedResearchFolds(start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || len(second) != 2 || first[0] != second[0] || first[1] != second[1] {
		t.Fatalf("folds are not deterministic: %+v / %+v", first, second)
	}
	if first[0].TrainEnd.Add(24*time.Hour) != first[0].TestStart || first[0].TestEnd.Add(24*time.Hour) != first[1].TrainStart ||
		first[1].TrainEnd.Add(24*time.Hour) != first[1].TestStart || first[1].TestEnd != end {
		t.Fatalf("fold boundaries violate purge or embargo: %+v", first)
	}
}

func TestPlanReviewedResearchFoldsRejectsShortOrNoncanonicalScope(t *testing.T) {
	t.Parallel()
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := PlanReviewedResearchFolds(start, start.Add(4*24*time.Hour)); err == nil {
		t.Fatal("short research scope was accepted")
	}
	if _, err := PlanReviewedResearchFolds(start.Local(), start.Add(270*24*time.Hour)); err == nil {
		t.Fatal("non-UTC research scope was accepted")
	}
}
