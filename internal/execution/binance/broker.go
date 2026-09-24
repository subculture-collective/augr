package binance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
)

const (
	defaultTimeInForce   = "GTC"
	orderExternalIDDelim = ":"
)

var preferredBalanceAssets = []string{"USDT", "USDC", "BUSD", "FDUSD", "TUSD", "USD"}

// quoteAssets are cash-equivalent balances that GetPositions excludes; they
// are reported through GetAccountBalance instead of as long positions.
var quoteAssets = map[string]struct{}{"USDT": {}, "USDC": {}, "BUSD": {}, "FDUSD": {}, "TUSD": {}, "USD": {}}

// Broker implements the execution.Broker interface for Binance spot trading.
type Broker struct {
	client  *Client
	filters *symbolFilterCache
}

// orderResponse is the shared shape of POST /api/v3/order (FULL/RESULT) and
// GET /api/v3/order responses; fields absent from ACK responses stay zero.
type orderResponse struct {
	Symbol              string      `json:"symbol"`
	OrderID             int64       `json:"orderId"`
	ClientOrderID       string      `json:"clientOrderId"`
	Status              string      `json:"status"`
	ExecutedQty         string      `json:"executedQty"`
	CummulativeQuoteQty string      `json:"cummulativeQuoteQty"`
	TransactTime        int64       `json:"transactTime"`
	UpdateTime          int64       `json:"updateTime"`
	Fills               []orderFill `json:"fills"`
}

type orderFill struct {
	Price           string `json:"price"`
	Qty             string `json:"qty"`
	Commission      string `json:"commission"`
	CommissionAsset string `json:"commissionAsset"`
}

type accountResponse struct {
	Balances []accountBalance `json:"balances"`
}

type accountBalance struct {
	Asset  string `json:"asset"`
	Free   string `json:"free"`
	Locked string `json:"locked"`
}

// NewBroker constructs a Binance broker adapter.
func NewBroker(client *Client) *Broker {
	var now func() time.Time
	if client != nil {
		now = client.nowFunc()
	}
	return &Broker{client: client, filters: newSymbolFilterCache(symbolFiltersTTL, now)}
}

// SubmitOrder sends a spot order to Binance and returns a composite external order ID.
func (b *Broker) SubmitOrder(ctx context.Context, order *domain.Order) (string, error) {
	if b == nil || b.client == nil {
		return "", errors.New("binance: broker client is required")
	}
	if order == nil {
		return "", errors.New("binance: order is required")
	}

	params, symbol, err := mapSubmitOrderParams(order)
	if err != nil {
		return "", err
	}
	filters, err := b.symbolFiltersFor(ctx, symbol)
	if err != nil {
		return "", err
	}
	shaped, err := applySymbolFilters(order, filters)
	if err != nil {
		return "", err
	}
	params.Set("quantity", shaped.Quantity)
	if shaped.Price != "" {
		params.Set("price", shaped.Price)
	}
	if clientOrderID := strings.TrimSpace(order.ClientOrderID); clientOrderID != "" {
		params.Set("newClientOrderId", clientOrderID)
	}
	params.Set("newOrderRespType", "FULL")

	responseBody, err := b.client.SignedPost(ctx, "/api/v3/order", params)
	if err != nil {
		return "", fmt.Errorf("binance: submit order: %w", err)
	}

	var response orderResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return "", fmt.Errorf("binance: decode submit order response: %w", err)
	}
	if response.OrderID <= 0 {
		return "", errors.New("binance: submit order response missing order id")
	}
	if responseSymbol := normalizeSymbol(response.Symbol); responseSymbol != "" {
		symbol = responseSymbol
	}
	if economics, econErr := orderEconomics(response); econErr != nil {
		b.client.getLogger().Warn("binance: submit response fill economics unreadable", slog.Any("error", econErr), slog.Int64("order_id", response.OrderID))
	} else if economics.FilledQuantity > 0 {
		avg := 0.0
		if economics.FilledAvgPrice != nil {
			avg = *economics.FilledAvgPrice
		}
		b.client.getLogger().Info("binance: order submitted with fills",
			slog.String("symbol", symbol),
			slog.Int64("order_id", response.OrderID),
			slog.String("status", string(economics.Status)),
			slog.Float64("filled_qty", economics.FilledQuantity),
			slog.Float64("filled_avg_price", avg),
			slog.Int("fills", len(response.Fills)),
		)
	}

	return formatExternalOrderID(symbol, response.OrderID), nil
}

// CancelOrder cancels an existing Binance order by composite external ID.
func (b *Broker) CancelOrder(ctx context.Context, externalID string) error {
	if b == nil || b.client == nil {
		return errors.New("binance: broker client is required")
	}

	symbol, orderID, err := parseExternalOrderID(externalID)
	if err != nil {
		return err
	}

	if _, err := b.client.SignedDelete(ctx, "/api/v3/order", url.Values{
		"symbol":  []string{symbol},
		"orderId": []string{strconv.FormatInt(orderID, 10)},
	}); err != nil {
		return fmt.Errorf("binance: cancel order: %w", err)
	}

	return nil
}

// GetOrderStatus fetches a Binance order by composite external ID and maps its status.
func (b *Broker) GetOrderStatus(ctx context.Context, externalID string) (domain.OrderStatus, error) {
	result, err := b.GetOrderStatusResult(ctx, externalID)
	if err != nil {
		return "", err
	}
	return result.Status, nil
}

// GetOrderStatusResult implements execution.BrokerOrderStatusProvider with the
// filled quantity and average price (cummulativeQuoteQty / executedQty).
func (b *Broker) GetOrderStatusResult(ctx context.Context, externalID string) (execution.BrokerOrderStatus, error) {
	if b == nil || b.client == nil {
		return execution.BrokerOrderStatus{}, errors.New("binance: broker client is required")
	}

	symbol, orderID, err := parseExternalOrderID(externalID)
	if err != nil {
		return execution.BrokerOrderStatus{}, err
	}

	response, err := b.getOrder(ctx, url.Values{
		"symbol":  []string{symbol},
		"orderId": []string{strconv.FormatInt(orderID, 10)},
	})
	if err != nil {
		return execution.BrokerOrderStatus{}, err
	}
	return orderEconomics(response)
}

// GetOrderStatusByClientOrderIDResult implements
// execution.BrokerClientOrderStatusProvider through
// GET /api/v3/order?symbol=&origClientOrderId=. The client order ID must carry
// the symbol as "SYMBOL:client-id" or be passed with a symbol prefix because
// Binance scopes client order IDs per symbol; a bare client ID cannot be
// resolved and returns an error.
func (b *Broker) GetOrderStatusByClientOrderIDResult(ctx context.Context, clientOrderID string) (string, execution.BrokerOrderStatus, error) {
	if b == nil || b.client == nil {
		return "", execution.BrokerOrderStatus{}, errors.New("binance: broker client is required")
	}
	symbol, origClientOrderID, err := splitSymbolClientOrderID(clientOrderID)
	if err != nil {
		return "", execution.BrokerOrderStatus{}, err
	}
	response, err := b.getOrder(ctx, url.Values{
		"symbol":            []string{symbol},
		"origClientOrderId": []string{origClientOrderID},
	})
	if err != nil {
		var provider *ErrorResponse
		if errors.As(err, &provider) && provider.Code == -2013 { // Order does not exist.
			return "", execution.BrokerOrderStatus{}, execution.ErrBrokerOrderNotFound
		}
		return "", execution.BrokerOrderStatus{}, err
	}
	if response.OrderID <= 0 {
		return "", execution.BrokerOrderStatus{}, execution.ErrBrokerOrderNotFound
	}
	status, err := orderEconomics(response)
	if err != nil {
		return "", execution.BrokerOrderStatus{}, err
	}
	if responseSymbol := normalizeSymbol(response.Symbol); responseSymbol != "" {
		symbol = responseSymbol
	}
	return formatExternalOrderID(symbol, response.OrderID), status, nil
}

func (b *Broker) getOrder(ctx context.Context, params url.Values) (orderResponse, error) {
	responseBody, err := b.client.SignedGet(ctx, "/api/v3/order", params)
	if err != nil {
		return orderResponse{}, fmt.Errorf("binance: get order status: %w", err)
	}
	var response orderResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return orderResponse{}, fmt.Errorf("binance: decode order status response: %w", err)
	}
	return response, nil
}

// splitSymbolClientOrderID accepts "SYMBOL:client-id" and returns both parts.
func splitSymbolClientOrderID(raw string) (string, string, error) {
	trimmed := strings.TrimSpace(raw)
	idx := strings.Index(trimmed, orderExternalIDDelim)
	if idx <= 0 || idx == len(trimmed)-1 {
		return "", "", fmt.Errorf("binance: client order id %q must be in SYMBOL:CLIENT_ID format", raw)
	}
	symbol := normalizeSymbol(trimmed[:idx])
	clientID := strings.TrimSpace(trimmed[idx+1:])
	if symbol == "" || clientID == "" {
		return "", "", fmt.Errorf("binance: client order id %q must be in SYMBOL:CLIENT_ID format", raw)
	}
	return symbol, clientID, nil
}

// orderEconomics maps status plus executedQty/cummulativeQuoteQty (and fills
// when present) onto BrokerOrderStatus. Average price is
// cummulativeQuoteQty / executedQty; fills are used only when the cumulative
// fields are absent.
func orderEconomics(response orderResponse) (execution.BrokerOrderStatus, error) {
	status, err := mapOrderStatus(response.Status)
	if err != nil {
		return execution.BrokerOrderStatus{}, err
	}
	result := execution.BrokerOrderStatus{Status: status}

	executed, err := parseOptionalFloat("executedQty", response.ExecutedQty)
	if err != nil {
		return execution.BrokerOrderStatus{}, err
	}
	quote, err := parseOptionalFloat("cummulativeQuoteQty", response.CummulativeQuoteQty)
	if err != nil {
		return execution.BrokerOrderStatus{}, err
	}
	if executed == 0 && len(response.Fills) > 0 {
		for _, fill := range response.Fills {
			qty, err := parseOptionalFloat("fill qty", fill.Qty)
			if err != nil {
				return execution.BrokerOrderStatus{}, err
			}
			price, err := parseOptionalFloat("fill price", fill.Price)
			if err != nil {
				return execution.BrokerOrderStatus{}, err
			}
			executed += qty
			quote += qty * price
		}
	}
	result.FilledQuantity = executed
	if executed > 0 {
		if quote > 0 {
			avg := quote / executed
			result.FilledAvgPrice = &avg
		}
		if ts := response.UpdateTime; ts > 0 {
			at := time.UnixMilli(ts).UTC()
			result.FilledAt = &at
		} else if ts := response.TransactTime; ts > 0 {
			at := time.UnixMilli(ts).UTC()
			result.FilledAt = &at
		}
		if status == domain.OrderStatusSubmitted || status == domain.OrderStatusPending {
			result.Status = domain.OrderStatusPartial
		}
	}
	return result, nil
}

// GetPositions returns current Binance spot balances mapped to long positions.
// Quote/stable assets (USDT, USDC, BUSD, FDUSD, TUSD, USD) are excluded because
// they are cash, not positions. Spot balances carry no cost basis, so
// AvgEntry is 0 (unknown); callers must not treat it as a real entry price.
func (b *Broker) GetPositions(ctx context.Context) ([]domain.Position, error) {
	if b == nil || b.client == nil {
		return nil, errors.New("binance: broker client is required")
	}

	account, err := b.getAccount(ctx)
	if err != nil {
		return nil, err
	}

	positions := make([]domain.Position, 0, len(account.Balances))
	for _, balance := range account.Balances {
		position, ok, err := mapPosition(balance)
		if err != nil {
			return nil, err
		}
		if ok {
			positions = append(positions, position)
		}
	}

	return positions, nil
}

// GetAccountBalance returns the preferred cash-equivalent balance from a Binance spot account.
func (b *Broker) GetAccountBalance(ctx context.Context) (execution.Balance, error) {
	if b == nil || b.client == nil {
		return execution.Balance{}, errors.New("binance: broker client is required")
	}

	account, err := b.getAccount(ctx)
	if err != nil {
		return execution.Balance{}, err
	}

	selectedBalance, err := selectAccountBalance(account.Balances)
	if err != nil {
		return execution.Balance{}, err
	}

	free, locked, err := parseBalanceAmounts(selectedBalance)
	if err != nil {
		return execution.Balance{}, err
	}

	return execution.Balance{
		Currency:    normalizeAsset(selectedBalance.Asset),
		Cash:        free,
		BuyingPower: free,
		Equity:      free + locked,
	}, nil
}

func (b *Broker) getAccount(ctx context.Context) (accountResponse, error) {
	responseBody, err := b.client.SignedGet(ctx, "/api/v3/account", nil)
	if err != nil {
		return accountResponse{}, fmt.Errorf("binance: get account: %w", err)
	}

	var response accountResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return accountResponse{}, fmt.Errorf("binance: decode account response: %w", err)
	}

	return response, nil
}

func mapSubmitOrderParams(order *domain.Order) (url.Values, string, error) {
	symbol := normalizeSymbol(order.Ticker)
	if symbol == "" {
		return nil, "", errors.New("binance: order ticker is required")
	}

	rawSide := strings.TrimSpace(order.Side.String())
	if rawSide == "" {
		return nil, "", errors.New("binance: order side is required")
	}

	side := strings.ToUpper(rawSide)
	switch domain.OrderSide(strings.ToLower(side)) {
	case domain.OrderSideBuy, domain.OrderSideSell:
	default:
		return nil, "", fmt.Errorf("binance: unsupported order side %q", order.Side)
	}

	if order.Quantity <= 0 {
		return nil, "", errors.New("binance: order quantity must be greater than zero")
	}

	params := url.Values{
		"symbol":   []string{symbol},
		"side":     []string{side},
		"quantity": []string{formatFloat(order.Quantity)},
	}

	switch order.OrderType {
	case domain.OrderTypeMarket:
		params.Set("type", "MARKET")
	case domain.OrderTypeLimit:
		if order.LimitPrice == nil {
			return nil, "", errors.New("binance: limit order requires limit price")
		}
		if *order.LimitPrice <= 0 {
			return nil, "", errors.New("binance: limit price must be greater than zero")
		}

		params.Set("type", "LIMIT")
		params.Set("price", formatFloat(*order.LimitPrice))
		params.Set("timeInForce", defaultTimeInForce)
	default:
		return nil, "", fmt.Errorf("binance: unsupported order type %q", order.OrderType)
	}

	return params, symbol, nil
}

func formatExternalOrderID(symbol string, orderID int64) string {
	return symbol + orderExternalIDDelim + strconv.FormatInt(orderID, 10)
}

func parseExternalOrderID(externalID string) (string, int64, error) {
	trimmedID := strings.TrimSpace(externalID)
	if trimmedID == "" {
		return "", 0, errors.New("binance: external order id is required")
	}

	parts := strings.Split(trimmedID, orderExternalIDDelim)
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("binance: external order id %q must be in SYMBOL:ORDER_ID format", externalID)
	}

	symbol := normalizeSymbol(parts[0])
	if symbol == "" {
		return "", 0, fmt.Errorf("binance: external order id %q must include a symbol", externalID)
	}

	orderID, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil || orderID <= 0 {
		return "", 0, fmt.Errorf("binance: external order id %q must include a valid numeric order id", externalID)
	}

	return symbol, orderID, nil
}

func mapOrderStatus(rawStatus string) (domain.OrderStatus, error) {
	switch status := strings.ToUpper(strings.TrimSpace(rawStatus)); status {
	case "":
		return "", errors.New("binance: order status is required")
	case "PENDING_NEW", "PENDING_CANCEL":
		return domain.OrderStatusPending, nil
	case "NEW":
		return domain.OrderStatusSubmitted, nil
	case "PARTIALLY_FILLED":
		return domain.OrderStatusPartial, nil
	case "FILLED":
		return domain.OrderStatusFilled, nil
	case "CANCELED", "EXPIRED", "EXPIRED_IN_MATCH":
		return domain.OrderStatusCancelled, nil
	case "REJECTED":
		return domain.OrderStatusRejected, nil
	default:
		return "", fmt.Errorf("binance: unsupported order status %q", rawStatus)
	}
}

func mapPosition(balance accountBalance) (domain.Position, bool, error) {
	asset := normalizeAsset(balance.Asset)
	if asset == "" {
		return domain.Position{}, false, errors.New("binance: balance asset is required")
	}

	free, locked, err := parseBalanceAmounts(balance)
	if err != nil {
		return domain.Position{}, false, err
	}

	quantity := free + locked
	if quantity == 0 {
		return domain.Position{}, false, nil
	}
	if _, isQuote := quoteAssets[asset]; isQuote {
		return domain.Position{}, false, nil
	}

	return domain.Position{
		Ticker:   asset,
		Side:     domain.PositionSideLong,
		Quantity: quantity,
		AvgEntry: 0, // unknown: Binance spot balances carry no cost basis
	}, true, nil
}

func selectAccountBalance(balances []accountBalance) (accountBalance, error) {
	if len(balances) == 0 {
		return accountBalance{}, errors.New("binance: account balances are required")
	}

	normalized := make([]accountBalance, 0, len(balances))
	for _, balance := range balances {
		asset := normalizeAsset(balance.Asset)
		if asset == "" {
			return accountBalance{}, errors.New("binance: balance asset is required")
		}
		normalized = append(normalized, balance)
	}

	for _, asset := range preferredBalanceAssets {
		for _, balance := range normalized {
			if normalizeAsset(balance.Asset) == asset {
				free, locked, err := parseBalanceAmounts(balance)
				if err != nil {
					return accountBalance{}, err
				}
				if free+locked > 0 {
					return balance, nil
				}
			}
		}
	}

	for _, balance := range normalized {
		free, locked, err := parseBalanceAmounts(balance)
		if err != nil {
			return accountBalance{}, err
		}
		if free+locked > 0 {
			return balance, nil
		}
	}

	return normalized[0], nil
}

func parseBalanceAmounts(balance accountBalance) (float64, float64, error) {
	free, err := parseRequiredFloat("free", balance.Free)
	if err != nil {
		return 0, 0, err
	}
	locked, err := parseRequiredFloat("locked", balance.Locked)
	if err != nil {
		return 0, 0, err
	}

	return free, locked, nil
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func parseRequiredFloat(fieldName, value string) (float64, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return 0, fmt.Errorf("binance: %s is required", fieldName)
	}

	parsedValue, err := strconv.ParseFloat(trimmedValue, 64)
	if err != nil {
		return 0, fmt.Errorf("binance: parse %s: %w", fieldName, err)
	}

	return parsedValue, nil
}

func normalizeSymbol(symbol string) string {
	return strings.ToUpper(strings.TrimSpace(symbol))
}

func normalizeAsset(asset string) string {
	return strings.ToUpper(strings.TrimSpace(asset))
}
