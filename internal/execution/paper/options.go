package paper

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/simulation"
)

// DefaultOptionFeePerContract is the standard per-contract option commission ($0.65).
const DefaultOptionFeePerContract = 0.65

// OptionsFillResult holds the result of a simulated options fill.
type OptionsFillResult struct {
	FillPrice float64
	Quantity  float64
	Premium   float64 // fillPrice * quantity * multiplier
	Fee       float64 // per-contract fee
}

type optionPositionEffect struct {
	ticker     string
	side       domain.PositionSide
	quantity   float64
	price      float64
	multiplier float64
	opened     bool
	previous   map[uuid.UUID]*domain.Position
}

// SimulateOptionFill calculates the fill for an explicitly priced options
// order through the relocated common simulation compatibility primitive.
func SimulateOptionFill(order *domain.Order) (*OptionsFillResult, error) {
	if order == nil {
		return nil, errors.New("paper: order is required")
	}
	if order.Quantity <= 0 {
		return nil, errors.New("paper: order quantity must be greater than zero")
	}

	if order.LimitPrice == nil || *order.LimitPrice <= 0 {
		return nil, errors.New("paper: executable option price is required")
	}
	legacy, err := simulation.SimulateOptionsFill(
		order,
		domain.OHLCV{Close: *order.LimitPrice},
		simulation.OptionsFillConfig{FeePerContract: DefaultOptionFeePerContract},
	)
	if err != nil {
		return nil, fmt.Errorf("paper: simulate option fill: %w", err)
	}

	return &OptionsFillResult{
		FillPrice: legacy.FillPrice,
		Quantity:  legacy.Quantity,
		Premium:   legacy.FillPrice * legacy.Quantity * legacy.Multiplier,
		Fee:       legacy.Fee,
	}, nil
}

// SubmitOptionOrder fills an explicitly priced option order in the paper book.
// It never calls an external broker and never invents a price for missing quote data.
func (b *PaperBroker) SubmitOptionOrder(ctx context.Context, order *domain.Order) (string, error) {
	if b == nil {
		return "", errors.New("paper: broker is required")
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("paper: submit option order: %w", err)
	}
	if order == nil || order.AssetClass != domain.AssetClassOption {
		return "", errors.New("paper: explicit option order is required")
	}
	result, err := SimulateOptionFill(order)
	if err != nil {
		return "", err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.currentTime().UTC()
	externalID := strings.TrimSpace(order.ClientOrderID)
	if externalID == "" {
		externalID = b.nextExternalIDLocked()
	} else if existing := b.orders[externalID]; existing != nil {
		intentMismatch := (existing.PositionIntent == nil) != (order.PositionIntent == nil)
		if !intentMismatch && existing.PositionIntent != nil {
			intentMismatch = *existing.PositionIntent != *order.PositionIntent
		}
		if existing.Ticker != order.Ticker || existing.Side != order.Side || existing.Quantity != order.Quantity || intentMismatch {
			return "", errors.New("paper: option client order id reused with different order")
		}
		*order = *cloneOrder(existing)
		return externalID, nil
	}
	totalDebit := result.Premium + result.Fee
	if order.Side == domain.OrderSideBuy && b.balance.Cash < totalDebit {
		return "", errors.Join(execution.ErrBrokerOrderRejected, fmt.Errorf("paper: insufficient balance: need %.2f, have %.2f", totalDebit, b.balance.Cash))
	}
	if err := ApplyOptionFill(order, result); err != nil {
		return "", err
	}
	var closePositionID uuid.UUID
	if len(order.ClosePositionIDs) == 1 {
		closePositionID = order.ClosePositionIDs[0]
	}
	effect, err := b.applyOptionPositionLocked(order.Ticker, order.UnderlyingTicker, order.OptionType, order.Strike, order.Expiry, order.ContractMultiplier, order.PositionIntent, closePositionID, result.Quantity, result.FillPrice, now)
	if err != nil {
		return "", err
	}
	if order.Side == domain.OrderSideBuy {
		b.balance.Cash -= totalDebit
	} else {
		b.balance.Cash += result.Premium - result.Fee
	}
	order.ExternalID = externalID
	order.SubmittedAt = timePtr(now)
	order.FilledAt = timePtr(now)
	b.balance.BuyingPower = b.balance.Cash
	b.balance.Equity = b.markToMarketEquityLocked()
	b.orders[externalID] = cloneOrder(order)
	b.optionOrderEffects[externalID] = effect
	return externalID, nil
}

// SubmitSpreadOrder remains disabled until atomic per-leg persistence and
// rollback semantics are available. Partial paper spreads would be misleading.
func (b *PaperBroker) SubmitSpreadOrder(ctx context.Context, spread *domain.OptionSpread, quantity float64, clientOrderID string) ([]string, error) {
	if err := b.PreflightSpread(ctx, spread, quantity); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if existing, ok := b.optionSpreadOrders[strings.TrimSpace(clientOrderID)]; ok {
		ids := make([]string, 0, len(existing.Legs))
		for _, leg := range existing.Legs {
			ids = append(ids, leg.ExternalID)
		}
		return ids, nil
	}
	var debit, fees float64
	for _, leg := range spread.Legs {
		premium := leg.ExecutablePrice * quantity * float64(leg.Ratio) * leg.Contract.Multiplier
		if leg.Side == domain.OrderSideBuy {
			debit += premium
		} else {
			debit -= premium
		}
		fees += quantity * float64(leg.Ratio) * DefaultOptionFeePerContract
	}
	total := debit + fees
	if b.balance.Cash < total {
		return nil, errors.Join(execution.ErrBrokerOrderRejected, fmt.Errorf("paper: insufficient balance for debit spread: need %.2f, have %.2f", total, b.balance.Cash))
	}
	ids := make([]string, len(spread.Legs))
	now := time.Now().UTC()
	status := execution.BrokerSpreadOrderStatus{ParentExternalID: strings.TrimSpace(clientOrderID), Legs: make([]execution.BrokerSpreadLegStatus, 0, len(spread.Legs))}
	effects := make([]optionPositionEffect, 0, len(spread.Legs))
	for index := range spread.Legs {
		ids[index] = b.nextExternalIDLocked()
		price := spread.Legs[index].ExecutablePrice
		filledAt := now
		status.Legs = append(status.Legs, execution.BrokerSpreadLegStatus{ExternalID: ids[index], Ticker: spread.Legs[index].Contract.OCCSymbol, Status: execution.BrokerOrderStatus{Status: domain.OrderStatusFilled, FilledQuantity: quantity * float64(spread.Legs[index].Ratio), FilledAvgPrice: &price, FilledAt: &filledAt}})
		leg := spread.Legs[index]
		effect, applyErr := b.applyOptionPositionLocked(leg.Contract.OCCSymbol, leg.Contract.Underlying, &leg.Contract.OptionType, &leg.Contract.Strike, &leg.Contract.Expiry, leg.Contract.Multiplier, &leg.PositionIntent, leg.ClosePositionID, quantity*float64(leg.Ratio), price, now)
		if applyErr != nil {
			for rollbackIndex := len(effects) - 1; rollbackIndex >= 0; rollbackIndex-- {
				_ = b.reverseOptionPositionLocked(effects[rollbackIndex])
			}
			return nil, applyErr
		}
		effects = append(effects, effect)
	}
	b.balance.Cash -= total
	b.balance.BuyingPower = b.balance.Cash
	b.balance.Equity = b.markToMarketEquityLocked()
	if b.optionSpreads == nil {
		b.optionSpreads = make(map[string]float64)
	}
	b.optionSpreads[strings.Join(ids, "|")] = total
	b.optionSpreadEffects[strings.Join(ids, "|")] = effects
	b.optionSpreadOrders[strings.TrimSpace(clientOrderID)] = status
	return ids, nil
}

func (b *PaperBroker) GetSpreadOrderStatusByClientOrderIDResult(_ context.Context, clientOrderID string) (execution.BrokerSpreadOrderStatus, error) {
	if b == nil {
		return execution.BrokerSpreadOrderStatus{}, errors.New("paper: broker is required")
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	status, ok := b.optionSpreadOrders[strings.TrimSpace(clientOrderID)]
	if !ok {
		return execution.BrokerSpreadOrderStatus{}, execution.ErrBrokerOrderNotFound
	}
	return status, nil
}

// RollbackOptionOrder compensates an immediate paper option fill when its
// durable lifecycle transaction fails.
func (b *PaperBroker) RollbackOptionOrder(ctx context.Context, externalID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if b == nil || strings.TrimSpace(externalID) == "" {
		return errors.New("paper: option rollback requires an external id")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	order := b.orders[externalID]
	if order == nil {
		return fmt.Errorf("paper: option rollback order %s not found", externalID)
	}
	fill, err := SimulateOptionFill(order)
	if err != nil {
		return fmt.Errorf("paper: reconstruct option rollback: %w", err)
	}
	if order.Side == domain.OrderSideBuy {
		b.balance.Cash += fill.Premium + fill.Fee
	} else {
		b.balance.Cash -= fill.Premium - fill.Fee
	}
	effect, ok := b.optionOrderEffects[externalID]
	if !ok {
		return errors.New("paper: option rollback position effect not found")
	}
	if err := b.reverseOptionPositionLocked(effect); err != nil {
		return err
	}
	order.Status, order.FilledQuantity, order.FilledAvgPrice, order.FilledAt = domain.OrderStatusRejected, 0, nil, nil
	delete(b.optionOrderEffects, externalID)
	b.balance.BuyingPower = b.balance.Cash
	b.balance.Equity = b.markToMarketEquityLocked()
	return nil
}

// RollbackOptionSpread compensates the paper broker's atomic net debit when
// the matching durable multi-leg transaction fails.
func (b *PaperBroker) RollbackOptionSpread(ctx context.Context, externalIDs []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if b == nil || len(externalIDs) == 0 {
		return errors.New("paper: spread rollback requires external ids")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	key := strings.Join(externalIDs, "|")
	total, ok := b.optionSpreads[key]
	if !ok {
		return errors.New("paper: spread rollback record not found")
	}
	b.balance.Cash += total
	for index := len(b.optionSpreadEffects[key]) - 1; index >= 0; index-- {
		if err := b.reverseOptionPositionLocked(b.optionSpreadEffects[key][index]); err != nil {
			return err
		}
	}
	delete(b.optionSpreads, key)
	delete(b.optionSpreadEffects, key)
	for parentID, status := range b.optionSpreadOrders {
		if len(status.Legs) != len(externalIDs) {
			continue
		}
		matches := true
		for i := range externalIDs {
			if status.Legs[i].ExternalID != externalIDs[i] {
				matches = false
				break
			}
		}
		if matches {
			for i := range status.Legs {
				status.Legs[i].Status = execution.BrokerOrderStatus{Status: domain.OrderStatusRejected}
			}
			b.optionSpreadOrders[parentID] = status
			break
		}
	}
	b.balance.BuyingPower = b.balance.Cash
	b.balance.Equity = b.markToMarketEquityLocked()
	return nil
}

// FinalizeOptionSpread discards the transient compensation record after the
// complete durable leg batch commits.
func (b *PaperBroker) FinalizeOptionSpread(externalIDs []string) error {
	if b == nil || len(externalIDs) == 0 {
		return errors.New("paper: spread finalization requires external ids")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	key := strings.Join(externalIDs, "|")
	delete(b.optionSpreads, key)
	delete(b.optionSpreadEffects, key)
	return nil
}

func isOpeningOptionIntent(intent *domain.PositionIntent) bool {
	return intent != nil && (*intent == domain.PositionIntentBuyToOpen || *intent == domain.PositionIntentSellToOpen)
}

func optionPositionSide(intent *domain.PositionIntent) domain.PositionSide {
	if intent != nil && (*intent == domain.PositionIntentSellToOpen || *intent == domain.PositionIntentBuyToClose) {
		return domain.PositionSideShort
	}
	return domain.PositionSideLong
}

func (b *PaperBroker) applyOptionPositionLocked(ticker, underlying string, optionType *domain.OptionType, strike *float64, expiry *time.Time, multiplier float64, intent *domain.PositionIntent, closePositionID uuid.UUID, quantity, price float64, now time.Time) (optionPositionEffect, error) {
	if intent == nil || quantity <= 0 || multiplier <= 0 {
		return optionPositionEffect{}, errors.New("paper: complete option position effect is required")
	}
	key := canonicalPositionKey(domain.MarketTypeOptions, ticker, "")
	side, opening := optionPositionSide(intent), isOpeningOptionIntent(intent)
	effect := optionPositionEffect{ticker: key, side: side, quantity: quantity, price: price, multiplier: multiplier, opened: opening, previous: cloneOptionLots(b.optionLots, key)}
	if opening {
		id := uuid.New()
		b.optionLots[id] = &domain.Position{ID: id, Ticker: ticker, MarketType: domain.MarketTypeOptions, Side: side, Quantity: quantity, AvgEntry: price, CurrentPrice: floatPtr(price), OpenedAt: now, AssetClass: domain.AssetClassOption, UnderlyingTicker: underlying, OptionType: optionType, Strike: strike, Expiry: expiry, ContractMultiplier: multiplier}
		b.pendingOptionLots[key] = append(b.pendingOptionLots[key], id)
		return effect, nil
	}
	remaining := quantity
	for id, position := range b.optionLots {
		if positionKey(position) != key || position.Side != side || remaining <= 0 || (closePositionID != uuid.Nil && id != closePositionID) {
			continue
		}
		closed := math.Min(position.Quantity, remaining)
		position.RealizedPnL += realizedPnL(side, position.AvgEntry, price, closed) * multiplier
		position.Quantity -= closed
		position.CurrentPrice = floatPtr(price)
		remaining -= closed
		if position.Quantity == 0 {
			delete(b.optionLots, id)
		}
	}
	if remaining > 0 {
		b.restoreOptionLotsLocked(key, effect.previous)
		return optionPositionEffect{}, errors.New("paper: option close exceeds broker position")
	}
	return effect, nil
}

func (b *PaperBroker) reverseOptionPositionLocked(effect optionPositionEffect) error {
	b.restoreOptionLotsLocked(effect.ticker, effect.previous)
	return nil
}

func cloneOptionLots(lots map[uuid.UUID]*domain.Position, key string) map[uuid.UUID]*domain.Position {
	result := make(map[uuid.UUID]*domain.Position)
	for id, position := range lots {
		if positionKey(position) == key {
			result[id] = clonePosition(position)
		}
	}
	return result
}

func (b *PaperBroker) restoreOptionLotsLocked(key string, previous map[uuid.UUID]*domain.Position) {
	for id, position := range b.optionLots {
		if positionKey(position) == key {
			delete(b.optionLots, id)
		}
	}
	for id, position := range previous {
		b.optionLots[id] = clonePosition(position)
	}
	queue := b.pendingOptionLots[key][:0]
	for _, id := range b.pendingOptionLots[key] {
		if b.optionLots[id] != nil {
			queue = append(queue, id)
		}
	}
	b.pendingOptionLots[key] = queue
}

// PreflightSpread fails before any leg orders are persisted. Atomic paper
// spread accounting is required before this broker can accept the plan.
func (b *PaperBroker) PreflightSpread(ctx context.Context, spread *domain.OptionSpread, quantity float64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if b == nil || spread == nil || quantity <= 0 {
		return errors.New("paper: valid spread and quantity are required")
	}
	if spread.StrategyType != domain.StrategyBullCallSpread && spread.StrategyType != domain.StrategyBearPutSpread {
		return errors.New("paper: only debit vertical spreads are enabled")
	}
	if len(spread.Legs) != 2 {
		return errors.New("paper: debit vertical requires two legs")
	}
	first := spread.Legs[0]
	var openBuys, openSells, closeBuys, closeSells int
	var netDebit float64
	for _, leg := range spread.Legs {
		if strings.TrimSpace(leg.Contract.OCCSymbol) == "" || leg.Contract.Expiry.IsZero() || leg.Contract.Multiplier <= 0 || leg.Ratio != 1 || !isFinitePositiveOptionPrice(leg.ExecutablePrice) {
			return errors.New("paper: each debit spread leg requires contract metadata, 1:1 ratio, and executable price")
		}
		if leg.Contract.Expiry != first.Contract.Expiry || leg.Contract.OptionType != first.Contract.OptionType || leg.Contract.Multiplier != first.Contract.Multiplier {
			return errors.New("paper: debit vertical legs must share type, expiry, and multiplier")
		}
		switch {
		case leg.Side == domain.OrderSideBuy && leg.PositionIntent == domain.PositionIntentBuyToOpen:
			openBuys++
			netDebit += leg.ExecutablePrice
		case leg.Side == domain.OrderSideSell && leg.PositionIntent == domain.PositionIntentSellToOpen:
			openSells++
			netDebit -= leg.ExecutablePrice
		case leg.Side == domain.OrderSideBuy && leg.PositionIntent == domain.PositionIntentBuyToClose:
			closeBuys++
			netDebit += leg.ExecutablePrice
		case leg.Side == domain.OrderSideSell && leg.PositionIntent == domain.PositionIntentSellToClose:
			closeSells++
			netDebit -= leg.ExecutablePrice
		default:
			return errors.New("paper: debit vertical requires consistent open or close intents")
		}
	}
	if openBuys == 1 && openSells == 1 {
		if spread.MaxRisk <= 0 || spread.MaxReward <= 0 {
			return errors.New("paper: opening debit vertical requires finite max risk/reward")
		}
		if netDebit <= 0 || !isFinitePositiveOptionPrice(netDebit) {
			return errors.New("paper: spread must be a net debit with one bought and one sold leg")
		}
		return nil
	}
	if closeBuys != 1 || closeSells != 1 || math.IsNaN(netDebit) || math.IsInf(netDebit, 0) {
		return errors.New("paper: spread must be a net debit with one bought and one sold leg")
	}
	return nil
}

func isFinitePositiveOptionPrice(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

// OptionFillReport returns deterministic accounting for a synchronous paper fill.
func (b *PaperBroker) OptionFillReport(_ context.Context, order *domain.Order) (execution.OptionFillReport, error) {
	result, err := SimulateOptionFill(order)
	if err != nil {
		return execution.OptionFillReport{}, err
	}
	return execution.OptionFillReport{Premium: result.Premium, Fee: result.Fee}, nil
}

var _ interface {
	SubmitOptionOrder(context.Context, *domain.Order) (string, error)
	SubmitSpreadOrder(context.Context, *domain.OptionSpread, float64, string) ([]string, error)
} = (*PaperBroker)(nil)

// IsExpired checks if an options position has expired at the given time.
// A position is expired when the current time is after the contract expiry date.
func IsExpired(position *domain.Position, now time.Time) bool {
	if position == nil || position.Expiry == nil {
		return false
	}
	return now.After(*position.Expiry)
}

// ExerciseValue returns the intrinsic value of an option at the given underlying
// price. Returns 0 for out-of-the-money options.
func ExerciseValue(optType domain.OptionType, strike, underlyingPrice float64) float64 {
	switch optType {
	case domain.OptionTypeCall:
		if underlyingPrice > strike {
			return underlyingPrice - strike
		}
		return 0
	case domain.OptionTypePut:
		if strike > underlyingPrice {
			return strike - underlyingPrice
		}
		return 0
	default:
		return 0
	}
}

// ApplyOptionFill applies a simulated options fill to the paper broker's position
// book. This is intended to be called from PaperBroker.SubmitOrder when the order
// has AssetClass == domain.AssetClassOption.
func ApplyOptionFill(order *domain.Order, result *OptionsFillResult) error {
	if order == nil || result == nil {
		return errors.New("paper: order and fill result are required")
	}
	if result.FillPrice <= 0 {
		return fmt.Errorf("paper: invalid fill price %.4f", result.FillPrice)
	}

	fillPrice := result.FillPrice
	order.FilledQuantity = result.Quantity
	order.FilledAvgPrice = &fillPrice
	order.Status = domain.OrderStatusFilled

	return nil
}
