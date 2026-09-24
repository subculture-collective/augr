package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	alpacaexec "github.com/PatrickFanella/get-rich-quick/internal/execution/alpaca"
)

const alpacaActivitiesPageSize = 100

// AlpacaClientAdapter adapts the Alpaca execution client into reconciliation snapshots.
type AlpacaClientAdapter struct {
	client *alpacaexec.Client
	broker *alpacaexec.Broker
	logger *slog.Logger

	// fillWatermarkMu guards the in-memory fill watermark. The first ListFills
	// in a process walks the full history; later calls start one day before
	// the newest fill already reconciled so late-arriving activities are
	// still observed without re-walking every page.
	fillWatermarkMu sync.Mutex
	fillWatermark   time.Time
}

const (
	alpacaOrdersPageSize    = 500
	alpacaFillLookbackSlack = 24 * time.Hour
)

type alpacaOrderResponse struct {
	ID             string `json:"id"`
	ClientOrderID  string `json:"client_order_id"`
	Symbol         string `json:"symbol"`
	AssetClass     string `json:"asset_class"`
	Side           string `json:"side"`
	Type           string `json:"type"`
	Qty            string `json:"qty"`
	LimitPrice     string `json:"limit_price"`
	StopPrice      string `json:"stop_price"`
	FilledQty      string `json:"filled_qty"`
	FilledAvgPrice string `json:"filled_avg_price"`
	Status         string `json:"status"`
	SubmittedAt    string `json:"submitted_at"`
	FilledAt       string `json:"filled_at"`
}

type alpacaFillActivityResponse struct {
	ActivityType    string `json:"activity_type"`
	ID              string `json:"id"`
	OrderID         string `json:"order_id"`
	Symbol          string `json:"symbol"`
	Side            string `json:"side"`
	Qty             string `json:"qty"`
	Price           string `json:"price"`
	TransactionTime string `json:"transaction_time"`
	OrderStatus     string `json:"order_status"`
	Type            string `json:"type"`
}

func NewAlpacaClientAdapter(client *alpacaexec.Client) *AlpacaClientAdapter {
	return &AlpacaClientAdapter{
		client: client,
		broker: alpacaexec.NewBroker(client),
		logger: slog.Default(),
	}
}

func (a *AlpacaClientAdapter) GetPositions(ctx context.Context) ([]domain.Position, error) {
	if a == nil || a.broker == nil {
		return nil, errors.New("alpaca: reconciliation broker is required")
	}
	return a.broker.GetPositions(ctx)
}

func (a *AlpacaClientAdapter) GetAccountBalance(ctx context.Context) (execution.Balance, error) {
	if a == nil || a.broker == nil {
		return execution.Balance{}, errors.New("alpaca: reconciliation broker is required")
	}
	return a.broker.GetAccountBalance(ctx)
}

func (a *AlpacaClientAdapter) GetAccountSnapshot(ctx context.Context) (BrokerAccountSnapshot, error) {
	balance, err := a.GetAccountBalance(ctx)
	if err != nil {
		return BrokerAccountSnapshot{}, err
	}
	return BrokerAccountSnapshot{Cash: balance.Cash, Equity: balance.Equity}, nil
}

func (a *AlpacaClientAdapter) ListOrders(ctx context.Context) ([]BrokerOrderSnapshot, error) {
	if a == nil || a.client == nil {
		return nil, errors.New("alpaca: reconciliation client is required")
	}

	var (
		orders []BrokerOrderSnapshot
		after  string
		seen   = map[string]struct{}{}
	)
	for {
		params := url.Values{
			"status":    {"all"},
			"limit":     {strconv.Itoa(alpacaOrdersPageSize)},
			"direction": {"asc"},
		}
		if after != "" {
			params.Set("after", after)
		}
		responseBody, err := a.client.Get(ctx, "/v2/orders", params)
		if err != nil {
			return nil, fmt.Errorf("alpaca: list orders: %w", err)
		}

		var response []alpacaOrderResponse
		if err := json.Unmarshal(responseBody, &response); err != nil {
			return nil, fmt.Errorf("alpaca: decode orders response: %w", err)
		}

		newest := ""
		for _, raw := range response {
			if _, dup := seen[raw.ID]; dup {
				continue
			}
			seen[raw.ID] = struct{}{}
			order, err := mapAlpacaOrderSnapshot(raw)
			if err != nil {
				return nil, err
			}
			orders = append(orders, order)
			if strings.TrimSpace(raw.SubmittedAt) != "" {
				newest = raw.SubmittedAt
			}
		}
		if len(response) < alpacaOrdersPageSize || newest == "" || newest == after {
			break
		}
		after = newest
	}
	return orders, nil
}

func (a *AlpacaClientAdapter) ListFills(ctx context.Context) ([]BrokerFillSnapshot, error) {
	if a == nil || a.client == nil {
		return nil, errors.New("alpaca: reconciliation client is required")
	}

	var (
		fills     []BrokerFillSnapshot
		pageToken string
		newest    time.Time
	)
	a.fillWatermarkMu.Lock()
	watermark := a.fillWatermark
	a.fillWatermarkMu.Unlock()
	after := ""
	if !watermark.IsZero() {
		after = watermark.Add(-alpacaFillLookbackSlack).UTC().Format(time.RFC3339Nano)
	}
	for {
		params := url.Values{
			"direction": {"desc"},
			"page_size": {strconv.Itoa(alpacaActivitiesPageSize)},
		}
		if after != "" {
			params.Set("after", after)
		}
		if strings.TrimSpace(pageToken) != "" {
			params.Set("page_token", pageToken)
		}

		responseBody, err := a.client.Get(ctx, "/v2/account/activities/FILL", params)
		if err != nil {
			return nil, fmt.Errorf("alpaca: list fills: %w", err)
		}

		var response []alpacaFillActivityResponse
		if err := json.Unmarshal(responseBody, &response); err != nil {
			return nil, fmt.Errorf("alpaca: decode fills response: %w", err)
		}
		if len(response) == 0 {
			break
		}

		for _, raw := range response {
			fill, err := mapAlpacaFillSnapshot(raw)
			if err != nil {
				return nil, err
			}
			fills = append(fills, fill)
			if fill.ExecutedAt.After(newest) {
				newest = fill.ExecutedAt
			}
		}

		if len(response) < alpacaActivitiesPageSize {
			break
		}
		pageToken = response[len(response)-1].ID
	}
	if !newest.IsZero() {
		a.fillWatermarkMu.Lock()
		if newest.After(a.fillWatermark) {
			a.fillWatermark = newest
		}
		a.fillWatermarkMu.Unlock()
	}
	return fills, nil
}

func mapAlpacaOrderSnapshot(raw alpacaOrderResponse) (BrokerOrderSnapshot, error) {
	externalID := strings.TrimSpace(raw.ID)
	if externalID == "" {
		return BrokerOrderSnapshot{}, errors.New("alpaca: order id is required")
	}
	ticker := strings.TrimSpace(raw.Symbol)
	if ticker == "" {
		return BrokerOrderSnapshot{}, errors.New("alpaca: order symbol is required")
	}
	side, err := mapAlpacaOrderSide(raw.Side)
	if err != nil {
		return BrokerOrderSnapshot{}, err
	}
	orderType, err := mapAlpacaOrderType(raw.Type)
	if err != nil {
		return BrokerOrderSnapshot{}, err
	}
	quantity, err := parseRequiredFloat("qty", raw.Qty)
	if err != nil {
		return BrokerOrderSnapshot{}, err
	}
	filledQty, err := parseRequiredFloat("filled_qty", raw.FilledQty)
	if err != nil {
		return BrokerOrderSnapshot{}, err
	}
	limitPrice, err := parseOptionalFloat("limit_price", raw.LimitPrice)
	if err != nil {
		return BrokerOrderSnapshot{}, err
	}
	stopPrice, err := parseOptionalFloat("stop_price", raw.StopPrice)
	if err != nil {
		return BrokerOrderSnapshot{}, err
	}
	filledAvgPrice, err := parseOptionalFloat("filled_avg_price", raw.FilledAvgPrice)
	if err != nil {
		return BrokerOrderSnapshot{}, err
	}
	status, err := mapAlpacaOrderStatus(raw.Status)
	if err != nil {
		return BrokerOrderSnapshot{}, err
	}
	submittedAt, err := parseOptionalTime("submitted_at", raw.SubmittedAt)
	if err != nil {
		return BrokerOrderSnapshot{}, err
	}
	filledAt, err := parseOptionalTime("filled_at", raw.FilledAt)
	if err != nil {
		return BrokerOrderSnapshot{}, err
	}

	snapshot := BrokerOrderSnapshot{
		ExternalID:     externalID,
		ClientOrderID:  strings.TrimSpace(raw.ClientOrderID),
		Ticker:         ticker,
		Side:           side,
		OrderType:      orderType,
		Quantity:       quantity,
		LimitPrice:     limitPrice,
		StopPrice:      stopPrice,
		FilledQuantity: filledQty,
		FilledAvgPrice: filledAvgPrice,
		Status:         status,
		SubmittedAt:    submittedAt,
		FilledAt:       filledAt,
		Broker:         "alpaca",
	}
	if strings.EqualFold(strings.TrimSpace(raw.AssetClass), "us_option") {
		contract, parseErr := domain.ParseOCC(ticker)
		if parseErr != nil {
			return BrokerOrderSnapshot{}, fmt.Errorf("alpaca: parse option order symbol: %w", parseErr)
		}
		snapshot.MarketType, snapshot.AssetClass = domain.MarketTypeOptions, domain.AssetClassOption
		snapshot.UnderlyingTicker, snapshot.OptionType, snapshot.Strike, snapshot.Expiry, snapshot.ContractMultiplier = contract.Underlying, &contract.OptionType, &contract.Strike, &contract.Expiry, contract.Multiplier
	}
	return snapshot, nil
}

func mapAlpacaFillSnapshot(raw alpacaFillActivityResponse) (BrokerFillSnapshot, error) {
	activityID := strings.TrimSpace(raw.ID)
	if activityID == "" {
		return BrokerFillSnapshot{}, errors.New("alpaca: fill activity id is required")
	}
	externalID := strings.TrimSpace(raw.OrderID)
	if externalID == "" {
		return BrokerFillSnapshot{}, errors.New("alpaca: fill order id is required")
	}
	ticker := strings.TrimSpace(raw.Symbol)
	if ticker == "" {
		return BrokerFillSnapshot{}, errors.New("alpaca: fill symbol is required")
	}
	side, err := mapAlpacaOrderSide(raw.Side)
	if err != nil {
		return BrokerFillSnapshot{}, err
	}
	quantity, err := parseRequiredFloat("qty", raw.Qty)
	if err != nil {
		return BrokerFillSnapshot{}, err
	}
	price, err := parseRequiredFloat("price", raw.Price)
	if err != nil {
		return BrokerFillSnapshot{}, err
	}
	executedAt, err := parseRequiredTime("transaction_time", raw.TransactionTime)
	if err != nil {
		return BrokerFillSnapshot{}, err
	}
	status, err := mapAlpacaFillOrderStatus(raw.OrderStatus, raw.Type)
	if err != nil {
		return BrokerFillSnapshot{}, err
	}

	return BrokerFillSnapshot{
		ActivityID:  activityID,
		ExternalID:  externalID,
		Ticker:      ticker,
		Side:        side,
		Quantity:    quantity,
		Price:       price,
		ExecutedAt:  executedAt,
		OrderStatus: status,
		Fee:         0,
	}, nil
}

func mapAlpacaOrderSide(raw string) (domain.OrderSide, error) {
	switch side := strings.ToLower(strings.TrimSpace(raw)); side {
	case string(domain.OrderSideBuy):
		return domain.OrderSideBuy, nil
	case string(domain.OrderSideSell):
		return domain.OrderSideSell, nil
	default:
		return "", fmt.Errorf("alpaca: unsupported order side %q", raw)
	}
}

func mapAlpacaOrderType(raw string) (domain.OrderType, error) {
	switch orderType := strings.ToLower(strings.TrimSpace(raw)); orderType {
	case string(domain.OrderTypeMarket):
		return domain.OrderTypeMarket, nil
	case string(domain.OrderTypeLimit):
		return domain.OrderTypeLimit, nil
	case string(domain.OrderTypeStop):
		return domain.OrderTypeStop, nil
	case string(domain.OrderTypeStopLimit):
		return domain.OrderTypeStopLimit, nil
	case string(domain.OrderTypeTrailingStop):
		return domain.OrderTypeTrailingStop, nil
	default:
		return "", fmt.Errorf("alpaca: unsupported order type %q", raw)
	}
}

func mapAlpacaOrderStatus(raw string) (domain.OrderStatus, error) {
	switch status := strings.ToLower(strings.TrimSpace(raw)); status {
	case "":
		return "", errors.New("alpaca: order status is required")
	case "accepted_for_bidding", "calculated", "held", "pending_cancel", "pending_new", "pending_replace":
		return domain.OrderStatusPending, nil
	case "accepted", "done_for_day", "new", "replaced", "stopped", "suspended":
		return domain.OrderStatusSubmitted, nil
	case "partially_filled":
		return domain.OrderStatusPartial, nil
	case "filled":
		return domain.OrderStatusFilled, nil
	case "canceled", "expired":
		return domain.OrderStatusCancelled, nil
	case "rejected":
		return domain.OrderStatusRejected, nil
	default:
		return "", fmt.Errorf("alpaca: unsupported order status %q", raw)
	}
}

func mapAlpacaFillOrderStatus(rawStatus, rawType string) (domain.OrderStatus, error) {
	if strings.TrimSpace(rawStatus) != "" {
		return mapAlpacaOrderStatus(rawStatus)
	}
	switch fillType := strings.ToLower(strings.TrimSpace(rawType)); fillType {
	case "partial_fill":
		return domain.OrderStatusPartial, nil
	case "fill":
		return domain.OrderStatusFilled, nil
	case "":
		return "", errors.New("alpaca: fill order status is required")
	default:
		return "", fmt.Errorf("alpaca: unsupported fill type %q", rawType)
	}
}

func parseRequiredFloat(fieldName, value string) (float64, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, fmt.Errorf("alpaca: %s is required", fieldName)
	}
	parsed, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, fmt.Errorf("alpaca: parse %s: %w", fieldName, err)
	}
	return parsed, nil
}

func parseOptionalFloat(fieldName, value string) (*float64, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return nil, fmt.Errorf("alpaca: parse %s: %w", fieldName, err)
	}
	return &parsed, nil
}

func parseRequiredTime(fieldName, value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, fmt.Errorf("alpaca: %s is required", fieldName)
	}
	parsed, err := time.Parse(time.RFC3339Nano, trimmed)
	if err != nil {
		return time.Time{}, fmt.Errorf("alpaca: parse %s: %w", fieldName, err)
	}
	return parsed.UTC(), nil
}

func parseOptionalTime(fieldName, value string) (*time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, trimmed)
	if err != nil {
		return nil, fmt.Errorf("alpaca: parse %s: %w", fieldName, err)
	}
	parsed = parsed.UTC()
	return &parsed, nil
}
