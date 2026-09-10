package alpaca

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

var _ data.ExactOptionsHistoricalProvider = (*OptionsDataProvider)(nil)

// GetExactOptionsOHLCVWithReceipt preserves decimal tokens and raw Alpaca pages.
// It neither falls back to floating point nor infers missing source fields.
func (p *OptionsDataProvider) GetExactOptionsOHLCVWithReceipt(ctx context.Context, symbol string, timeframe data.Timeframe, from, to time.Time, feed, adjustment string) (data.ExactHistoricalResult, error) {
	result := data.ExactHistoricalResult{Receipt: data.HistoricalFetchReceipt{Provider: "alpaca", Feed: feed, AdjustmentPolicy: adjustment}}
	fail := func(err error) (data.ExactHistoricalResult, error) {
		return data.ExactHistoricalResult{Receipt: result.Receipt}, err
	}
	if p == nil || p.client == nil || p.apiKey == "" || p.apiSecret == "" {
		return fail(fmt.Errorf("alpaca/options: exact provider configuration incomplete"))
	}
	if _, err := domain.ParseOCC(symbol); err != nil || strings.HasPrefix(symbol, "O:") || strings.TrimSpace(symbol) != symbol {
		return fail(fmt.Errorf("alpaca/options: exact canonical OCC symbol required"))
	}
	if from.After(to) || from.Before(time.Unix(0, 0)) || (feed != "opra" && feed != "indicative") || adjustment != "raw" {
		return fail(fmt.Errorf("alpaca/options: invalid exact interval, feed, or adjustment"))
	}
	tf, err := mapTimeframe(timeframe)
	if err != nil {
		return fail(err)
	}
	const path = "/v1beta1/options/bars"
	params := url.Values{"symbols": {symbol}, "timeframe": {tf}, "start": {from.UTC().Format(time.RFC3339Nano)}, "end": {to.UTC().Format(time.RFC3339Nano)}, "limit": {"1000"}, "feed": {feed}}
	seen := map[string]bool{}
	total := 0
	for {
		if len(result.Pages) >= 100 {
			return fail(fmt.Errorf("alpaca/options: exact page limit exceeded"))
		}
		body, err := p.getExactOptionPage(ctx, path, params)
		if err != nil {
			return fail(err)
		}
		result.Receipt.Pages++
		total += len(body)
		if total > 64*1024*1024 || bytes.Contains(body, []byte(p.apiKey)) || bytes.Contains(body, []byte(p.apiSecret)) {
			return fail(fmt.Errorf("alpaca/options: exact source size or credential boundary rejected"))
		}
		fields, err := exactOptionFields(body, 16*1024*1024)
		if err != nil {
			return fail(err)
		}
		keyed, err := exactOptionFields(fields["bars"], 16*1024*1024)
		if err != nil || len(keyed) != 1 {
			return fail(fmt.Errorf("alpaca/options: exact requested symbol map required"))
		}
		rawRows, ok := keyed[symbol]
		if !ok {
			return fail(fmt.Errorf("alpaca/options: exact response symbol mismatch"))
		}
		var rows []json.RawMessage
		if json.Unmarshal(rawRows, &rows) != nil || rows == nil {
			return fail(fmt.Errorf("alpaca/options: explicit exact bars array required"))
		}
		result.Pages = append(result.Pages, data.HistoricalSourcePage{RequestPath: path, Query: params.Encode(), Body: bytes.Clone(body)})
		for index, row := range rows {
			bar, err := decodeExactOptionBar(row)
			if err != nil {
				return fail(err)
			}
			if bar.Timestamp.Before(from) || bar.Timestamp.After(to) {
				return fail(fmt.Errorf("alpaca/options: exact bar outside requested interval"))
			}
			bar.PageIndex, bar.RowIndex = len(result.Pages)-1, index
			result.Bars = append(result.Bars, bar)
		}
		rawToken, exists := fields["next_page_token"]
		if !exists {
			return fail(fmt.Errorf("alpaca/options: explicit pagination termination required"))
		}
		var next string
		if !bytes.Equal(bytes.TrimSpace(rawToken), []byte("null")) && json.Unmarshal(rawToken, &next) != nil {
			return fail(fmt.Errorf("alpaca/options: invalid next page token"))
		}
		if next == "" {
			result.Receipt.Entitled, result.Receipt.PaginationComplete = true, true
			return result, nil
		}
		if len(next) > 4096 || seen[next] {
			return fail(fmt.Errorf("alpaca/options: repeated or oversized page token"))
		}
		seen[next] = true
		params.Set("page_token", next)
	}
}

func (p *OptionsDataProvider) getExactOptionPage(ctx context.Context, path string, params url.Values) ([]byte, error) {
	u, err := url.Parse(p.baseURL)
	if err != nil || u.User != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("alpaca/options: invalid exact provider URL")
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("alpaca/options: create exact request failed")
	}
	req.Header.Set("APCA-API-KEY-ID", p.apiKey)
	req.Header.Set("APCA-API-SECRET-KEY", p.apiSecret)
	req.Header.Set("Accept", "application/json")
	client := *p.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("alpaca/options: exact request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("alpaca/options: exact HTTP status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024+1))
	if err != nil || len(body) > 16*1024*1024 {
		return nil, fmt.Errorf("alpaca/options: exact response read or size failure")
	}
	return body, nil
}
