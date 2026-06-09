package replay

import (
	"math"
	"sort"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// BuildWorkbench builds a deterministic replay response for a decision.
func BuildWorkbench(source domain.TradeDecision, events []domain.ReplayEvent) domain.ReplayWorkbenchResponse {
	source = sanitizeTradeDecision(source)
	sortedEvents := append([]domain.ReplayEvent(nil), events...)
	sort.Slice(sortedEvents, func(i, j int) bool {
		if !sortedEvents[i].OccurredAt.Equal(sortedEvents[j].OccurredAt) {
			return sortedEvents[i].OccurredAt.Before(sortedEvents[j].OccurredAt)
		}
		if !sortedEvents[i].CreatedAt.Equal(sortedEvents[j].CreatedAt) {
			return sortedEvents[i].CreatedAt.Before(sortedEvents[j].CreatedAt)
		}
		return sortedEvents[i].ID.String() < sortedEvents[j].ID.String()
	})
	if sortedEvents == nil {
		sortedEvents = []domain.ReplayEvent{}
	}

	summary := domain.ReplayWorkbenchSummary{
		EventCount:        len(sortedEvents),
		HasPaperOrder:     source.PaperOrderID != nil,
		HasLiveOrder:      source.LiveOrderID != nil,
		HasFill:           false,
		HasOutcome:        false,
		LatestStatus:      string(source.Status),
		TotalApprovedSize: finiteFloat(source.ApprovedSize),
		TotalNetEV:        finiteFloat(source.NetEV),
		RejectionCount:    len(source.RiskReasons),
		RejectionReasons:  append([]string(nil), source.RiskReasons...),
	}

	for _, event := range sortedEvents {
		switch event.EventType {
		case domain.ReplayEventTypePaperOrdered:
			summary.HasPaperOrder = true
		case domain.ReplayEventTypeLiveOrdered:
			summary.HasLiveOrder = true
		case domain.ReplayEventTypeFillObserved:
			summary.HasFill = true
		case domain.ReplayEventTypeOutcomeResolved:
			summary.HasOutcome = true
		}
	}

	if len(sortedEvents) > 0 {
		first := sortedEvents[0].OccurredAt
		last := sortedEvents[len(sortedEvents)-1].OccurredAt
		summary.FirstEventAt = &first
		summary.LastEventAt = &last
	}

	return domain.ReplayWorkbenchResponse{
		Source:  source,
		Events:  sortedEvents,
		Summary: summary,
	}
}

func sanitizeTradeDecision(decision domain.TradeDecision) domain.TradeDecision {
	decision.FairValue = finiteFloat(decision.FairValue)
	decision.ExecutablePrice = finiteFloat(decision.ExecutablePrice)
	decision.Spread = finiteFloat(decision.Spread)
	decision.Depth = finiteFloat(decision.Depth)
	decision.GrossEV = finiteFloat(decision.GrossEV)
	decision.NetEV = finiteFloat(decision.NetEV)
	decision.KellyFraction = finiteFloat(decision.KellyFraction)
	decision.ProposedSize = finiteFloat(decision.ProposedSize)
	decision.ApprovedSize = finiteFloat(decision.ApprovedSize)
	return decision
}

func finiteFloat(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}
