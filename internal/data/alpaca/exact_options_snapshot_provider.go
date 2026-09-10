package alpaca

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

var _ data.ExactOptionsSnapshotProvider = (*OptionsDataProvider)(nil)

// GetExactOptionsSnapshotsWithReceipt preserves current snapshot objects and pages.
// It does not infer reference terms or claim historical interval coverage.
func (p *OptionsDataProvider) GetExactOptionsSnapshotsWithReceipt(ctx context.Context, underlying, feed string) (data.ExactOptionsSnapshotResult, error) {
	result := data.ExactOptionsSnapshotResult{Receipt: data.HistoricalFetchReceipt{Provider: "alpaca", Feed: feed, AdjustmentPolicy: "raw"}}
	fail := func(err error) (data.ExactOptionsSnapshotResult, error) {
		return data.ExactOptionsSnapshotResult{Receipt: result.Receipt}, err
	}
	if p == nil || p.client == nil || p.apiKey == "" || p.apiSecret == "" {
		return fail(fmt.Errorf("alpaca/options: exact provider configuration incomplete"))
	}
	if underlying == "" || len(underlying) > 6 || strings.Trim(underlying, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") != "" || (feed != "opra" && feed != "indicative") {
		return fail(fmt.Errorf("alpaca/options: invalid exact snapshot underlying or feed"))
	}
	path := "/v1beta1/options/snapshots/" + underlying
	params := url.Values{"feed": {feed}, "limit": {"100"}}
	seenTokens, seenSymbols := map[string]bool{}, map[string]bool{}
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
		keyed, err := exactOptionFields(fields["snapshots"], 16*1024*1024)
		if err != nil {
			return fail(fmt.Errorf("alpaca/options: explicit exact snapshot map required"))
		}
		result.Pages = append(result.Pages, data.HistoricalSourcePage{RequestPath: path, Query: params.Encode(), Body: bytes.Clone(body)})
		symbols := make([]string, 0, len(keyed))
		for symbol := range keyed {
			symbols = append(symbols, symbol)
		}
		sort.Strings(symbols)
		for _, symbol := range symbols {
			contract, err := domain.ParseStrictOCC(symbol)
			if err != nil || contract.Underlying != underlying || strings.HasPrefix(symbol, "O:") || strings.TrimSpace(symbol) != symbol || seenSymbols[symbol] {
				return fail(fmt.Errorf("alpaca/options: invalid or duplicate exact snapshot symbol"))
			}
			seenSymbols[symbol] = true
			snapshot, err := decodeExactOptionSnapshot(keyed[symbol])
			if err != nil {
				return fail(err)
			}
			snapshot.PageIndex, snapshot.Symbol = len(result.Pages)-1, symbol
			result.Snapshots = append(result.Snapshots, snapshot)
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
		if len(next) > 4096 || seenTokens[next] {
			return fail(fmt.Errorf("alpaca/options: repeated or oversized page token"))
		}
		seenTokens[next] = true
		params.Set("page_token", next)
	}
}
