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
)

// MarketClockEvidence is provider calendar classification at an explicitly
// requested instant, not security-level halt clearance or a current quote.
type MarketClockEvidence struct {
	Market, MIC, Phase, RequestPath, ResponseSHA256 string
	At, PhaseUntil, ObservedAt                      time.Time
	IsMarketDay                                     bool
	RawResponse                                     []byte
}

// MarketClockProvider reads the paper API's calendar only. It creates no broker
// account relationship and cannot submit an order.
type MarketClockProvider struct {
	apiKey, apiSecret, baseURL string
	client                     *http.Client
}

func NewMarketClockProvider(apiKey, apiSecret string) *MarketClockProvider {
	return &MarketClockProvider{apiKey: strings.TrimSpace(apiKey), apiSecret: strings.TrimSpace(apiSecret), baseURL: "https://paper-api.alpaca.markets", client: &http.Client{Timeout: defaultTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// ClockAt binds calendar evidence to the quote's exact timestamp, avoiding a
// later current clock incorrectly classifying a quote across a session boundary.
// Contract: https://docs.alpaca.markets/us/reference/clock-1.md
func (p *MarketClockProvider) ClockAt(ctx context.Context, market string, at time.Time) (*MarketClockEvidence, error) {
	if p == nil || p.client == nil || p.apiKey == "" || p.apiSecret == "" || at.IsZero() || at.After(time.Now()) {
		return nil, fmt.Errorf("alpaca: clock requires credentials and a nonfuture instant")
	}
	// This first source binding intentionally supports only the observed IEX
	// market. Do not silently substitute a different exchange's calendar.
	if market != "IEX" {
		return nil, fmt.Errorf("alpaca: unsupported explicit clock market")
	}
	path := "/v3/clock?" + url.Values{"markets": {market}, "time": {at.UTC().Format(time.RFC3339Nano)}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("alpaca: create clock request")
	}
	req.Header.Set("APCA-API-KEY-ID", p.apiKey)
	req.Header.Set("APCA-API-SECRET-KEY", p.apiSecret)
	response, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("alpaca: clock transport failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("alpaca: clock HTTP %d", response.StatusCode)
	}
	const maxResponse = 1 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil || len(body) > maxResponse {
		return nil, fmt.Errorf("alpaca: clock response unreadable or oversized")
	}
	observed := time.Now().UTC()
	var payload struct {
		Clocks []struct {
			Market struct {
				Acronym string `json:"acronym"`
				MIC     string `json:"mic"`
			} `json:"market"`
			Timestamp   time.Time `json:"timestamp"`
			PhaseUntil  time.Time `json:"phase_until"`
			IsMarketDay *bool     `json:"is_market_day"`
			Phase       string    `json:"phase"`
		} `json:"clocks"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || len(payload.Clocks) != 1 {
		return nil, fmt.Errorf("alpaca: clock requires one exact market result")
	}
	clock := payload.Clocks[0]
	if clock.Market.Acronym != market || clock.Market.MIC != "IEXG" || !clock.Timestamp.Equal(at) || !clock.PhaseUntil.After(at) || clock.IsMarketDay == nil {
		return nil, fmt.Errorf("alpaca: clock lacks matching timed market facts")
	}
	switch clock.Phase {
	case "closed":
	case "pre", "core", "lunch", "post":
		if !*clock.IsMarketDay {
			return nil, fmt.Errorf("alpaca: clock phase contradicts market day")
		}
	default:
		return nil, fmt.Errorf("alpaca: unknown clock phase")
	}
	digest := sha256.Sum256(body)
	return &MarketClockEvidence{Market: market, MIC: clock.Market.MIC, Phase: clock.Phase, RequestPath: path, ResponseSHA256: hex.EncodeToString(digest[:]), At: at.UTC(), PhaseUntil: clock.PhaseUntil.UTC(), ObservedAt: observed, IsMarketDay: *clock.IsMarketDay, RawResponse: append([]byte(nil), body...)}, nil
}
