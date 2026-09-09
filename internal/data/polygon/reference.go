package polygon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TickerReferenceEvidence retains provider identity evidence without asserting
// execution mechanics. A provider round lot is not a minimum execution lot.
type TickerReferenceEvidence struct {
	RequestPath     string
	AsOfDate        string
	ObservedAt      time.Time
	ResponseSHA256  string
	RawResponse     []byte
	Ticker          string
	CompositeFIGI   string
	ShareClassFIGI  string
	PrimaryExchange string
	Currency        string
	Type            string
	Active          bool
}

// GetTickerReference fetches a dated reference, retaining exact response bytes.
// AsOfDate describes the provider query, never historical availability. Callers
// must separately source tick/lot/settlement facts before creating active records.
func (c *Client) GetTickerReference(ctx context.Context, ticker, asOfDate string) (*TickerReferenceEvidence, error) {
	if ticker == "" || ticker != strings.TrimSpace(ticker) || strings.ContainsAny(ticker, "/?&#\\") {
		return nil, fmt.Errorf("polygon: invalid reference ticker")
	}
	date, err := time.Parse("2006-01-02", asOfDate)
	if err != nil || date.Format("2006-01-02") != asOfDate {
		return nil, fmt.Errorf("polygon: reference requires an exact as-of date")
	}
	path := "/v3/reference/tickers/" + url.PathEscape(ticker)
	params := url.Values{"date": []string{asOfDate}}
	body, err := c.Get(ctx, path, params)
	if err != nil {
		return nil, err
	}
	observed := time.Now().UTC().Truncate(time.Microsecond)
	var response struct {
		Status  string `json:"status"`
		Results struct {
			Ticker          string `json:"ticker"`
			Market          string `json:"market"`
			CompositeFIGI   string `json:"composite_figi"`
			ShareClassFIGI  string `json:"share_class_figi"`
			PrimaryExchange string `json:"primary_exchange"`
			Currency        string `json:"currency_name"`
			Type            string `json:"type"`
			Active          bool   `json:"active"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("polygon: decode reference: %w", err)
	}
	r := response.Results
	if response.Status != "OK" || r.Ticker != ticker || r.Market != "stocks" || strings.TrimSpace(r.CompositeFIGI) == "" || strings.TrimSpace(r.ShareClassFIGI) == "" || r.PrimaryExchange == "" || r.Currency == "" || r.Type == "" {
		return nil, fmt.Errorf("polygon: reference lacks matching stock identity evidence")
	}
	digest := sha256.Sum256(body)
	return &TickerReferenceEvidence{RequestPath: path + "?" + params.Encode(), AsOfDate: asOfDate, ObservedAt: observed, ResponseSHA256: hex.EncodeToString(digest[:]), RawResponse: append([]byte(nil), body...), Ticker: r.Ticker, CompositeFIGI: r.CompositeFIGI, ShareClassFIGI: r.ShareClassFIGI, PrimaryExchange: r.PrimaryExchange, Currency: r.Currency, Type: r.Type, Active: r.Active}, nil
}
