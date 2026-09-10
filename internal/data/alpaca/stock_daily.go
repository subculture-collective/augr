package alpaca

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"time"

	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

// StockDailySource identifies this reader's feed and adjustment basis. Its data
// must never be cached as stock-chain or spliced into another provider's series.
const StockDailySource = "alpaca-sip-split-daily-v1"

var stockDailySymbol = regexp.MustCompile(`^[A-Z][A-Z0-9.\-]{0,14}$`)

// StockDailyProvider reads bounded historical SIP daily bars with split adjustment.
// It has no execution methods and does not implement the generic provider chain.
type StockDailyProvider struct {
	transport *OptionsDataProvider
	now       func() time.Time
	cache     repository.MarketDataCacheRepository
}

// NewCachedStockDailyProvider retains whole source responses under a dedicated
// feed/adjustment cache identity. It never reads or updates generic stock history.
func NewCachedStockDailyProvider(apiKey, apiSecret string, cache repository.MarketDataCacheRepository) *StockDailyProvider {
	p := NewStockDailyProvider(apiKey, apiSecret)
	p.cache = cache
	return p
}

// NewStockDailyProvider creates a market-data-only reader with explicit credentials.
func NewStockDailyProvider(apiKey, apiSecret string) *StockDailyProvider {
	return &StockDailyProvider{transport: NewOptionsDataProvider(apiKey, apiSecret, nil), now: time.Now}
}

// GetDailyBars reads a complete, single-source [from,to) interval of up to 45 days.
// Bounds are UTC session-date midnights. Today's session is available only after
// 16:15 New York time, with the actual SIP request capped at the completed close.
// This deliberately excludes real-time SIP requests and provisional sessions.
func (p *StockDailyProvider) GetDailyBars(ctx context.Context, symbol string, from, to time.Time) (data.ExactHistoricalResult, error) {
	var empty data.ExactHistoricalResult
	if p == nil || p.transport == nil || p.transport.apiKey == "" || p.transport.apiSecret == "" || p.now == nil {
		return empty, fmt.Errorf("alpaca/daily: incomplete provider")
	}
	from, to = from.UTC(), to.UTC()
	if !stockDailySymbol.MatchString(symbol) || !from.Equal(from.Truncate(24*time.Hour)) || !to.Equal(to.Truncate(24*time.Hour)) || !from.Before(to) || from.Before(time.Unix(0, 0)) || to.Sub(from) > 45*24*time.Hour {
		return empty, fmt.Errorf("alpaca/daily: invalid symbol or historical day interval")
	}
	queryEnd := to
	if to.After(p.now().UTC().Truncate(24 * time.Hour)) {
		location, err := time.LoadLocation("America/New_York")
		if err != nil {
			return empty, fmt.Errorf("alpaca/daily: market timezone unavailable: %w", err)
		}
		local := p.now().In(location)
		closeTime := time.Date(local.Year(), local.Month(), local.Day(), 16, 0, 0, 0, location)
		nextDate := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, time.UTC)
		if !to.Equal(nextDate) || !scheduler.IsNYSETradingDay(local) || p.now().Before(closeTime.Add(15*time.Minute)) {
			return empty, fmt.Errorf("alpaca/daily: completed delayed session required")
		}
		queryEnd = closeTime.UTC()
	}
	path := "/v2/stocks/" + symbol + "/bars"
	query := url.Values{"timeframe": {"1Day"}, "start": {from.Format(time.RFC3339)}, "end": {queryEnd.Format(time.RFC3339)}, "adjustment": {"split"}, "feed": {"sip"}, "sort": {"asc"}, "limit": {"1000"}}
	key := repository.MarketDataCacheKey{Ticker: symbol, Provider: StockDailySource, DataType: "stock_daily_source", Timeframe: "1d", DateFrom: &from, DateTo: &to}
	if p.cache != nil {
		entry, err := p.cache.Get(ctx, key)
		if err == nil && entry != nil && entry.Ticker == symbol && entry.Provider == key.Provider && entry.DataType == key.DataType && entry.Timeframe == key.Timeframe && entry.DateFrom != nil && entry.DateTo != nil && entry.DateFrom.Equal(from) && entry.DateTo.Equal(to) && entry.ExpiresAt.After(p.now()) && !entry.FetchedAt.After(p.now()) {
			if result, err := decodeStockDailyResponse(entry.Data, symbol, from, to, path, query); err == nil {
				// Retained source pages remain available, but this call made no
				// provider request and must earn no acquisition/freshness credit.
				result.Receipt.Pages = 0
				return result, nil
			}
		}
	}
	body, err := p.transport.getExactOptionPage(ctx, path, query)
	if err != nil {
		return empty, err
	}
	if bytes.Contains(body, []byte(p.transport.apiKey)) || bytes.Contains(body, []byte(p.transport.apiSecret)) {
		return empty, fmt.Errorf("alpaca/daily: credential echo rejected")
	}
	result, err := decodeStockDailyResponse(body, symbol, from, to, path, query)
	if err != nil {
		return empty, err
	}
	if p.cache != nil {
		now := p.now().UTC()
		entry := &domain.MarketData{Ticker: symbol, Provider: key.Provider, DataType: key.DataType, Timeframe: key.Timeframe, DateFrom: &from, DateTo: &to, Data: bytes.Clone(body), FetchedAt: now, ExpiresAt: now.Add(30 * time.Minute)}
		if err := p.cache.Set(ctx, entry); err != nil {
			return empty, fmt.Errorf("alpaca/daily: retain source cache: %w", err)
		}
	}
	return result, nil
}

func decodeStockDailyResponse(body []byte, symbol string, from, to time.Time, path string, query url.Values) (data.ExactHistoricalResult, error) {
	var empty data.ExactHistoricalResult
	fields, err := exactOptionFields(body, 65536)
	if err != nil {
		return empty, err
	}
	var responseSymbol string
	if json.Unmarshal(fields["symbol"], &responseSymbol) != nil || responseSymbol != symbol || !bytes.Equal(bytes.TrimSpace(fields["next_page_token"]), []byte("null")) {
		return empty, fmt.Errorf("alpaca/daily: symbol mismatch or incomplete page")
	}
	var rows []json.RawMessage
	if json.Unmarshal(fields["bars"], &rows) != nil || len(rows) == 0 || len(rows) > 45 {
		return empty, fmt.Errorf("alpaca/daily: bounded nonempty bars required")
	}
	result := data.ExactHistoricalResult{}
	marketLocation, err := time.LoadLocation("America/New_York")
	if err != nil {
		return empty, fmt.Errorf("alpaca/daily: market timezone unavailable: %w", err)
	}
	sessions := make(map[string]bool)
	for i, raw := range rows {
		bar, err := decodeExactOptionBar(raw)
		if err != nil {
			return empty, err
		}
		if bar.Timestamp.Before(from) || !bar.Timestamp.Before(to) || (i > 0 && !bar.Timestamp.After(result.Bars[i-1].Timestamp)) {
			return empty, fmt.Errorf("alpaca/daily: unordered or out-of-interval bar")
		}
		local := bar.Timestamp.In(marketLocation)
		if local.Hour() != 0 || local.Minute() != 0 || local.Second() != 0 || local.Nanosecond() != 0 || !scheduler.IsNYSETradingDay(local) {
			return empty, fmt.Errorf("alpaca/daily: regular session midnight required")
		}
		day := local.Format("2006-01-02")
		if sessions[day] {
			return empty, fmt.Errorf("alpaca/daily: duplicate session")
		}
		sessions[day] = true
		if err := validateStockDailyPrices(bar); err != nil {
			return empty, err
		}
		bar.PageIndex, bar.RowIndex = 0, i
		result.Bars = append(result.Bars, bar)
	}
	for day := from; day.Before(to); day = day.AddDate(0, 0, 1) {
		y, m, d := day.Date()
		local := time.Date(y, m, d, 0, 0, 0, 0, marketLocation)
		if scheduler.IsNYSETradingDay(local) && !sessions[day.Format("2006-01-02")] {
			return empty, fmt.Errorf("alpaca/daily: missing completed session %s", day.Format("2006-01-02"))
		}
	}
	result.Pages = []data.HistoricalSourcePage{{RequestPath: path, Query: query.Encode(), Body: bytes.Clone(body)}}
	result.Receipt = data.HistoricalFetchReceipt{Provider: "alpaca", Feed: "sip", AdjustmentPolicy: "split", Pages: 1, Entitled: true, PaginationComplete: true}
	return result, nil
}

func validateStockDailyPrices(bar data.ExactHistoricalBar) error {
	values := make([]decimal.Decimal, 0, 4)
	for _, value := range []string{bar.Open, bar.High, bar.Low, bar.Close} {
		parsed, err := decimal.NewFromString(value)
		if err != nil || !parsed.IsPositive() {
			return fmt.Errorf("alpaca/daily: positive OHLC required")
		}
		values = append(values, parsed)
	}
	open, high, low, closePrice := values[0], values[1], values[2], values[3]
	if high.LessThan(low) || open.LessThan(low) || open.GreaterThan(high) || closePrice.LessThan(low) || closePrice.GreaterThan(high) {
		return fmt.Errorf("alpaca/daily: inconsistent OHLC bounds")
	}
	return nil
}
