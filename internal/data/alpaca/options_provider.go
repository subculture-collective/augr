package alpaca

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

const (
	alpacaDataBaseURL = "https://data.alpaca.markets"
	defaultTimeout    = 30 * time.Second
)

// OptionsDataProvider fetches options data from Alpaca's market data API.
type OptionsDataProvider struct {
	apiKey    string
	apiSecret string
	baseURL   string
	client    *http.Client
	logger    *slog.Logger
}

var _ data.OptionsDataProvider = (*OptionsDataProvider)(nil)

// NewOptionsDataProvider constructs an Alpaca options data provider.
// If logger is nil, slog.Default() is used.
func NewOptionsDataProvider(apiKey, apiSecret string, logger *slog.Logger) *OptionsDataProvider {
	if logger == nil {
		logger = slog.Default()
	}

	return &OptionsDataProvider{
		apiKey:    strings.TrimSpace(apiKey),
		apiSecret: strings.TrimSpace(apiSecret),
		baseURL:   alpacaDataBaseURL,
		client: &http.Client{
			Timeout: defaultTimeout,
		},
		logger: logger,
	}
}

// SetBaseURL overrides the configured base URL. This is primarily useful for testing.
func (p *OptionsDataProvider) SetBaseURL(baseURL string) {
	if p != nil {
		p.baseURL = baseURL
	}
}

// SetHTTPClient replaces the underlying HTTP client. This is primarily useful for testing.
func (p *OptionsDataProvider) SetHTTPClient(client *http.Client) {
	if p != nil && client != nil {
		p.client = client
	}
}

// ------------------------------------------------------------------
// Options chain snapshots
// ------------------------------------------------------------------

// snapshotsResponse is the top-level response from the Alpaca options snapshots endpoint.
type snapshotsResponse struct {
	Snapshots     map[string]optionSnapshot `json:"snapshots"`
	NextPageToken string                    `json:"next_page_token"`
}

type optionSnapshot struct {
	LatestTrade       *optionTrade  `json:"latestTrade"`
	LatestQuote       *optionQuote  `json:"latestQuote"`
	ImpliedVolatility *float64      `json:"impliedVolatility"`
	Greeks            *optionGreeks `json:"greeks"`
}

type optionTrade struct {
	ID       int64   `json:"i"`
	Price    float64 `json:"p"`
	Size     float64 `json:"s"`
	Time     string  `json:"t"`
	Exchange string  `json:"x"`
}

type optionQuote struct {
	AskPrice float64 `json:"ap"`
	AskSize  float64 `json:"as"`
	BidPrice float64 `json:"bp"`
	BidSize  float64 `json:"bs"`
	Time     string  `json:"t"`
}

type optionGreeks struct {
	Delta float64 `json:"delta"`
	Gamma float64 `json:"gamma"`
	Rho   float64 `json:"rho"`
	Theta float64 `json:"theta"`
	Vega  float64 `json:"vega"`
}

// GetOptionsChain returns option snapshots (price + Greeks) for an underlying.
// Snapshots are fetched from Alpaca and then filtered client-side by expiry and optionType.
func (p *OptionsDataProvider) GetOptionsChain(
	ctx context.Context,
	underlying string,
	expiry time.Time,
	optionType domain.OptionType,
) ([]domain.OptionSnapshot, error) {
	snapshots, _, err := p.GetOptionsChainWithReceipt(ctx, underlying, expiry, optionType, "indicative")
	return snapshots, err
}

// GetOptionsChainWithReceipt returns current snapshots and proves the exact
// requested feed plus terminal pagination. It does not claim historical chain
// coverage.
func (p *OptionsDataProvider) GetOptionsChainWithReceipt(
	ctx context.Context,
	underlying string,
	expiry time.Time,
	optionType domain.OptionType,
	feed string,
) ([]domain.OptionSnapshot, data.HistoricalFetchReceipt, error) {
	receipt := data.HistoricalFetchReceipt{Provider: "alpaca", Feed: feed, AdjustmentPolicy: "raw"}
	if p == nil {
		return nil, receipt, fmt.Errorf("alpaca/options: provider is nil")
	}

	underlying = strings.TrimSpace(strings.ToUpper(underlying))
	if underlying == "" {
		return nil, receipt, fmt.Errorf("alpaca/options: underlying ticker is required")
	}
	feed = strings.ToLower(strings.TrimSpace(feed))
	if feed != "indicative" && feed != "opra" {
		return nil, receipt, fmt.Errorf("alpaca/options: unsupported feed %q", feed)
	}
	receipt.Feed = feed

	var allSnapshots []domain.OptionSnapshot
	var pageToken string
	seenPageTokens := make(map[string]struct{})
	seenSymbols := make(map[string]struct{})

	for {
		params := url.Values{}
		params.Set("feed", feed)
		params.Set("limit", "100")
		if pageToken != "" {
			params.Set("page_token", pageToken)
		}

		requestPath := fmt.Sprintf("/v1beta1/options/snapshots/%s", url.PathEscape(underlying))
		body, err := p.doGet(ctx, requestPath, params)
		if err != nil {
			return nil, receipt, fmt.Errorf("alpaca/options: chain request failed: %w", err)
		}
		receipt.Pages++

		var resp snapshotsResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, receipt, fmt.Errorf("alpaca/options: unmarshal chain response: %w", err)
		}

		for occSymbol, snap := range resp.Snapshots {
			parsed, err := domain.ParseOCC(occSymbol)
			if err != nil {
				return nil, receipt, fmt.Errorf("alpaca/options: unparseable OCC symbol %s: %w", occSymbol, err)
			}
			if _, duplicate := seenSymbols[parsed.OCCSymbol]; duplicate {
				return nil, receipt, fmt.Errorf("alpaca/options: duplicate OCC symbol %s across pages", parsed.OCCSymbol)
			}
			seenSymbols[parsed.OCCSymbol] = struct{}{}

			// Client-side filters.
			if !expiry.IsZero() && !parsed.Expiry.Equal(expiry) {
				continue
			}
			if optionType != "" && parsed.OptionType != optionType {
				continue
			}

			ds := domain.OptionSnapshot{
				Contract: *parsed,
			}

			if snap.Greeks != nil {
				ds.Greeks = domain.OptionGreeks{
					Delta: snap.Greeks.Delta,
					Gamma: snap.Greeks.Gamma,
					Theta: snap.Greeks.Theta,
					Vega:  snap.Greeks.Vega,
					Rho:   snap.Greeks.Rho,
				}
			}
			if snap.ImpliedVolatility != nil {
				ds.Greeks.IV = *snap.ImpliedVolatility
			}

			if snap.LatestQuote != nil {
				ds.Bid = snap.LatestQuote.BidPrice
				ds.BidSize = snap.LatestQuote.BidSize
				ds.Ask = snap.LatestQuote.AskPrice
				ds.AskSize = snap.LatestQuote.AskSize
				if observedAt, parseErr := parseAlpacaTime(snap.LatestQuote.Time); parseErr == nil {
					ds.ObservedAt = observedAt
					ds.QuoteObservedAt = observedAt
				}
				if ds.Bid > 0 && ds.Ask > 0 {
					ds.Mid = (ds.Bid + ds.Ask) / 2
				}
			}

			if snap.LatestTrade != nil {
				ds.Last = snap.LatestTrade.Price
				ds.LastSize = snap.LatestTrade.Size
				if observedAt, parseErr := parseAlpacaTime(snap.LatestTrade.Time); parseErr == nil {
					ds.LastTradeObservedAt = observedAt
					if ds.ObservedAt.IsZero() {
						ds.ObservedAt = observedAt
					}
				}
			}

			allSnapshots = append(allSnapshots, ds)
		}

		if resp.NextPageToken == "" {
			receipt.Entitled = true
			receipt.PaginationComplete = true
			break
		}
		if _, duplicate := seenPageTokens[resp.NextPageToken]; duplicate {
			return nil, receipt, fmt.Errorf("alpaca/options: repeated page token")
		}
		seenPageTokens[resp.NextPageToken] = struct{}{}
		pageToken = resp.NextPageToken
	}

	sort.Slice(allSnapshots, func(i, j int) bool {
		return allSnapshots[i].Contract.OCCSymbol < allSnapshots[j].Contract.OCCSymbol
	})
	return allSnapshots, receipt, nil
}

func parseAlpacaTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC().Truncate(time.Microsecond), nil
}

// ------------------------------------------------------------------
// Options OHLCV bars
// ------------------------------------------------------------------

// barsResponse is the top-level response from the Alpaca options bars endpoint.
type barsResponse struct {
	Bars          map[string][]optionBar `json:"bars"`
	NextPageToken string                 `json:"next_page_token"`
}

type tradesResponse struct {
	Trades        map[string][]optionTrade `json:"trades"`
	NextPageToken string                   `json:"next_page_token"`
}

type optionBar struct {
	Timestamp string  `json:"t"`
	Open      float64 `json:"o"`
	High      float64 `json:"h"`
	Low       float64 `json:"l"`
	Close     float64 `json:"c"`
	Volume    float64 `json:"v"`
	Count     float64 `json:"n"`
	VWAP      float64 `json:"vw"`
}

// GetOptionsOHLCV returns historical OHLCV bars for a specific options contract.
func (p *OptionsDataProvider) GetOptionsOHLCV(
	ctx context.Context,
	occSymbol string,
	timeframe data.Timeframe,
	from, to time.Time,
) ([]domain.OHLCV, error) {
	bars, _, err := p.GetOptionsOHLCVWithReceipt(ctx, occSymbol, timeframe, from, to, "indicative", "raw")
	return bars, err
}

// GetOptionsOHLCVWithReceipt returns historical bars together with the exact
// feed and terminal-pagination evidence required by immutable imports.
func (p *OptionsDataProvider) GetOptionsOHLCVWithReceipt(
	ctx context.Context,
	occSymbol string,
	timeframe data.Timeframe,
	from, to time.Time,
	feed, adjustmentPolicy string,
) ([]domain.OHLCV, data.HistoricalFetchReceipt, error) {
	receipt := data.HistoricalFetchReceipt{Provider: "alpaca", Feed: feed, AdjustmentPolicy: adjustmentPolicy}
	if p == nil {
		return nil, receipt, fmt.Errorf("alpaca/options: provider is nil")
	}

	occSymbol = strings.TrimSpace(occSymbol)
	if occSymbol == "" {
		return nil, receipt, fmt.Errorf("alpaca/options: OCC symbol is required")
	}
	feed = strings.ToLower(strings.TrimSpace(feed))
	if feed != "indicative" && feed != "opra" {
		return nil, receipt, fmt.Errorf("alpaca/options: unsupported feed %q", feed)
	}
	receipt.Feed = feed
	if adjustmentPolicy != "raw" {
		return nil, receipt, fmt.Errorf("alpaca/options: unsupported adjustment policy %q", adjustmentPolicy)
	}

	alpacaTF, err := mapTimeframe(timeframe)
	if err != nil {
		return nil, receipt, err
	}

	var allBars []domain.OHLCV
	var pageToken string
	seenPageTokens := make(map[string]struct{})

	for {
		params := url.Values{}
		params.Set("symbols", occSymbol)
		params.Set("timeframe", alpacaTF)
		params.Set("start", from.UTC().Format(time.RFC3339))
		params.Set("end", to.UTC().Format(time.RFC3339))
		params.Set("limit", "1000")
		params.Set("feed", feed)
		if pageToken != "" {
			params.Set("page_token", pageToken)
		}

		body, err := p.doGet(ctx, "/v1beta1/options/bars", params)
		if err != nil {
			return nil, receipt, fmt.Errorf("alpaca/options: ohlcv request failed: %w", err)
		}
		receipt.Pages++

		var resp barsResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, receipt, fmt.Errorf("alpaca/options: unmarshal ohlcv response: %w", err)
		}

		bars, ok := resp.Bars[occSymbol]
		if !ok {
			prefixed := "O:" + strings.TrimPrefix(occSymbol, "O:")
			bars, ok = resp.Bars[prefixed]
		}
		if !ok && len(resp.Bars) != 0 {
			return nil, receipt, fmt.Errorf("alpaca/options: response omitted requested symbol %s", occSymbol)
		}

		for _, bar := range bars {
			ts, parseErr := time.Parse(time.RFC3339, bar.Timestamp)
			if parseErr != nil {
				ts, parseErr = time.Parse(time.RFC3339Nano, bar.Timestamp)
				if parseErr != nil {
					continue
				}
			}

			allBars = append(allBars, domain.OHLCV{
				Timestamp: ts.UTC(),
				Open:      bar.Open,
				High:      bar.High,
				Low:       bar.Low,
				Close:     bar.Close,
				Volume:    bar.Volume,
			})
		}

		if resp.NextPageToken == "" {
			receipt.Entitled = true
			receipt.PaginationComplete = true
			break
		}
		if _, duplicate := seenPageTokens[resp.NextPageToken]; duplicate {
			return nil, receipt, fmt.Errorf("alpaca/options: repeated page token")
		}
		seenPageTokens[resp.NextPageToken] = struct{}{}
		pageToken = resp.NextPageToken
	}

	return allBars, receipt, nil
}

// GetOptionsTradesWithReceipt returns exact historical trades for one OCC
// symbol and is complete only after a terminal provider page.
func (p *OptionsDataProvider) GetOptionsTradesWithReceipt(ctx context.Context, occSymbol string, from, to time.Time, feed string) ([]data.OptionTradeObservation, data.HistoricalFetchReceipt, error) {
	receipt := data.HistoricalFetchReceipt{Provider: "alpaca", Feed: feed, AdjustmentPolicy: "raw"}
	if p == nil {
		return nil, receipt, fmt.Errorf("alpaca/options: provider is nil")
	}
	occSymbol = domain.AlpacaSymbol(strings.TrimSpace(occSymbol))
	if _, err := domain.ParseOCC(occSymbol); err != nil {
		return nil, receipt, fmt.Errorf("alpaca/options: invalid OCC symbol: %w", err)
	}
	if from.After(to) {
		return nil, receipt, fmt.Errorf("alpaca/options: trade range is invalid")
	}
	feed = strings.ToLower(strings.TrimSpace(feed))
	if feed != "indicative" && feed != "opra" {
		return nil, receipt, fmt.Errorf("alpaca/options: unsupported feed %q", feed)
	}
	receipt.Feed = feed
	var values []data.OptionTradeObservation
	var pageToken string
	seenPageTokens := make(map[string]struct{})
	seenTrades := make(map[string]struct{})
	for {
		params := url.Values{}
		params.Set("symbols", occSymbol)
		params.Set("start", from.UTC().Format(time.RFC3339Nano))
		params.Set("end", to.UTC().Format(time.RFC3339Nano))
		params.Set("feed", feed)
		params.Set("limit", "1000")
		if pageToken != "" {
			params.Set("page_token", pageToken)
		}
		body, err := p.doGet(ctx, "/v1beta1/options/trades", params)
		if err != nil {
			return nil, receipt, fmt.Errorf("alpaca/options: trades request failed: %w", err)
		}
		receipt.Pages++
		var response tradesResponse
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, receipt, fmt.Errorf("alpaca/options: unmarshal trades response: %w", err)
		}
		trades, ok := response.Trades[occSymbol]
		if !ok {
			trades, ok = response.Trades["O:"+occSymbol]
		}
		if !ok && len(response.Trades) != 0 {
			return nil, receipt, fmt.Errorf("alpaca/options: trades response omitted requested symbol %s", occSymbol)
		}
		for _, trade := range trades {
			at, err := parseAlpacaTime(trade.Time)
			if err != nil || trade.ID <= 0 || trade.Price <= 0 || trade.Size <= 0 {
				return nil, receipt, fmt.Errorf("alpaca/options: invalid trade for %s", occSymbol)
			}
			key := strconv.FormatInt(trade.ID, 10)
			if _, duplicate := seenTrades[key]; duplicate {
				return nil, receipt, fmt.Errorf("alpaca/options: duplicate trade for %s", occSymbol)
			}
			seenTrades[key] = struct{}{}
			values = append(values, data.OptionTradeObservation{ProviderID: key, Price: trade.Price, Size: trade.Size, Timestamp: at, Exchange: trade.Exchange})
		}
		if response.NextPageToken == "" {
			receipt.Entitled = true
			receipt.PaginationComplete = true
			break
		}
		if _, duplicate := seenPageTokens[response.NextPageToken]; duplicate {
			return nil, receipt, fmt.Errorf("alpaca/options: repeated page token")
		}
		seenPageTokens[response.NextPageToken] = struct{}{}
		pageToken = response.NextPageToken
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Timestamp.Before(values[j].Timestamp) })
	return values, receipt, nil
}

// ------------------------------------------------------------------
// HTTP helpers
// ------------------------------------------------------------------

func (p *OptionsDataProvider) doGet(ctx context.Context, requestPath string, params url.Values) ([]byte, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("alpaca/options: API key is required")
	}
	if p.apiSecret == "" {
		return nil, fmt.Errorf("alpaca/options: API secret is required")
	}

	u, err := url.Parse(p.baseURL)
	if err != nil {
		return nil, fmt.Errorf("alpaca/options: parse base url: %w", err)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(requestPath, "/")
	u.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("alpaca/options: create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("APCA-API-KEY-ID", p.apiKey)
	req.Header.Set("APCA-API-SECRET-KEY", p.apiSecret)

	startedAt := time.Now()
	p.logger.Debug("alpaca/options: sending request",
		slog.String("method", req.Method),
		slog.String("path", req.URL.Path),
	)

	resp, err := p.client.Do(req)
	if err != nil {
		p.logger.Warn("alpaca/options: request failed",
			slog.String("path", req.URL.Path),
			slog.Any("error", err),
			slog.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
		)
		return nil, fmt.Errorf("alpaca/options: do request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("alpaca/options: read response body: %w", err)
	}

	p.logger.Debug("alpaca/options: received response",
		slog.String("path", req.URL.Path),
		slog.Int("status", resp.StatusCode),
		slog.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
	)

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("alpaca/options: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return body, nil
}

// mapTimeframe converts a data.Timeframe to the Alpaca bar timeframe string.
func mapTimeframe(tf data.Timeframe) (string, error) {
	switch tf {
	case data.Timeframe1m:
		return "1Min", nil
	case data.Timeframe5m:
		return "5Min", nil
	case data.Timeframe15m:
		return "15Min", nil
	case data.Timeframe1h:
		return "1Hour", nil
	case data.Timeframe1d:
		return "1Day", nil
	default:
		return "", fmt.Errorf("alpaca/options: unsupported timeframe %q", tf)
	}
}
