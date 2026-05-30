package postgres

import (
	"math"
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestNormalizePolymarketTradeSide(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "yes lowercase", input: "yes", want: "YES"},
		{name: "no spaced", input: " no ", want: "NO"},
		{name: "up mixed", input: "uP", want: "Up"},
		{name: "down", input: "DOWN", want: "Down"},
		{name: "over", input: "over", want: "Over"},
		{name: "under", input: "Under", want: "Under"},
		{name: "invalid", input: "sideways", wantErr: true},
		{name: "display name in side field", input: "Team Spirit", wantErr: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := normalizePolymarketTradeSide(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("normalizePolymarketTradeSide(%q) error = nil, want error", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizePolymarketTradeSide(%q) error = %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("normalizePolymarketTradeSide(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestPolymarketAccountDerivedScores(t *testing.T) {
	t.Parallel()

	acc := &domain.PolymarketAccount{MarketsWon: 1, MarketsLost: 0}
	domain.EnrichPolymarketAccountScores(acc)
	if acc.ResolvedMarkets != 1 {
		t.Fatalf("ResolvedMarkets = %d, want 1", acc.ResolvedMarkets)
	}
	if diff := math.Abs(acc.BayesianWinRate - (4.0 / 7.0)); diff > 0.000001 {
		t.Fatalf("BayesianWinRate = %.6f, want %.6f", acc.BayesianWinRate, 4.0/7.0)
	}
	if acc.ConsistencyScore <= 0 || acc.ConsistencyScore >= acc.BayesianWinRate {
		t.Fatalf("ConsistencyScore = %.6f, want positive score scaled below bayesian rate for one resolved bet", acc.ConsistencyScore)
	}
}

func TestPolymarketAccountSortClause(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"":                  polymarketConsistencyScoreSQL() + " DESC",
		"consistency_score": polymarketConsistencyScoreSQL() + " DESC",
		"bayesian_win_rate": polymarketBayesianWinRateSQL() + " DESC",
		"resolved_markets":  "(markets_won + markets_lost) DESC",
		"last_active":       "last_active DESC NULLS LAST",
		"win_rate":          "win_rate DESC",
		"volume":            "total_volume DESC",
	}
	for input, want := range tests {
		if got := polymarketAccountSortClause(input); got != want {
			t.Fatalf("polymarketAccountSortClause(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestFilterSupportedPolymarketTrades(t *testing.T) {
	t.Parallel()

	trades := []domain.PolymarketAccountTrade{
		{AccountAddress: "0x1", MarketSlug: "btc", Side: "YES", Action: "buy"},
		{AccountAddress: "0x2", MarketSlug: "eth", Side: "Team Spirit", Action: "buy"},
		{AccountAddress: "0x3", MarketSlug: "sol", Side: "under", Action: "sell"},
	}

	filtered, skipped := filterSupportedPolymarketTrades(trades)
	if len(filtered) != 2 {
		t.Fatalf("filterSupportedPolymarketTrades() kept %d trades, want 2", len(filtered))
	}
	if filtered[0].Side != "YES" {
		t.Fatalf("first kept trade side = %q, want YES", filtered[0].Side)
	}
	if filtered[1].Side != "under" {
		t.Fatalf("second kept trade side = %q, want under", filtered[1].Side)
	}
	if len(skipped) != 1 {
		t.Fatalf("filterSupportedPolymarketTrades() skipped %d trades, want 1", len(skipped))
	}
	if skipped[0].Side != "Team Spirit" {
		t.Fatalf("skipped trade side = %q, want Team Spirit", skipped[0].Side)
	}
}
