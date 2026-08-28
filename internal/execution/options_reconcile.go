package execution

import (
	"fmt"
	"math"
	"sort"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

type OptionsReconciliation struct {
	OptionOrders    int
	OptionPositions int
	OptionTrades    int
	LegGroups       int
	Findings        []string
}

func (r OptionsReconciliation) Healthy() bool { return len(r.Findings) == 0 }

// ReconcileOptionsLifecycle checks the durable order-position-trade graph. It
// reports inconsistencies but does not invent repairs or broker state.
func ReconcileOptionsLifecycle(orders []domain.Order, positions []domain.Position, trades []domain.Trade) OptionsReconciliation {
	result := OptionsReconciliation{}
	tradesByOrder := map[uuid.UUID]int{}
	openByPosition, closeByPosition := map[uuid.UUID]float64{}, map[uuid.UUID]float64{}
	for _, trade := range trades {
		if trade.AssetClass != domain.AssetClassOption {
			continue
		}
		result.OptionTrades++
		if trade.OrderID != nil {
			tradesByOrder[*trade.OrderID]++
		}
		if trade.PositionID != nil {
			if trade.OpenClose == "open" {
				openByPosition[*trade.PositionID] += trade.Quantity
			}
			if trade.OpenClose == "close" {
				closeByPosition[*trade.PositionID] += trade.Quantity
			}
		}
	}
	groups := map[uuid.UUID][]domain.Position{}
	for _, order := range orders {
		marketOption := order.MarketType.Normalize() == domain.MarketTypeOptions
		assetOption := order.AssetClass == domain.AssetClassOption
		if !marketOption && !assetOption {
			continue
		}
		result.OptionOrders++
		if marketOption != assetOption {
			result.Findings = append(result.Findings, fmt.Sprintf("option order %s has inconsistent market and asset classification", order.ID))
			continue
		}
		if order.Status == domain.OrderStatusFilled && tradesByOrder[order.ID] == 0 {
			result.Findings = append(result.Findings, fmt.Sprintf("filled option order %s has no trade", order.ID))
		}
	}
	for _, position := range positions {
		if position.AssetClass != domain.AssetClassOption {
			continue
		}
		result.OptionPositions++
		opened, closed := openByPosition[position.ID], closeByPosition[position.ID]
		if opened <= 0 {
			result.Findings = append(result.Findings, fmt.Sprintf("option position %s has no opening trade", position.ID))
		}
		if position.ClosedAt != nil && closed <= 0 {
			result.Findings = append(result.Findings, fmt.Sprintf("closed option position %s has no closing trade", position.ID))
		}
		remaining := opened - closed
		wantRemaining := position.Quantity
		if position.ClosedAt != nil {
			wantRemaining = 0
		}
		if opened > 0 && (remaining < -1e-8 || math.Abs(remaining-wantRemaining) > 1e-8) {
			result.Findings = append(result.Findings, fmt.Sprintf("option position %s trade quantities do not match remaining quantity", position.ID))
		}
		if position.LegGroupID != nil {
			groups[*position.LegGroupID] = append(groups[*position.LegGroupID], position)
		}
	}
	result.LegGroups = len(groups)
	for groupID, legs := range groups {
		if len(legs) < 2 {
			result.Findings = append(result.Findings, fmt.Sprintf("option leg group %s has only %d persisted leg", groupID, len(legs)))
			continue
		}
		closed := legs[0].ClosedAt != nil
		for _, leg := range legs[1:] {
			if (leg.ClosedAt != nil) != closed {
				result.Findings = append(result.Findings, fmt.Sprintf("option leg group %s has mixed open and closed legs", groupID))
				break
			}
		}
	}
	sort.Strings(result.Findings)
	return result
}
