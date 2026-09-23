package analysts

import (
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
)

func TestETFFundamentalsPromptDoesNotInventCorporateMetrics(t *testing.T) {
	fee := 0.000945
	f := &data.ETFFundamentals{Contract: data.SPYETFContractV1, Ticker: "SPY", ISIN: "US78462F1030", NetAssetsUSD: 100, GrossExpenseRatio: &fee, Holdings: []data.ETFHolding{{Symbol: "AAA", Weight: 1}}, ProfileSource: data.ETFSource{AsOf: time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)}}
	prompt := FormatFundamentalsAnalystUserPrompt("SPY", &data.Fundamentals{ETF: f})
	for _, want := range []string{"Gross expense ratio: 0.094500%", "2026-09-21", "AAA: 100.000000%", "Corporate fundamentals and tracking error are not supplied"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("missing %s", want)
		}
	}
	for _, unwanted := range []string{"| Revenue |", "| Debt-to-Equity |", "| P/E Ratio |"} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("invented corporate field %s", unwanted)
		}
	}
}
