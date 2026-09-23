package data

import (
	"math"
	"strings"
	"testing"
	"time"
)

func validETFFixture(now time.Time) *ETFFundamentals {
	fee := 0.000945
	source := ETFSource{SHA256: strings.Repeat("a", 64), AsOf: now.Add(-24 * time.Hour), FetchedAt: now}
	result := &ETFFundamentals{Contract: SPYETFContractV1, Ticker: "SPY", ISIN: "US78462F1030", Currency: "USD", NetAssetsUSD: 1e9, GrossExpenseRatio: &fee, Holdings: []ETFHolding{{Symbol: "AAA", Weight: 1}}, ProfileSource: source, HoldingsSource: source}
	result.ProfileSource.URL = "https://www.ssga.com/library-content/products/fund-data/etfs/us/spdr-product-data-us-en.xlsx"
	result.HoldingsSource.URL = "https://www.ssga.com/library-content/products/fund-data/etfs/us/holdings-daily-us-en-spy.xlsx"
	return result
}

func TestETFFundamentalsContractRejectsMissingStaleOrMismatchedEvidence(t *testing.T) {
	now := time.Now().UTC()
	if err := ValidateSPYETFFundamentals(validETFFixture(now), now); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*ETFFundamentals){
		"wrong ticker":        func(f *ETFFundamentals) { f.Ticker = "QQQ" },
		"wrong contract":      func(f *ETFFundamentals) { f.Contract = "other" },
		"missing source date": func(f *ETFFundamentals) { f.ProfileSource.AsOf = time.Time{} },
		"stale source":        func(f *ETFFundamentals) { f.HoldingsSource.AsOf = now.Add(-8 * 24 * time.Hour) },
		"stale retrieval":     func(f *ETFFundamentals) { f.ProfileSource.FetchedAt = now.Add(-25 * time.Hour) },
		"future":              func(f *ETFFundamentals) { f.HoldingsSource.FetchedAt = now.Add(time.Second) },
		"source after fetch":  func(f *ETFFundamentals) { f.HoldingsSource.AsOf = now.Add(time.Second) },
		"missing digest":      func(f *ETFFundamentals) { f.ProfileSource.SHA256 = "" },
		"invalid digest":      func(f *ETFFundamentals) { f.ProfileSource.SHA256 = strings.Repeat("z", 64) },
		"untrusted URL":       func(f *ETFFundamentals) { f.ProfileSource.URL = "https://example.org" },
		"missing assets":      func(f *ETFFundamentals) { f.NetAssetsUSD = 0 },
		"nonfinite assets":    func(f *ETFFundamentals) { f.NetAssetsUSD = math.NaN() },
		"missing expense":     func(f *ETFFundamentals) { f.GrossExpenseRatio = nil },
		"invalid expense":     func(f *ETFFundamentals) { *f.GrossExpenseRatio = math.Inf(1) },
		"no holdings":         func(f *ETFFundamentals) { f.Holdings = nil },
		"partial holdings":    func(f *ETFFundamentals) { f.Holdings[0].Weight = 0.9 },
		"excess holdings": func(f *ETFFundamentals) {
			f.Holdings = []ETFHolding{{Symbol: "AAA", Weight: 0.6}, {Symbol: "BBB", Weight: 0.5}}
		},
		"duplicate": func(f *ETFFundamentals) {
			f.Holdings = []ETFHolding{{Symbol: "AAA", Weight: 0.5}, {Symbol: "AAA", Weight: 0.5}}
		},
		"nonfinite weight": func(f *ETFFundamentals) { f.Holdings[0].Weight = math.NaN() },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			f := validETFFixture(now)
			edit(f)
			if err := ValidateSPYETFFundamentals(f, now); err == nil {
				t.Fatal("invalid evidence passed")
			}
		})
	}
	if err := ValidateSPYETFFundamentals(nil, now); err == nil {
		t.Fatal("nil passed")
	}
}
