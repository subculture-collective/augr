package binance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// symbolFiltersTTL bounds how long cached /api/v3/exchangeInfo filters are
// reused before a refresh.
const symbolFiltersTTL = time.Hour

// symbolFilters is the subset of Binance exchangeInfo filters needed to shape
// an order before submission.
type symbolFilters struct {
	Symbol      string
	StepSize    float64 // LOT_SIZE.stepSize
	MinQty      float64 // LOT_SIZE.minQty
	MaxQty      float64 // LOT_SIZE.maxQty (0 = unbounded)
	TickSize    float64 // PRICE_FILTER.tickSize
	MinNotional float64 // MIN_NOTIONAL.minNotional or NOTIONAL.minNotional
	// NotionalAppliesToMarket mirrors MIN_NOTIONAL.applyToMarket /
	// NOTIONAL.applyMinToMarket. Market orders carry no price here, so the
	// notional check is skipped for them regardless; the flag is kept for
	// callers that supply a reference price.
	NotionalAppliesToMarket bool
	FetchedAt               time.Time
}

type cachedSymbolFilters struct {
	filters symbolFilters
	expires time.Time
}

// symbolFilterCache caches per-symbol exchangeInfo filters with a TTL.
type symbolFilterCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	entries map[string]cachedSymbolFilters
}

func newSymbolFilterCache(ttl time.Duration, now func() time.Time) *symbolFilterCache {
	if ttl <= 0 {
		ttl = symbolFiltersTTL
	}
	if now == nil {
		now = time.Now
	}
	return &symbolFilterCache{ttl: ttl, now: now, entries: map[string]cachedSymbolFilters{}}
}

func (c *symbolFilterCache) get(symbol string) (symbolFilters, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[symbol]
	if !ok || !c.now().Before(entry.expires) {
		return symbolFilters{}, false
	}
	return entry.filters, true
}

func (c *symbolFilterCache) put(filters symbolFilters) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[filters.Symbol] = cachedSymbolFilters{filters: filters, expires: c.now().Add(c.ttl)}
}

type exchangeInfoResponse struct {
	Symbols []struct {
		Symbol  string            `json:"symbol"`
		Status  string            `json:"status"`
		Filters []json.RawMessage `json:"filters"`
	} `json:"symbols"`
}

// symbolFiltersFor returns cached filters or fetches
// GET /api/v3/exchangeInfo?symbol=<symbol> and caches the result.
func (b *Broker) symbolFiltersFor(ctx context.Context, symbol string) (symbolFilters, error) {
	if b == nil || b.client == nil {
		return symbolFilters{}, errors.New("binance: broker client is required")
	}
	symbol = normalizeSymbol(symbol)
	if symbol == "" {
		return symbolFilters{}, errors.New("binance: symbol is required")
	}
	if cached, ok := b.filters.get(symbol); ok {
		return cached, nil
	}
	body, err := b.client.Get(ctx, "/api/v3/exchangeInfo", url.Values{"symbol": []string{symbol}})
	if err != nil {
		return symbolFilters{}, fmt.Errorf("binance: exchange info: %w", err)
	}
	filters, err := parseExchangeInfo(body, symbol, b.client.nowFunc()())
	if err != nil {
		return symbolFilters{}, err
	}
	b.filters.put(filters)
	return filters, nil
}

func parseExchangeInfo(body []byte, symbol string, now time.Time) (symbolFilters, error) {
	var response exchangeInfoResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return symbolFilters{}, fmt.Errorf("binance: decode exchange info: %w", err)
	}
	for _, entry := range response.Symbols {
		if normalizeSymbol(entry.Symbol) != symbol {
			continue
		}
		filters := symbolFilters{Symbol: symbol, FetchedAt: now}
		for _, raw := range entry.Filters {
			var head struct {
				FilterType string `json:"filterType"`
			}
			if err := json.Unmarshal(raw, &head); err != nil {
				return symbolFilters{}, fmt.Errorf("binance: decode exchange info filter: %w", err)
			}
			switch strings.ToUpper(head.FilterType) {
			case "LOT_SIZE":
				var f struct {
					StepSize string `json:"stepSize"`
					MinQty   string `json:"minQty"`
					MaxQty   string `json:"maxQty"`
				}
				if err := json.Unmarshal(raw, &f); err != nil {
					return symbolFilters{}, fmt.Errorf("binance: decode LOT_SIZE: %w", err)
				}
				var err error
				if filters.StepSize, err = parseOptionalFloat("stepSize", f.StepSize); err != nil {
					return symbolFilters{}, err
				}
				if filters.MinQty, err = parseOptionalFloat("minQty", f.MinQty); err != nil {
					return symbolFilters{}, err
				}
				if filters.MaxQty, err = parseOptionalFloat("maxQty", f.MaxQty); err != nil {
					return symbolFilters{}, err
				}
			case "PRICE_FILTER":
				var f struct {
					TickSize string `json:"tickSize"`
				}
				if err := json.Unmarshal(raw, &f); err != nil {
					return symbolFilters{}, fmt.Errorf("binance: decode PRICE_FILTER: %w", err)
				}
				var err error
				if filters.TickSize, err = parseOptionalFloat("tickSize", f.TickSize); err != nil {
					return symbolFilters{}, err
				}
			case "MIN_NOTIONAL", "NOTIONAL":
				var f struct {
					MinNotional      string `json:"minNotional"`
					ApplyToMarket    bool   `json:"applyToMarket"`
					ApplyMinToMarket bool   `json:"applyMinToMarket"`
				}
				if err := json.Unmarshal(raw, &f); err != nil {
					return symbolFilters{}, fmt.Errorf("binance: decode %s: %w", head.FilterType, err)
				}
				minNotional, err := parseOptionalFloat("minNotional", f.MinNotional)
				if err != nil {
					return symbolFilters{}, err
				}
				if minNotional > filters.MinNotional {
					filters.MinNotional = minNotional
				}
				filters.NotionalAppliesToMarket = filters.NotionalAppliesToMarket || f.ApplyToMarket || f.ApplyMinToMarket
			}
		}
		return filters, nil
	}
	return symbolFilters{}, fmt.Errorf("binance: exchange info does not include symbol %q", symbol)
}

func parseOptionalFloat(field, raw string) (float64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	value, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, fmt.Errorf("binance: parse %s: %w", field, err)
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, fmt.Errorf("binance: %s out of range: %s", field, trimmed)
	}
	return value, nil
}

// shapedOrder is an order quantity/price after exchange filters are applied.
type shapedOrder struct {
	Quantity string
	Price    string // empty for market orders
}

// applySymbolFilters rounds quantity down to stepSize and the limit price to
// tickSize (down for buys, up for sells, so the order never becomes more
// aggressive than requested) and rejects orders below minQty or minNotional.
func applySymbolFilters(order *domain.Order, filters symbolFilters) (shapedOrder, error) {
	quantity := order.Quantity
	if filters.StepSize > 0 {
		quantity = floorToIncrement(quantity, filters.StepSize)
	}
	if quantity <= 0 {
		return shapedOrder{}, fmt.Errorf("binance: quantity %s rounds to zero at step size %s", formatFloat(order.Quantity), formatFloat(filters.StepSize))
	}
	if filters.MinQty > 0 && quantity < filters.MinQty {
		return shapedOrder{}, fmt.Errorf("binance: quantity %s below minimum %s for %s", formatIncrement(quantity, filters.StepSize), formatFloat(filters.MinQty), filters.Symbol)
	}
	if filters.MaxQty > 0 && quantity > filters.MaxQty {
		return shapedOrder{}, fmt.Errorf("binance: quantity %s above maximum %s for %s", formatIncrement(quantity, filters.StepSize), formatFloat(filters.MaxQty), filters.Symbol)
	}
	shaped := shapedOrder{Quantity: formatIncrement(quantity, filters.StepSize)}

	if order.OrderType != domain.OrderTypeLimit {
		return shaped, nil
	}
	if order.LimitPrice == nil || *order.LimitPrice <= 0 {
		return shapedOrder{}, errors.New("binance: limit order requires limit price")
	}
	price := *order.LimitPrice
	if filters.TickSize > 0 {
		if order.Side == domain.OrderSideSell {
			price = ceilToIncrement(price, filters.TickSize)
		} else {
			price = floorToIncrement(price, filters.TickSize)
		}
	}
	if price <= 0 {
		return shapedOrder{}, fmt.Errorf("binance: price %s rounds to zero at tick size %s", formatFloat(*order.LimitPrice), formatFloat(filters.TickSize))
	}
	if filters.MinNotional > 0 && quantity*price < filters.MinNotional {
		return shapedOrder{}, fmt.Errorf("binance: notional %s below minimum %s for %s", formatFloat(quantity*price), formatFloat(filters.MinNotional), filters.Symbol)
	}
	shaped.Price = formatIncrement(price, filters.TickSize)
	return shaped, nil
}

func floorToIncrement(value, increment float64) float64 {
	if increment <= 0 {
		return value
	}
	steps := math.Floor(value/increment + 1e-9)
	return roundToDecimals(steps*increment, incrementDecimals(increment))
}

func ceilToIncrement(value, increment float64) float64 {
	if increment <= 0 {
		return value
	}
	steps := math.Ceil(value/increment - 1e-9)
	return roundToDecimals(steps*increment, incrementDecimals(increment))
}

func roundToDecimals(value float64, decimals int) float64 {
	pow := math.Pow(10, float64(decimals))
	return math.Round(value*pow) / pow
}

// incrementDecimals returns the number of decimal places implied by an
// increment such as 0.001 (3) or 1 (0).
func incrementDecimals(increment float64) int {
	if increment <= 0 {
		return 8
	}
	text := strconv.FormatFloat(increment, 'f', -1, 64)
	if idx := strings.IndexByte(text, '.'); idx >= 0 {
		return len(text) - idx - 1
	}
	return 0
}

// formatIncrement renders value with the increment's precision and trims
// trailing zeros ("1.50" -> "1.5"); Binance accepts either form.
func formatIncrement(value, increment float64) string {
	if increment <= 0 {
		return formatFloat(value)
	}
	text := strconv.FormatFloat(value, 'f', incrementDecimals(increment), 64)
	if strings.IndexByte(text, '.') >= 0 {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	return text
}
