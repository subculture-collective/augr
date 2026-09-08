package generativestrategy

import (
	"fmt"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/robustness"
)

type ResearchFold struct {
	Sequence   int
	TrainStart time.Time
	TrainEnd   time.Time
	TestStart  time.Time
	TestEnd    time.Time
}

// PlanReviewedResearchFolds partitions one immutable scope into two ordered,
// non-overlapping train/test folds using the reviewed purge and embargo. The
// first training boundary is also the latest evidence a proposal generator may
// inspect, keeping both test windows out of model-authored hypotheses.
func PlanReviewedResearchFolds(start, end time.Time) ([]ResearchFold, error) {
	if !scenarioTime(start) || !scenarioTime(end) || !start.Before(end) {
		return nil, fmt.Errorf("generated research fold interval is invalid")
	}
	policy, err := robustness.ReviewedPolicyV1()
	if err != nil {
		return nil, fmt.Errorf("generated research fold policy: %w", err)
	}
	if policy.FoldCount() != 2 {
		return nil, fmt.Errorf("generated research fold planner supports exactly two reviewed folds")
	}
	purge := time.Duration(policy.PurgeSeconds()) * time.Second
	embargo := time.Duration(policy.EmbargoSeconds()) * time.Second
	usable := end.Sub(start) - 2*purge - embargo
	segment := (usable / 4).Truncate(time.Microsecond)
	if segment < 24*time.Hour {
		return nil, fmt.Errorf("generated research scope is too short for two reviewed daily folds")
	}
	firstTrainEnd := start.Add(segment)
	firstTestStart := firstTrainEnd.Add(purge)
	firstTestEnd := firstTestStart.Add(segment)
	secondTrainStart := firstTestEnd.Add(embargo)
	secondTrainEnd := secondTrainStart.Add(segment)
	secondTestStart := secondTrainEnd.Add(purge)
	if !secondTestStart.Before(end) {
		return nil, fmt.Errorf("generated research scope cannot satisfy reviewed fold boundaries")
	}
	return []ResearchFold{
		{Sequence: 0, TrainStart: start, TrainEnd: firstTrainEnd, TestStart: firstTestStart, TestEnd: firstTestEnd},
		{Sequence: 1, TrainStart: secondTrainStart, TrainEnd: secondTrainEnd, TestStart: secondTestStart, TestEnd: end},
	}, nil
}
