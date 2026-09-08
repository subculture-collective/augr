package automation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

var (
	optionsExpirySpec    = scheduler.ScheduleSpec{Type: scheduler.ScheduleTypeAfterHours, Cron: "0 23 * * 1-5", SkipWeekends: true, SkipHolidays: false}
	optionsReconcileSpec = scheduler.ScheduleSpec{Type: scheduler.ScheduleTypeAfterHours, Cron: "30 23 * * 1-5", SkipWeekends: true, SkipHolidays: false}
)

func (o *JobOrchestrator) registerOptionsLifecycleJobs() {
	if o.deps.PositionRepo != nil && o.deps.OptionSettlementRepo != nil && o.deps.DataService != nil {
		o.Register("options_expiry_settlement", "Cash-settle expired paper option positions", optionsExpirySpec, o.optionsExpirySettlement)
	}
	if o.deps.OrderRepo != nil && o.deps.PositionRepo != nil && o.deps.TradeRepo != nil {
		o.Register("options_lifecycle_reconcile", "Audit durable option order, position, trade, and leg-group state", optionsReconcileSpec, o.optionsLifecycleReconcile, "options_expiry_settlement")
	}
}

func (o *JobOrchestrator) optionsLifecycleReconcile(ctx context.Context) error {
	orders, err := listAllOptionsLifecycleOrders(ctx, o.deps.OrderRepo, o.deps.ExecutionAccount)
	if err != nil {
		return fmt.Errorf("options_lifecycle_reconcile: list orders: %w", err)
	}
	positions, err := listAllOptionsLifecyclePositions(ctx, o.deps.PositionRepo, o.deps.ExecutionAccount)
	if err != nil {
		return fmt.Errorf("options_lifecycle_reconcile: list positions: %w", err)
	}
	trades, err := listAllOptionsLifecycleTrades(ctx, o.deps.TradeRepo, o.deps.ExecutionAccount)
	if err != nil {
		return fmt.Errorf("options_lifecycle_reconcile: list trades: %w", err)
	}
	result := execution.ReconcileOptionsLifecycle(orders, positions, trades)
	o.SetLastSummary("options_lifecycle_reconcile", map[string]int{"orders": result.OptionOrders, "positions": result.OptionPositions, "trades": result.OptionTrades, "leg_groups": result.LegGroups, "findings": len(result.Findings)})
	if !result.Healthy() {
		return fmt.Errorf("options_lifecycle_reconcile: %s", strings.Join(result.Findings, "; "))
	}
	return nil
}

func (o *JobOrchestrator) optionsExpirySettlement(ctx context.Context) error {
	now := time.Now().UTC()
	scope, err := execution.NewScheduledNonRunExecutionScope(o.deps.ExecutionAccount.AccountID(), o.deps.ExecutionAccount.Environment(), ledger.ExecutionOriginSettlement, "options-expiry/"+now.Format("2006-01-02"))
	if err != nil {
		return fmt.Errorf("options_expiry_settlement: execution scope: %w", err)
	}
	positions, err := listAllOpenPositionsByAccount(ctx, o.deps.PositionRepo, o.deps.ExecutionAccount)
	if err != nil {
		return fmt.Errorf("options_expiry_settlement: list positions: %w", err)
	}
	contracts := make(map[execution.OptionExpiryPriceKey]struct{})
	for _, position := range positions {
		if position.AssetClass == domain.AssetClassOption && position.Expiry != nil && !position.Expiry.After(now) && position.UnderlyingTicker != "" {
			contracts[execution.NewOptionExpiryPriceKey(position.UnderlyingTicker, *position.Expiry)] = struct{}{}
		}
	}
	prices := make(map[execution.OptionExpiryPriceKey]float64, len(contracts))
	for contract := range contracts {
		end := contract.ExpiryDate.Add(24 * time.Hour)
		bars, err := o.deps.DataService.GetOHLCV(ctx, domain.MarketTypeStock, contract.Underlying, data.Timeframe1d, contract.ExpiryDate.Add(-7*24*time.Hour), end)
		if err != nil {
			return fmt.Errorf("options_expiry_settlement: closing price lookup for %s at %s: %w", contract.Underlying, contract.ExpiryDate.Format("2006-01-02"), err)
		}
		closePrice, ok := optionExpirySessionClose(bars, contract.ExpiryDate)
		if !ok {
			return fmt.Errorf("options_expiry_settlement: closing price unavailable for %s at contract expiry %s", contract.Underlying, contract.ExpiryDate.Format("2006-01-02"))
		}
		prices[contract] = closePrice
	}
	summary, err := execution.SettleExpiredOptionPositions(ctx, scope, positions, prices, now, o.deps.OptionSettlementRepo, o.deps.OptionSettlementState)
	if err != nil {
		return err
	}
	o.SetLastSummary("options_expiry_settlement", map[string]int{"expired_worthless": summary.ExpiredWorthless, "cash_settled": summary.CashSettled})
	return nil
}

func optionExpirySessionClose(bars []domain.OHLCV, expiry time.Time) (float64, bool) {
	want := expiry.UTC().Format("2006-01-02")
	var closePrice float64
	found := false
	for _, bar := range bars {
		if bar.Timestamp.UTC().Format("2006-01-02") == want && bar.Close > 0 {
			closePrice, found = bar.Close, true
		}
	}
	return closePrice, found
}

func listAllOpenPositions(ctx context.Context, repo repository.PositionRepository) ([]domain.Position, error) {
	const pageSize = 250
	var all []domain.Position
	for offset := 0; ; offset += pageSize {
		page, err := repo.GetOpen(ctx, repository.PositionFilter{}, pageSize, offset)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < pageSize {
			return all, nil
		}
	}
}

func listAllOpenPositionsByAccount(ctx context.Context, repo repository.PositionRepository, account domain.ExecutionAccountBinding) ([]domain.Position, error) {
	if err := account.Validate(); err != nil {
		return nil, fmt.Errorf("options lifecycle account: %w", err)
	}
	scoped, ok := repo.(repository.AccountScopedPositionRepository)
	if !ok {
		return nil, fmt.Errorf("options lifecycle requires account-scoped position repository")
	}
	const pageSize = 250
	var all []domain.Position
	for offset := 0; ; offset += pageSize {
		page, err := scoped.GetOpenByAccount(ctx, account.AccountID(), account.Environment(), repository.PositionFilter{}, pageSize, offset)
		if err != nil {
			return nil, err
		}
		for _, position := range page {
			if position.AccountID != account.AccountID() || position.Environment != account.Environment() {
				return nil, fmt.Errorf("options lifecycle position %s escaped account scope", position.ID)
			}
		}
		all = append(all, page...)
		if len(page) < pageSize {
			return all, nil
		}
	}
}

func listAllOptionsLifecyclePositions(ctx context.Context, repo repository.PositionRepository, account domain.ExecutionAccountBinding) ([]domain.Position, error) {
	if err := account.Validate(); err != nil {
		return nil, fmt.Errorf("options lifecycle account: %w", err)
	}
	scoped, ok := repo.(repository.OptionsLifecyclePositionRepository)
	if !ok {
		return nil, fmt.Errorf("options lifecycle requires account and environment scoped positions")
	}
	const pageSize = 250
	var all []domain.Position
	for offset := 0; ; offset += pageSize {
		page, err := scoped.ListOptionsLifecyclePositions(ctx, account.AccountID(), account.Environment(), pageSize, offset)
		if err != nil {
			return nil, err
		}
		for _, position := range page {
			if position.AccountID != account.AccountID() || position.Environment != account.Environment() {
				return nil, fmt.Errorf("options lifecycle position %s escaped account scope", position.ID)
			}
		}
		all = append(all, page...)
		if len(page) < pageSize {
			return all, nil
		}
	}
}

func listAllOptionsLifecycleOrders(ctx context.Context, repo repository.OrderRepository, account domain.ExecutionAccountBinding) ([]domain.Order, error) {
	if err := account.Validate(); err != nil {
		return nil, fmt.Errorf("options lifecycle account: %w", err)
	}
	scoped, ok := repo.(repository.OptionsLifecycleOrderRepository)
	if !ok {
		return nil, fmt.Errorf("options lifecycle requires account and environment scoped orders")
	}
	const pageSize = 250
	var all []domain.Order
	for offset := 0; ; offset += pageSize {
		page, err := scoped.ListOptionsLifecycleOrders(ctx, account.AccountID(), account.Environment(), pageSize, offset)
		if err != nil {
			return nil, err
		}
		for _, order := range page {
			if order.AccountID != account.AccountID() || order.Environment != account.Environment() {
				return nil, fmt.Errorf("options lifecycle order %s escaped account scope", order.ID)
			}
		}
		all = append(all, page...)
		if len(page) < pageSize {
			return all, nil
		}
	}
}

func listAllOptionsLifecycleTrades(ctx context.Context, repo repository.TradeRepository, account domain.ExecutionAccountBinding) ([]domain.Trade, error) {
	if err := account.Validate(); err != nil {
		return nil, fmt.Errorf("options lifecycle account: %w", err)
	}
	scoped, ok := repo.(repository.OptionsLifecycleTradeRepository)
	if !ok {
		return nil, fmt.Errorf("options lifecycle requires account and environment scoped trades")
	}
	const pageSize = 250
	var all []domain.Trade
	for offset := 0; ; offset += pageSize {
		page, err := scoped.ListOptionsLifecycleTrades(ctx, account.AccountID(), account.Environment(), pageSize, offset)
		if err != nil {
			return nil, err
		}
		for _, trade := range page {
			if trade.AccountID != account.AccountID() || trade.Environment != account.Environment() {
				return nil, fmt.Errorf("options lifecycle trade %s escaped account scope", trade.ID)
			}
		}
		all = append(all, page...)
		if len(page) < pageSize {
			return all, nil
		}
	}
}
