package polygon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
)

var _ data.ExactHistoricalProvider = (*Provider)(nil)

// GetExactOHLCVWithReceipt retains exact numeric tokens and raw source pages.
func (p *Provider) GetExactOHLCVWithReceipt(ctx context.Context, ticker string, timeframe data.Timeframe, from, to time.Time, feed, adjustmentPolicy string) (data.ExactHistoricalResult, error) {
	receipt := data.HistoricalFetchReceipt{Provider: "polygon", Feed: feed, AdjustmentPolicy: adjustmentPolicy}
	if p == nil {
		return data.ExactHistoricalResult{Receipt: receipt}, errors.New("polygon: provider is nil")
	}
	if p.client == nil {
		return data.ExactHistoricalResult{Receipt: receipt}, errors.New("polygon: client is nil")
	}

	ticker = strings.TrimSpace(ticker)
	if ticker == "" {
		return data.ExactHistoricalResult{Receipt: receipt}, errors.New("polygon: ticker is required")
	}
	if from.After(to) {
		return data.ExactHistoricalResult{Receipt: receipt}, errors.New("polygon: from must be before or equal to to")
	}
	feed = strings.ToLower(strings.TrimSpace(feed))
	if feed != "sip" {
		return data.ExactHistoricalResult{Receipt: receipt}, fmt.Errorf("polygon: unsupported stock feed %q", feed)
	}
	receipt.Feed = feed
	adjusted := ""
	switch adjustmentPolicy {
	case "raw":
		adjusted = "false"
	case "adjusted":
		adjusted = "true"
	default:
		return data.ExactHistoricalResult{Receipt: receipt}, fmt.Errorf("polygon: unsupported adjustment policy %q", adjustmentPolicy)
	}

	mapping, err := mapTimeframe(timeframe)
	if err != nil {
		return data.ExactHistoricalResult{Receipt: receipt}, err
	}

	requestPath := fmt.Sprintf(
		"/v2/aggs/ticker/%s/range/%d/%s/%d/%d",
		url.PathEscape(ticker),
		mapping.multiplier,
		mapping.timespan,
		from.UTC().UnixMilli(),
		to.UTC().UnixMilli(),
	)
	baseParams := url.Values{
		"adjusted": []string{adjusted},
		"sort":     []string{"asc"},
		"limit":    []string{strconv.Itoa(polygonMaxPageSize)},
	}
	params := cloneQueryValues(baseParams)

	bars := make([]data.ExactHistoricalBar, 0, 128)
	pages := make([]data.HistoricalSourcePage, 0, 1)
	sourceBytes := 0
	seenNextURLs := make(map[string]struct{})
	for {
		body, err := p.client.Get(ctx, requestPath, params)
		if err != nil {
			return data.ExactHistoricalResult{Receipt: receipt}, err
		}
		receipt.Pages++
		sourceBytes += len(body)
		if p.client.apiKey != "" && bytes.Contains(body, []byte(p.client.apiKey)) {
			return data.ExactHistoricalResult{Receipt: receipt}, errors.New("polygon: source response contains credential material")
		}

		if len(body) > 16*1024*1024 || len(pages) >= 100 || sourceBytes > 64*1024*1024 {
			return data.ExactHistoricalResult{Receipt: receipt}, errors.New("polygon: exact source bounds exceeded")
		}
		fields, err := exactObjectFieldsBounded(body, 16*1024*1024)
		if err != nil {
			return data.ExactHistoricalResult{Receipt: receipt}, fmt.Errorf("polygon: exact response object: %w", err)
		}
		if raw, present := fields["status"]; present {
			var status string
			if err := json.Unmarshal(raw, &status); err != nil || (status != "OK" && status != "DELAYED") {
				return data.ExactHistoricalResult{Receipt: receipt}, errors.New("polygon: unsuccessful exact response status")
			}
		}
		if raw, present := fields["ticker"]; present {
			var returnedTicker string
			if json.Unmarshal(raw, &returnedTicker) != nil || returnedTicker != ticker {
				return data.ExactHistoricalResult{Receipt: receipt}, errors.New("polygon: response ticker mismatch")
			}
		}
		if raw, present := fields["adjusted"]; present {
			var returnedAdjusted bool
			if json.Unmarshal(raw, &returnedAdjusted) != nil || returnedAdjusted != (adjustmentPolicy == "adjusted") {
				return data.ExactHistoricalResult{Receipt: receipt}, errors.New("polygon: response adjustment mismatch")
			}
		}
		var response struct {
			NextURL string            `json:"next_url"`
			Results []json.RawMessage `json:"results"`
		}
		if err := json.Unmarshal(body, &response); err != nil {
			return data.ExactHistoricalResult{Receipt: receipt}, fmt.Errorf("polygon: decode aggregates response: %w", err)
		}

		if response.Results == nil {
			return data.ExactHistoricalResult{Receipt: receipt}, errors.New("polygon: explicit exact results required")
		}
		pages = append(pages, data.HistoricalSourcePage{RequestPath: requestPath, Query: params.Encode(), Body: bytes.Clone(body)})
		for rowIndex, raw := range response.Results {
			result, err := decodeExactAggregate(raw)
			if err != nil {
				return data.ExactHistoricalResult{Receipt: receipt}, err
			}
			stamp := time.UnixMilli(result.Timestamp).UTC()
			if stamp.Before(from) || stamp.After(to) {
				return data.ExactHistoricalResult{Receipt: receipt}, errors.New("polygon: exact bar outside requested interval")
			}
			bars = append(bars, data.ExactHistoricalBar{
				PageIndex: len(pages) - 1, RowIndex: rowIndex,
				Timestamp: time.UnixMilli(result.Timestamp).UTC(),
				Open:      result.Open, High: result.High, Low: result.Low, Close: result.Close, Volume: result.Volume,
				TradeCount: result.TradeCount, VWAP: result.VWAP, Raw: result.Raw,
			})
		}

		if strings.TrimSpace(response.NextURL) == "" {
			receipt.Entitled = true
			receipt.PaginationComplete = true
			break
		}
		if _, duplicate := seenNextURLs[response.NextURL]; duplicate {
			return data.ExactHistoricalResult{Receipt: receipt}, errors.New("polygon: repeated aggregates next_url")
		}
		seenNextURLs[response.NextURL] = struct{}{}

		requestPath, params, err = nextPageRequest(response.NextURL, baseParams)
		if err != nil {
			return data.ExactHistoricalResult{Receipt: receipt}, err
		}
	}

	return data.ExactHistoricalResult{Bars: bars, Pages: pages, Receipt: receipt}, nil
}
