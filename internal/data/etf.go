package data

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// SPYETFContractV1 is opt-in and never selected by ticker inference.
const SPYETFContractV1 = "spy-ssga-etf-v1"

type ETFHolding struct {
	Symbol string  `json:"symbol"`
	Weight float64 `json:"weight_fraction"`
}

type ETFSource struct {
	URL       string    `json:"url"`
	SHA256    string    `json:"sha256"`
	AsOf      time.Time `json:"as_of"`
	FetchedAt time.Time `json:"fetched_at"`
}

// ETFFundamentals keeps fund-level evidence separate from corporate metrics.
type ETFFundamentals struct {
	Contract          string       `json:"contract"`
	Ticker            string       `json:"ticker"`
	ISIN              string       `json:"isin"`
	Currency          string       `json:"currency"`
	NetAssetsUSD      float64      `json:"net_assets_usd"`
	GrossExpenseRatio *float64     `json:"gross_expense_ratio_fraction"`
	Holdings          []ETFHolding `json:"holdings"`
	ProfileSource     ETFSource    `json:"profile_source"`
	HoldingsSource    ETFSource    `json:"holdings_source"`
}

func ValidateSPYETFFundamentals(f *ETFFundamentals, now time.Time) error {
	fail := func(message string) error { return fmt.Errorf("ETF fundamentals: %s", message) }
	if f == nil {
		return fail("unavailable")
	}
	if f.Contract != SPYETFContractV1 || f.Ticker != "SPY" || f.ISIN != "US78462F1030" || f.Currency != "USD" {
		return fail("identity mismatch")
	}
	for _, source := range []ETFSource{f.ProfileSource, f.HoldingsSource} {
		if source.FetchedAt.IsZero() || source.AsOf.IsZero() || source.FetchedAt.After(now) || source.AsOf.After(source.FetchedAt) || now.Sub(source.FetchedAt) > 24*time.Hour || now.Sub(source.AsOf) > 7*24*time.Hour {
			return fail("source missing, stale, or future dated")
		}
		if len(source.SHA256) != 64 {
			return fail("source digest missing")
		}
		for _, c := range source.SHA256 {
			if !strings.ContainsRune("0123456789abcdef", c) {
				return fail("source digest invalid")
			}
		}
	}
	if f.ProfileSource.URL != "https://www.ssga.com/library-content/products/fund-data/etfs/us/spdr-product-data-us-en.xlsx" || f.HoldingsSource.URL != "https://www.ssga.com/library-content/products/fund-data/etfs/us/holdings-daily-us-en-spy.xlsx" {
		return fail("source URL mismatch")
	}
	finite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
	if !finite(f.NetAssetsUSD) || f.NetAssetsUSD <= 0 {
		return fail("net assets unavailable")
	}
	if f.GrossExpenseRatio == nil || !finite(*f.GrossExpenseRatio) || *f.GrossExpenseRatio < 0 || *f.GrossExpenseRatio > 1 {
		return fail("gross expense ratio unavailable")
	}
	if len(f.Holdings) == 0 || len(f.Holdings) > 2000 {
		return fail("holdings unavailable or unbounded")
	}
	seen := map[string]bool{}
	total := 0.0
	for _, holding := range f.Holdings {
		if holding.Symbol == "" || holding.Symbol != strings.TrimSpace(strings.ToUpper(holding.Symbol)) || seen[holding.Symbol] || !finite(holding.Weight) || holding.Weight <= 0 || holding.Weight > 1 {
			return fail("holding identity or weight invalid")
		}
		seen[holding.Symbol] = true
		total += holding.Weight
	}
	if total < 0.95 || total > 1.005 {
		return fail("holdings coverage incomplete")
	}
	return nil
}
