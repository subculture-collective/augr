package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// AccountContext is a broker snapshot, not an execution authorization. Nil means
// unknown; a non-nil context with no positions means the broker reported none.
type AccountContext struct {
	Source      string            `json:"source"`
	IsPaper     bool              `json:"is_paper"`
	ObservedAt  time.Time         `json:"observed_at"`
	Currency    string            `json:"currency"`
	Cash        float64           `json:"cash"`
	BuyingPower float64           `json:"buying_power"`
	Equity      float64           `json:"equity"`
	Positions   []AccountPosition `json:"positions"`
}

// AccountPosition contains holdings facts without broker account identifiers.
type AccountPosition struct {
	Ticker           string              `json:"ticker"`
	Side             domain.PositionSide `json:"side"`
	Quantity         float64             `json:"quantity"`
	AvgEntry         float64             `json:"avg_entry"`
	AssetClass       domain.AssetClass   `json:"asset_class,omitempty"`
	UnderlyingTicker string              `json:"underlying_ticker,omitempty"`
}

func CloneAccountContext(account *AccountContext) *AccountContext {
	if account == nil {
		return nil
	}
	clone := *account
	clone.Positions = append([]AccountPosition{}, account.Positions...)
	return &clone
}

// AccountContextPrompt distinguishes an absent snapshot from confirmed flat exposure.
func AccountContextPrompt(account *AccountContext, ticker string) string {
	if account == nil {
		return "Account context: unavailable. Do not assume existing exposure or an empty portfolio. Cash and holdings are unknown."
	}
	payload, err := json.Marshal(account)
	if err != nil {
		return "Account context: invalid. Do not infer cash or holdings."
	}
	held := false
	for _, position := range account.Positions {
		if strings.EqualFold(strings.TrimSpace(position.Ticker), strings.TrimSpace(ticker)) && position.Quantity != 0 {
			held = true
		}
	}
	exposure := fmt.Sprintf("The broker reports no direct position in %s. HOLD means remaining without a direct position; assess any entry on its merits. Other holdings, including options on this underlying, are listed below.", ticker)
	if held {
		exposure = fmt.Sprintf("The broker reports an existing position in %s; use its actual side and quantity below.", ticker)
	}
	return "Account context (snapshot; execution rechecks account and risk limits):\n" + exposure + "\nUse these holdings facts over assumptions in research or debate. Do not invent exposure. Snapshot balances do not reserve capital or account for subsequent fills.\n" + string(payload)
}
