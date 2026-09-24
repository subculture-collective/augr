package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/agent"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// loadAgentAccountContext reads the same broker selected for execution. A
// failed or partial read must never become a fabricated empty account.
func (r *realStrategyRunner) loadAgentAccountContext(ctx context.Context, strategy domain.Strategy) (*agent.AccountContext, error) {
	broker, name, err := r.newBrokerForStrategy(strategy)
	if err != nil {
		return nil, fmt.Errorf("agent account context: select broker: %w", err)
	}
	if broker == nil {
		return nil, fmt.Errorf("agent account context: broker unavailable")
	}
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	balance, err := broker.GetAccountBalance(readCtx)
	if err != nil {
		return nil, fmt.Errorf("agent account context: read %s balance: %w", name, err)
	}
	positions, err := broker.GetPositions(readCtx)
	if err != nil {
		return nil, fmt.Errorf("agent account context: read %s positions: %w", name, err)
	}
	snapshot := &agent.AccountContext{
		Source: name, IsPaper: strategy.IsPaper, ObservedAt: time.Now().UTC(),
		Currency: balance.Currency, Cash: balance.Cash, BuyingPower: balance.BuyingPower, Equity: balance.Equity,
		Positions: make([]agent.AccountPosition, 0, len(positions)),
	}
	for _, position := range positions {
		if position.ClosedAt != nil || position.Quantity == 0 {
			continue
		}
		snapshot.Positions = append(snapshot.Positions, agent.AccountPosition{
			Ticker: position.Ticker, Side: position.Side, Quantity: position.Quantity, AvgEntry: position.AvgEntry,
			AssetClass: position.AssetClass, UnderlyingTicker: position.UnderlyingTicker,
		})
	}
	sort.SliceStable(snapshot.Positions, func(i, j int) bool { return snapshot.Positions[i].Ticker < snapshot.Positions[j].Ticker })
	if _, err := json.Marshal(snapshot); err != nil {
		return nil, fmt.Errorf("agent account context: invalid broker values: %w", err)
	}
	return snapshot, nil
}
