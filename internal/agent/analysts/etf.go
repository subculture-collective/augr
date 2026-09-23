package analysts

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
)

const ETFFundamentalsSystemPrompt = `You analyze SPY as an exchange-traded fund using dated issuer evidence. Evaluate fund scale, gross expense ratio, and holdings concentration. Corporate revenue growth, gross margin, and debt-to-equity are not fund financial statements and must not be inferred. Tracking error and forward returns are unknown unless supplied. Do not treat fund assets or a data-quality pass as an investment recommendation. State source dates, limitations, and missing evidence. Market liquidity and trade eligibility are separate checks.`

func FormatETFFundamentalsUserPrompt(f *data.ETFFundamentals) string {
	var b strings.Builder
	fmt.Fprintf(&b, "SPY ETF evidence (%s)\nISIN: %s\nNet assets USD: %.2f\n", sanitizeCell(f.Contract), sanitizeCell(f.ISIN), f.NetAssetsUSD)
	if f.GrossExpenseRatio != nil {
		fmt.Fprintf(&b, "Gross expense ratio: %.6f%%\n", *f.GrossExpenseRatio*100)
	}
	fmt.Fprintf(&b, "Profile as of: %s; holdings as of: %s\n", f.ProfileSource.AsOf.Format(time.DateOnly), f.HoldingsSource.AsOf.Format(time.DateOnly))
	fmt.Fprintf(&b, "Issuer profile: %s (SHA256 %s)\nIssuer holdings: %s (SHA256 %s)\n", f.ProfileSource.URL, f.ProfileSource.SHA256, f.HoldingsSource.URL, f.HoldingsSource.SHA256)
	holdings := append([]data.ETFHolding(nil), f.Holdings...)
	sort.Slice(holdings, func(i, j int) bool { return holdings[i].Weight > holdings[j].Weight })
	total := 0.0
	for _, h := range holdings {
		total += h.Weight
	}
	fmt.Fprintf(&b, "Holdings retained: %d; total weight: %.6f%%\nLargest ten holdings:\n", len(holdings), total*100)
	for i, h := range holdings {
		if i == 10 {
			break
		}
		fmt.Fprintf(&b, "%s: %.6f%%\n", sanitizeCell(h.Symbol), h.Weight*100)
	}
	b.WriteString("Corporate fundamentals and tracking error are not supplied. Provide an ETF analysis with explicit limitations.\n")
	return b.String()
}
