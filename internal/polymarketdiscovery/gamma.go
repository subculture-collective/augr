// Package polymarketdiscovery autonomously generates Polymarket trading
// strategies. It screens open markets from the Polymarket Gamma API, asks an
// LLM to choose a strategy template and write a thesis, and persists the
// result as a paper strategy with an active thesis attached.
package polymarketdiscovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// gammaString preserves Gamma API fields that historically arrived as JSON
// strings but may now arrive as JSON numbers. Keeping string storage avoids
// changing downstream parsing code while making decode tolerant to both shapes.
type gammaString string

func (s *gammaString) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*s = gammaString(text)
		return nil
	}

	var num json.Number
	if err := json.Unmarshal(data, &num); err == nil {
		*s = gammaString(num.String())
		return nil
	}

	return fmt.Errorf("gamma string: unsupported value %s", string(data))
}

func (s gammaString) String() string { return string(s) }

const defaultGammaBaseURL = "https://gamma-api.polymarket.com"

// GammaMarket is the subset of Gamma API market fields used by discovery.
//
// Gamma serialises numeric fields as both strings and numbers depending on the
// endpoint. We keep them as strings and parse on demand.
type GammaMarket struct {
	Slug             string      `json:"slug"`
	Question         string      `json:"question"`
	Description      string      `json:"description"`
	Category         string      `json:"category"`
	ConditionID      string      `json:"conditionId"`
	Active           bool        `json:"active"`
	Closed           bool        `json:"closed"`
	Archived         bool        `json:"archived"`
	AcceptingOrders  bool        `json:"acceptingOrders"`
	EndDate          string      `json:"endDate"`
	StartDate        string      `json:"startDate"`
	Outcomes         gammaString `json:"outcomes"`
	OutcomePrices    gammaString `json:"outcomePrices"`
	Volume           gammaString `json:"volume"`
	Volume24Hr       gammaString `json:"volume24hr"`
	Liquidity        gammaString `json:"liquidity"`
	BestBid          any         `json:"bestBid"`
	BestAsk          any         `json:"bestAsk"`
	Spread           any         `json:"spread"`
	LastTradePrice   any         `json:"lastTradePrice"`
	ResolutionSource string      `json:"resolutionSource"`
}

// FloatField parses a Gamma numeric field that may arrive as string or number.
func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case nil:
		return 0, false
	case float64:
		return x, true
	case string:
		x = strings.TrimSpace(x)
		if x == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(x, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// VolumeFloat returns the cumulative volume as float64.
func (m *GammaMarket) VolumeFloat() float64 {
	f, _ := strconv.ParseFloat(m.Volume.String(), 64)
	return f
}

// Volume24HrFloat returns the 24-hour volume as float64.
func (m *GammaMarket) Volume24HrFloat() float64 {
	f, _ := strconv.ParseFloat(m.Volume24Hr.String(), 64)
	return f
}

// LiquidityFloat returns liquidity as float64.
func (m *GammaMarket) LiquidityFloat() float64 {
	f, _ := strconv.ParseFloat(m.Liquidity.String(), 64)
	return f
}

// BestBidFloat returns the YES best-bid price.
func (m *GammaMarket) BestBidFloat() (float64, bool) { return toFloat(m.BestBid) }

// BestAskFloat returns the YES best-ask price.
func (m *GammaMarket) BestAskFloat() (float64, bool) { return toFloat(m.BestAsk) }

// SpreadFloat returns the spread (best ask - best bid).
func (m *GammaMarket) SpreadFloat() (float64, bool) { return toFloat(m.Spread) }

// LastPriceFloat returns the last trade price for YES.
func (m *GammaMarket) LastPriceFloat() (float64, bool) { return toFloat(m.LastTradePrice) }

// EndTime parses EndDate as RFC3339, returning zero time when unparseable.
func (m *GammaMarket) EndTime() time.Time {
	if m.EndDate == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, m.EndDate)
	if err != nil {
		return time.Time{}
	}
	return t
}

// OutcomeLabels returns the parsed outcome label list (e.g. ["Yes","No"]).
func (m *GammaMarket) OutcomeLabels() []string {
	var out []string
	if m.Outcomes.String() == "" {
		return out
	}
	_ = json.Unmarshal([]byte(m.Outcomes.String()), &out)
	return out
}

// IsBinaryYesNo returns true when outcomes are exactly Yes/No.
func (m *GammaMarket) IsBinaryYesNo() bool {
	out := m.OutcomeLabels()
	if len(out) != 2 {
		return false
	}
	return strings.EqualFold(out[0], "Yes") && strings.EqualFold(out[1], "No")
}

// FetchOpenMarkets queries the Gamma API for open, accepting-orders markets,
// ordered by 24h volume desc. limit caps total markets returned across pages.
func FetchOpenMarkets(ctx context.Context, baseURL string, limit int) ([]GammaMarket, error) {
	if baseURL == "" {
		baseURL = defaultGammaBaseURL
	}
	if limit <= 0 {
		limit = 100
	}

	var all []GammaMarket
	const pageSize = 100
	for offset := 0; offset < limit; offset += pageSize {
		want := pageSize
		if offset+want > limit {
			want = limit - offset
		}
		url := fmt.Sprintf(
			"%s/markets?closed=false&active=true&order=volume24hr&ascending=false&limit=%d&offset=%d",
			baseURL, want, offset,
		)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("gamma fetch: %w", err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode/100 != 2 {
			return nil, fmt.Errorf("gamma fetch: status %d", resp.StatusCode)
		}
		var page []GammaMarket
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("gamma decode: %w", err)
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		if len(page) < want {
			break
		}
	}
	return all, nil
}
