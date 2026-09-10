package alpaca

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

var _ data.ExactOptionsTradeProvider = (*OptionsDataProvider)(nil)

// GetExactOptionsTradesWithReceipt preserves decimal tokens and raw Alpaca pages.
// It neither falls back to floating point nor infers missing source fields.
func (p *OptionsDataProvider) GetExactOptionsTradesWithReceipt(ctx context.Context, symbol string, from, to time.Time, feed string) (data.ExactOptionsTradeResult, error) {
	result := data.ExactOptionsTradeResult{Receipt: data.HistoricalFetchReceipt{Provider: "alpaca", Feed: feed, AdjustmentPolicy: "raw"}}
	fail := func(err error) (data.ExactOptionsTradeResult, error) {
		return data.ExactOptionsTradeResult{Receipt: result.Receipt}, err
	}
	if p == nil || p.client == nil || p.apiKey == "" || p.apiSecret == "" {
		return fail(fmt.Errorf("alpaca/options: exact provider configuration incomplete"))
	}
	if _, err := domain.ParseOCC(symbol); err != nil || strings.HasPrefix(symbol, "O:") || strings.TrimSpace(symbol) != symbol {
		return fail(fmt.Errorf("alpaca/options: exact canonical OCC symbol required"))
	}
	if from.After(to) || from.Before(time.Unix(0, 0)) || (feed != "opra" && feed != "indicative") {
		return fail(fmt.Errorf("alpaca/options: invalid exact trade interval or feed"))
	}
	const path = "/v1beta1/options/trades"
	params := url.Values{"symbols": {symbol}, "start": {from.UTC().Format(time.RFC3339Nano)}, "end": {to.UTC().Format(time.RFC3339Nano)}, "limit": {"1000"}, "feed": {feed}}
	seen := map[string]bool{}
	seenTrades := map[string]bool{}
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
		keyed, err := exactOptionFields(fields["trades"], 16*1024*1024)
		if err != nil || len(keyed) != 1 {
			return fail(fmt.Errorf("alpaca/options: exact requested symbol map required"))
		}
		rawRows, ok := keyed[symbol]
		if !ok {
			return fail(fmt.Errorf("alpaca/options: exact response symbol mismatch"))
		}
		var rows []json.RawMessage
		if json.Unmarshal(rawRows, &rows) != nil || rows == nil {
			return fail(fmt.Errorf("alpaca/options: explicit exact trades array required"))
		}
		result.Pages = append(result.Pages, data.HistoricalSourcePage{RequestPath: path, Query: params.Encode(), Body: bytes.Clone(body)})
		for index, row := range rows {
			trade, err := decodeExactOptionTrade(row)
			if err != nil {
				return fail(err)
			}
			if trade.Timestamp.Before(from) || trade.Timestamp.After(to) {
				return fail(fmt.Errorf("alpaca/options: exact trade outside requested interval"))
			}
			if seenTrades[trade.ProviderID] {
				return fail(fmt.Errorf("alpaca/options: duplicate exact trade identity"))
			}
			seenTrades[trade.ProviderID] = true
			trade.PageIndex, trade.RowIndex = len(result.Pages)-1, index
			result.Trades = append(result.Trades, trade)
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
