package alpaca

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// StockQuoteEvidence preserves top-of-book provider facts and original bytes.
// It does not assert consolidated coverage, market status, or execution venue.
// BidSize and AskSize retain provider values. ShareSizes applies the dated REST
// unit contract; execution lot size is a separate instrument/venue fact.
type StockQuoteEvidence struct {
	Ticker, Feed, RequestPath, ResponseSHA256 string
	BidExchange, AskExchange                  string
	Bid, Ask, BidSize, AskSize                decimal.Decimal
	ExchangeAt, ObservedAt                    time.Time
	RawResponse                               []byte
}

// ShareSizes returns REST quote sizes in shares after the provider's November
// 3, 2025 unit change. The cutover day is rejected conservatively because its
// timezone boundary is not specified. Older round-lot data needs dated reference
// conversion and must never be multiplied by a guessed or current round lot.
// Source: https://docs.alpaca.markets/us/reference/stocklatestquotesingle-1.md
func (e *StockQuoteEvidence) ShareSizes() (decimal.Decimal, decimal.Decimal, error) {
	if e == nil || e.ExchangeAt.Before(time.Date(2025, 11, 4, 0, 0, 0, 0, time.UTC)) {
		return decimal.Zero, decimal.Zero, fmt.Errorf("alpaca: historical quote size needs dated unit conversion")
	}
	for _, size := range []decimal.Decimal{e.BidSize, e.AskSize} {
		if !size.IsPositive() || !size.Equal(size.Truncate(0)) || size.GreaterThan(decimal.NewFromInt(4294967295)) {
			return decimal.Zero, decimal.Zero, fmt.Errorf("alpaca: quote size is not a positive uint32 share quantity")
		}
	}
	return e.BidSize, e.AskSize, nil
}

// StockQuoteProvider fetches explicitly selected feeds without a broker binding.
type StockQuoteProvider struct {
	apiKey, apiSecret, baseURL string
	client                     *http.Client
}

// NewStockQuoteProvider constructs a read-only market-data client.
func NewStockQuoteProvider(apiKey, apiSecret string) *StockQuoteProvider {
	return &StockQuoteProvider{apiKey: strings.TrimSpace(apiKey), apiSecret: strings.TrimSpace(apiSecret), baseURL: alpacaDataBaseURL, client: &http.Client{Timeout: defaultTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// LatestQuote retains one response from the caller-selected feed. No default,
// entitlement fallback, retry, or market/session inference is performed.
// Contract: https://docs.alpaca.markets/us/reference/stocklatestquotesingle-1
func (p *StockQuoteProvider) LatestQuote(ctx context.Context, ticker, feed string) (*StockQuoteEvidence, error) {
	if p == nil || p.client == nil || p.apiKey == "" || p.apiSecret == "" || ticker == "" || ticker != strings.TrimSpace(ticker) || strings.ContainsAny(ticker, "/?&#\\") {
		return nil, fmt.Errorf("alpaca: stock quote requires credentials and an exact ticker")
	}
	switch feed {
	case "iex", "sip", "delayed_sip", "boats", "overnight", "otc":
	default:
		return nil, fmt.Errorf("alpaca: stock quote requires an explicit supported feed")
	}
	path := "/v2/stocks/" + url.PathEscape(ticker) + "/quotes/latest?" + url.Values{"feed": {feed}, "currency": {"USD"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("alpaca: create stock quote request")
	}
	req.Header.Set("APCA-API-KEY-ID", p.apiKey)
	req.Header.Set("APCA-API-SECRET-KEY", p.apiSecret)
	response, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("alpaca: stock quote transport failed")
	}
	// ReadAll reports content errors; closing cannot change the retained bytes.
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("alpaca: stock quote HTTP %d", response.StatusCode)
	}
	const maxResponse = 1 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil || len(body) > maxResponse {
		return nil, fmt.Errorf("alpaca: stock quote response unreadable or oversized")
	}
	observed := time.Now().UTC()
	var payload struct {
		Symbol string `json:"symbol"`
		Quote  struct {
			Bid         json.Number `json:"bp"`
			Ask         json.Number `json:"ap"`
			BidSize     json.Number `json:"bs"`
			AskSize     json.Number `json:"as"`
			BidExchange string      `json:"bx"`
			AskExchange string      `json:"ax"`
			Timestamp   time.Time   `json:"t"`
		} `json:"quote"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Symbol != ticker || payload.Quote.Timestamp.IsZero() || payload.Quote.Timestamp.After(observed) || payload.Quote.BidExchange == "" || payload.Quote.AskExchange == "" {
		return nil, fmt.Errorf("alpaca: stock quote lacks matching timed source facts")
	}
	values := []json.Number{payload.Quote.Bid, payload.Quote.Ask, payload.Quote.BidSize, payload.Quote.AskSize}
	parsed := make([]decimal.Decimal, len(values))
	for i, value := range values {
		parsed[i], err = decimal.NewFromString(value.String())
		if err != nil || !parsed[i].IsPositive() {
			return nil, fmt.Errorf("alpaca: stock quote requires positive prices and sizes")
		}
	}
	if parsed[0].GreaterThan(parsed[1]) {
		return nil, fmt.Errorf("alpaca: crossed stock quote")
	}
	digest := sha256.Sum256(body)
	return &StockQuoteEvidence{Ticker: ticker, Feed: feed, RequestPath: path, ResponseSHA256: hex.EncodeToString(digest[:]), RawResponse: append([]byte(nil), body...), Bid: parsed[0], Ask: parsed[1], BidSize: parsed[2], AskSize: parsed[3], BidExchange: payload.Quote.BidExchange, AskExchange: payload.Quote.AskExchange, ExchangeAt: payload.Quote.Timestamp.UTC(), ObservedAt: observed}, nil
}
