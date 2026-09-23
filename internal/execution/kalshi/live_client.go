package kalshi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/execution"
)

// signedClient is the minimal authenticated Kalshi transport used by the live adapter.
type signedClient interface {
	Get(ctx context.Context, path string, query url.Values, authenticated bool) ([]byte, error)
	Post(ctx context.Context, path string, body any) ([]byte, error)
	Delete(ctx context.Context, path string, query url.Values) ([]byte, error)
}

// HTTPClient adapts the signed Kalshi transport to the execution LiveClient boundary.
type HTTPClient struct {
	client signedClient
}

// NewLiveHTTPClient constructs a live execution adapter backed by the signed Kalshi HTTP client.
func NewLiveHTTPClient(client signedClient) (*HTTPClient, error) {
	if client == nil {
		return nil, errors.New("kalshi: live client is required")
	}
	return &HTTPClient{client: client}, nil
}

func (c *HTTPClient) CreateOrder(ctx context.Context, req CreateOrderRequest) (CreateOrderResponse, error) {
	if c == nil || c.client == nil {
		return CreateOrderResponse{}, errors.New("kalshi: live client is required")
	}
	payload, err := buildCreateOrderPayload(req)
	if err != nil {
		return CreateOrderResponse{}, err
	}

	// POST is deliberately not retried by the transport; a timeout is recovered
	// through GetOrderByClientOrderID instead of a duplicate submission.
	body, err := c.client.Post(ctx, "/portfolio/orders", payload)
	if err != nil {
		return CreateOrderResponse{}, fmt.Errorf("kalshi: create order: %w", err)
	}

	var resp createOrderEnvelope
	if err := json.Unmarshal(body, &resp); err != nil {
		return CreateOrderResponse{}, fmt.Errorf("kalshi: decode create order response: %w", err)
	}
	orderID := firstNonEmpty(strings.TrimSpace(resp.Order.OrderID), strings.TrimSpace(resp.OrderID))
	if orderID == "" {
		return CreateOrderResponse{}, errors.New("kalshi: create order response missing order id")
	}
	return CreateOrderResponse{OrderID: orderID}, nil
}

func (c *HTTPClient) CancelOrder(ctx context.Context, orderID string) error {
	if c == nil || c.client == nil {
		return errors.New("kalshi: live client is required")
	}
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return errors.New("kalshi: order id is required")
	}
	if _, err := c.client.Delete(ctx, "/portfolio/orders/"+url.PathEscape(orderID), nil); err != nil {
		return fmt.Errorf("kalshi: cancel order: %w", err)
	}
	return nil
}

func (c *HTTPClient) GetOrder(ctx context.Context, orderID string) (OrderResponse, error) {
	if c == nil || c.client == nil {
		return OrderResponse{}, errors.New("kalshi: live client is required")
	}
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return OrderResponse{}, errors.New("kalshi: order id is required")
	}

	body, err := c.client.Get(ctx, "/portfolio/orders/"+url.PathEscape(orderID), nil, true)
	if err != nil {
		return OrderResponse{}, fmt.Errorf("kalshi: get order: %w", err)
	}

	var resp getOrderEnvelope
	if err := json.Unmarshal(body, &resp); err != nil {
		return OrderResponse{}, fmt.Errorf("kalshi: decode get order response: %w", err)
	}
	return mapWireOrder(resp.Order, orderID)
}

// clientOrderLookupStatuses are the /portfolio/orders status filters scanned
// when recovering an order by client_order_id. Kalshi does not filter by
// client_order_id server-side, so every page is matched client-side.
var clientOrderLookupStatuses = []string{"resting", "executed", "canceled"}

const (
	clientOrderLookupPageLimit = 200
	clientOrderLookupMaxPages  = 10
)

func (c *HTTPClient) GetOrderByClientOrderID(ctx context.Context, clientOrderID string) (OrderResponse, error) {
	if c == nil || c.client == nil {
		return OrderResponse{}, errors.New("kalshi: live client is required")
	}
	clientOrderID = strings.TrimSpace(clientOrderID)
	if clientOrderID == "" {
		return OrderResponse{}, errors.New("kalshi: client order id is required")
	}
	matches := map[string]OrderResponse{}
	for _, status := range clientOrderLookupStatuses {
		cursor := ""
		for page := 0; page < clientOrderLookupMaxPages; page++ {
			if err := ctx.Err(); err != nil {
				return OrderResponse{}, err
			}
			query := url.Values{}
			query.Set("status", status)
			query.Set("limit", strconv.Itoa(clientOrderLookupPageLimit))
			if cursor != "" {
				query.Set("cursor", cursor)
			}
			body, err := c.client.Get(ctx, "/portfolio/orders", query, true)
			if err != nil {
				return OrderResponse{}, fmt.Errorf("kalshi: lookup client order id: %w", err)
			}
			var response struct {
				Orders []json.RawMessage `json:"orders"`
				Cursor string            `json:"cursor"`
			}
			if err := json.Unmarshal(body, &response); err != nil {
				return OrderResponse{}, fmt.Errorf("kalshi: decode client order lookup: %w", err)
			}
			for _, raw := range response.Orders {
				if len(raw) == 0 || string(raw) == "null" {
					continue
				}
				candidate, decodeErr := decodeOrderResponse(raw, "")
				if decodeErr != nil {
					return OrderResponse{}, decodeErr
				}
				if candidate.ClientOrderID != clientOrderID || strings.TrimSpace(candidate.OrderID) == "" {
					continue
				}
				matches[candidate.OrderID] = candidate
			}
			cursor = strings.TrimSpace(response.Cursor)
			if cursor == "" || len(response.Orders) == 0 {
				break
			}
		}
	}
	if len(matches) > 1 {
		return OrderResponse{}, fmt.Errorf("kalshi: client order lookup returned %d matches", len(matches))
	}
	for _, match := range matches {
		return match, nil
	}
	return OrderResponse{}, execution.ErrBrokerOrderNotFound
}

func decodeOrderResponse(raw []byte, fallbackID string) (OrderResponse, error) {
	var order wireOrder
	if err := json.Unmarshal(raw, &order); err != nil {
		return OrderResponse{}, fmt.Errorf("kalshi: decode order response: %w", err)
	}
	return mapWireOrder(order, fallbackID)
}

func mapWireOrder(order wireOrder, fallbackID string) (OrderResponse, error) {
	status := strings.TrimSpace(order.Status)
	if status == "" {
		remaining, parseErr := parseFixedFloat(order.RemainingCountFP)
		if parseErr == nil && remaining == 0 {
			status = "executed"
		} else {
			return OrderResponse{}, errors.New("kalshi: order status is required")
		}
	}
	if strings.TrimSpace(order.OrderID) == "" {
		order.OrderID = fallbackID
	}
	filled := float64(0)
	if strings.TrimSpace(order.FillCountFP) != "" {
		var err error
		filled, err = parseFixedFloat(order.FillCountFP)
		if err != nil {
			return OrderResponse{}, fmt.Errorf("kalshi: parse filled count: %w", err)
		}
	}
	if filled != math.Trunc(filled) {
		return OrderResponse{}, errors.New("kalshi: filled count is not whole-contract")
	}
	var average *float64
	if strings.TrimSpace(order.AveragePriceDollars) != "" {
		value, parseErr := parseFixedFloat(order.AveragePriceDollars)
		if parseErr != nil {
			return OrderResponse{}, fmt.Errorf("kalshi: parse average fill price: %w", parseErr)
		}
		average = &value
	}
	var filledAt *time.Time
	if strings.TrimSpace(order.LastUpdateTime) != "" {
		value, parseErr := time.Parse(time.RFC3339Nano, order.LastUpdateTime)
		if parseErr != nil {
			return OrderResponse{}, fmt.Errorf("kalshi: parse order update time: %w", parseErr)
		}
		filledAt = &value
	}
	return OrderResponse{OrderID: strings.TrimSpace(order.OrderID), ClientOrderID: strings.TrimSpace(order.ClientOrderID), Status: status, FilledCount: int64(filled), AveragePrice: average, FilledAt: filledAt}, nil
}

func (c *HTTPClient) ListPositions(ctx context.Context) ([]PositionResponse, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("kalshi: live client is required")
	}
	var positions []PositionResponse
	cursor := ""
	for {
		query := url.Values{}
		query.Set("limit", "1000")
		query.Set("count_filter", "position")
		if cursor != "" {
			query.Set("cursor", cursor)
		}

		body, err := c.client.Get(ctx, "/portfolio/positions", query, true)
		if err != nil {
			return nil, fmt.Errorf("kalshi: list positions: %w", err)
		}

		var resp listPositionsEnvelope
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("kalshi: decode positions response: %w", err)
		}

		for _, marketPosition := range resp.MarketPositions {
			position, err := mapMarketPosition(marketPosition)
			if err != nil {
				return nil, err
			}
			positions = append(positions, position)
		}

		nextCursor := strings.TrimSpace(resp.NextCursor)
		if nextCursor == "" {
			nextCursor = strings.TrimSpace(resp.Cursor)
		}
		if nextCursor == "" || nextCursor == cursor {
			return positions, nil
		}
		cursor = nextCursor
	}
}

func (c *HTTPClient) GetBalance(ctx context.Context) (BalanceResponse, error) {
	if c == nil || c.client == nil {
		return BalanceResponse{}, errors.New("kalshi: live client is required")
	}
	body, err := c.client.Get(ctx, "/portfolio/balance", nil, true)
	if err != nil {
		return BalanceResponse{}, fmt.Errorf("kalshi: get balance: %w", err)
	}

	var resp balanceEnvelope
	if err := json.Unmarshal(body, &resp); err != nil {
		return BalanceResponse{}, fmt.Errorf("kalshi: decode balance response: %w", err)
	}

	return BalanceResponse{
		CashCents:        resp.Balance,
		BuyingPowerCents: resp.Balance,
		EquityCents:      resp.PortfolioValue,
	}, nil
}

type createOrderEnvelope struct {
	OrderID string `json:"order_id"`
	Order   struct {
		OrderID string `json:"order_id"`
	} `json:"order"`
}

type getOrderEnvelope struct {
	Order wireOrder `json:"order"`
}

type wireOrder struct {
	OrderID             string `json:"order_id"`
	ClientOrderID       string `json:"client_order_id"`
	Status              string `json:"status"`
	RemainingCountFP    string `json:"remaining_count_fp"`
	FillCountFP         string `json:"fill_count_fp"`
	AveragePriceDollars string `json:"average_price_dollars"`
	LastUpdateTime      string `json:"last_update_time"`
}

type listPositionsEnvelope struct {
	MarketPositions []marketPosition `json:"market_positions"`
	Cursor          string           `json:"cursor"`
	NextCursor      string           `json:"next_cursor"`
}

type marketPosition struct {
	Ticker                string `json:"ticker"`
	PositionFP            string `json:"position_fp"`
	MarketExposureDollars string `json:"market_exposure_dollars"`
}

type balanceEnvelope struct {
	Balance        int64 `json:"balance"`
	PortfolioValue int64 `json:"portfolio_value"`
}

// buildCreateOrderPayload maps the request onto the Kalshi Trade API v2
// POST /portfolio/orders body:
//
//	{ticker, action: buy|sell, side: yes|no, count: <int>, type: "limit",
//	 yes_price|no_price: <cents>, client_order_id, time_in_force}
//
// The price is expressed for the contract side actually traded; Kalshi
// derives the opposite book itself. Only limit orders are supported; market
// orders are rejected at planning time in mapCreateOrderRequest and again
// here as a guard.
func buildCreateOrderPayload(req CreateOrderRequest) (map[string]any, error) {
	if strings.TrimSpace(req.Ticker) == "" {
		return nil, errors.New("kalshi: ticker is required")
	}
	if req.Count <= 0 {
		return nil, errors.New("kalshi: count must be positive")
	}
	side, err := mapContractSide(req.Side)
	if err != nil {
		return nil, err
	}
	action, err := mapOrderAction(req.Action)
	if err != nil {
		return nil, err
	}
	orderType := strings.ToLower(strings.TrimSpace(req.Type))
	switch orderType {
	case "limit":
	case "":
		return nil, errors.New("kalshi: order type is required")
	case "market":
		return nil, errors.New("kalshi: market orders are disabled until sandbox smoke-tested")
	default:
		return nil, fmt.Errorf("kalshi: unsupported order type %q", req.Type)
	}

	payload := map[string]any{
		"ticker":        strings.TrimSpace(req.Ticker),
		"action":        action,
		"side":          side,
		"count":         req.Count,
		"type":          "limit",
		"time_in_force": timeInForceForOrder(orderType),
	}
	switch side {
	case "yes":
		if req.YesPrice == nil {
			return nil, errors.New("kalshi: yes price is required")
		}
		cents, err := validateQuoteCents(*req.YesPrice)
		if err != nil {
			return nil, err
		}
		payload["yes_price"] = cents
	case "no":
		if req.NoPrice == nil {
			return nil, errors.New("kalshi: no price is required")
		}
		cents, err := validateQuoteCents(*req.NoPrice)
		if err != nil {
			return nil, err
		}
		payload["no_price"] = cents
	}
	if clientOrderID := strings.TrimSpace(req.ClientOrderID); clientOrderID != "" {
		payload["client_order_id"] = clientOrderID
	}
	return payload, nil
}

func mapContractSide(raw string) (string, error) {
	switch side := strings.ToLower(strings.TrimSpace(raw)); side {
	case "yes", "no":
		return side, nil
	default:
		return "", fmt.Errorf("kalshi: unsupported side %q", raw)
	}
}

func mapOrderAction(raw string) (string, error) {
	switch action := strings.ToLower(strings.TrimSpace(raw)); action {
	case "buy", "sell":
		return action, nil
	default:
		return "", fmt.Errorf("kalshi: unsupported action %q", raw)
	}
}

func validateQuoteCents(cents int64) (int64, error) {
	if cents <= 0 || cents >= 100 {
		return 0, fmt.Errorf("kalshi: quote cents %d out of range", cents)
	}
	return cents, nil
}

func timeInForceForOrder(orderType string) string {
	switch strings.ToLower(strings.TrimSpace(orderType)) {
	case "market":
		return "immediate_or_cancel"
	default:
		return "good_till_canceled"
	}
}

func mapMarketPosition(resp marketPosition) (PositionResponse, error) {
	ticker := strings.TrimSpace(resp.Ticker)
	if ticker == "" {
		return PositionResponse{}, errors.New("kalshi: position ticker is required")
	}
	positionFP, err := parseFixedFloat(resp.PositionFP)
	if err != nil {
		return PositionResponse{}, fmt.Errorf("kalshi: parse position count: %w", err)
	}
	exposureDollars, err := parseFixedFloat(resp.MarketExposureDollars)
	if err != nil {
		return PositionResponse{}, fmt.Errorf("kalshi: parse market exposure: %w", err)
	}
	position := PositionResponse{Ticker: ticker}
	if positionFP < 0 {
		position.Side = "no"
		position.Count = int64(math.Round(math.Abs(positionFP)))
	} else {
		position.Side = "yes"
		position.Count = int64(math.Round(positionFP))
	}
	position.ValueCents = int64(math.Round(math.Abs(exposureDollars) * 100))
	return position, nil
}

func parseFixedFloat(raw string) (float64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, errors.New("value is required")
	}
	return strconv.ParseFloat(trimmed, 64)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
