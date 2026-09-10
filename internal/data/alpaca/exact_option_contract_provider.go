package alpaca

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// ExactContractProvider is a read-only reference client, separate from the market
// data host and broker execution client. It exposes no order or account methods.
type ExactContractProvider struct {
	transport *OptionsDataProvider
}

var _ data.ExactOptionContractProvider = (*ExactContractProvider)(nil)

// NewExactContractProvider requires an explicit reference API origin. HTTP is
// accepted only for loopback test servers; production origins must be Alpaca HTTPS.
func NewExactContractProvider(referenceURL, apiKey, apiSecret string) (*ExactContractProvider, error) {
	u, err := url.Parse(referenceURL)
	if err != nil || u.User != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Opaque != "" {
		return nil, fmt.Errorf("alpaca/contracts: explicit reference API origin required")
	}
	ip := net.ParseIP(u.Hostname())
	local := u.Scheme == "http" && ip != nil && ip.IsLoopback()
	production := u.Scheme == "https" && u.Port() == "" && (u.Host == "api.alpaca.markets" || u.Host == "paper-api.alpaca.markets")
	if !local && !production {
		return nil, fmt.Errorf("alpaca/contracts: reference API origin rejected")
	}
	transport := NewOptionsDataProvider(apiKey, apiSecret, nil)
	if transport.apiKey == "" || transport.apiSecret == "" {
		return nil, fmt.Errorf("alpaca/contracts: credentials required")
	}
	transport.baseURL = referenceURL
	return &ExactContractProvider{transport: transport}, nil
}

// GetExactOptionContract retains the exact response for a strict OCC symbol.
// A successful current lookup does not establish historical availability or rights.
func (p *ExactContractProvider) GetExactOptionContract(ctx context.Context, symbol string) (data.ExactOptionContractResult, error) {
	var empty data.ExactOptionContractResult
	if p == nil || p.transport == nil || p.transport.client == nil {
		return empty, fmt.Errorf("alpaca/contracts: provider unavailable")
	}
	if _, err := domain.ParseStrictOCC(symbol); err != nil {
		return empty, fmt.Errorf("alpaca/contracts: strict OCC symbol required")
	}
	path := "/v2/options/contracts/" + symbol
	body, err := p.transport.getExactOptionPage(ctx, path, nil)
	if err != nil {
		return empty, err
	}
	if bytes.Contains(body, []byte(p.transport.apiKey)) || bytes.Contains(body, []byte(p.transport.apiSecret)) {
		return empty, fmt.Errorf("alpaca/contracts: credential echo rejected")
	}
	contract, err := decodeExactOptionContract(body)
	if err != nil {
		return empty, err
	}
	if contract.Symbol != symbol {
		return empty, fmt.Errorf("alpaca/contracts: response symbol mismatch")
	}
	return data.ExactOptionContractResult{
		Contract:   contract,
		Page:       data.HistoricalSourcePage{RequestPath: path, Body: bytes.Clone(body)},
		ObservedAt: time.Now().UTC(),
	}, nil
}
