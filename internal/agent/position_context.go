package agent

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ContextKeyCurrentPosition labels the account's current position in debate
// context maps. It is a context label, not an agent role.
const ContextKeyCurrentPosition AgentRole = "current_position"

// PositionSnapshot is what the strategy's execution scope holds in its ticker
// when a run starts. Trader and risk roles see it so they do not reason about
// "existing exposure" the account does not have. A nil snapshot means the
// market type has no position semantics wired here and nothing is shown.
type PositionSnapshot struct {
	Ticker string `json:"ticker"`
	// Known is false when the lookup failed; agents are told not to assume
	// any holding.
	Known bool `json:"known"`
	// Quantity is the open long quantity this strategy owns and can sell.
	Quantity float64 `json:"quantity"`
	// AvgEntry is the quantity-weighted average entry of those lots.
	AvgEntry      float64    `json:"avg_entry,omitempty"`
	UnrealizedPnL *float64   `json:"unrealized_pnl,omitempty"`
	OpenedAt      *time.Time `json:"opened_at,omitempty"`
	// AccountQuantity is the account-wide open long quantity in the ticker,
	// including lots owned by other strategies that this one cannot sell.
	AccountQuantity float64   `json:"account_quantity,omitempty"`
	AsOf            time.Time `json:"as_of"`
}

// PromptText renders the snapshot as an explicit statement for LLM prompts.
func (p *PositionSnapshot) PromptText() string {
	if p == nil {
		return ""
	}
	ticker := strings.TrimSpace(p.Ticker)
	if !p.Known {
		return fmt.Sprintf("Current position in %s: UNKNOWN. The position lookup failed. Do not assume the account already holds %s; treat any BUY as a new position.", ticker, ticker)
	}
	var b strings.Builder
	if p.Quantity <= 0 {
		fmt.Fprintf(&b, "Current position in %s: FLAT. This strategy holds no %s. BUY opens a new long position. SELL cannot execute because the account does not sell short; use HOLD to stay flat.", ticker, ticker)
	} else {
		fmt.Fprintf(&b, "Current position in %s: LONG %s at an average entry of %.2f", ticker, formatQuantity(p.Quantity), p.AvgEntry)
		if p.OpenedAt != nil && !p.OpenedAt.IsZero() {
			fmt.Fprintf(&b, ", opened %s", p.OpenedAt.UTC().Format("2006-01-02"))
		}
		if p.UnrealizedPnL != nil {
			fmt.Fprintf(&b, ", unrealized P&L %.2f", *p.UnrealizedPnL)
		}
		fmt.Fprintf(&b, ". BUY adds to the position. SELL reduces or exits it, up to %s. HOLD leaves it unchanged.", formatQuantity(p.Quantity))
	}
	if other := p.AccountQuantity - p.Quantity; other > 0 {
		fmt.Fprintf(&b, " The account also holds %s %s through other strategies; this strategy cannot sell those.", formatQuantity(other), ticker)
	}
	return b.String()
}

// WithPositionContext returns a copy of reports with the position statement
// added under ContextKeyCurrentPosition. The input map is not modified.
func WithPositionContext(reports map[AgentRole]string, position *PositionSnapshot) map[AgentRole]string {
	text := position.PromptText()
	if text == "" {
		return reports
	}
	out := make(map[AgentRole]string, len(reports)+1)
	for role, report := range reports {
		out[role] = report
	}
	out[ContextKeyCurrentPosition] = text
	return out
}

func formatQuantity(quantity float64) string {
	return strconv.FormatFloat(quantity, 'f', -1, 64)
}
