package domain

import (
	"math"
	"time"
)

const (
	// PolymarketWinRatePriorWins/Losses are a conservative beta prior used to
	// avoid over-ranking accounts with tiny resolved samples, e.g. 1/1 winners.
	PolymarketWinRatePriorWins   = 3.0
	PolymarketWinRatePriorLosses = 3.0
)

// PolymarketWatchedMarket tracks a slug that should be monitored by the
// Polymarket signal source.
type PolymarketWatchedMarket struct {
	Slug    string    `json:"slug"`
	Enabled bool      `json:"enabled"`
	AddedAt time.Time `json:"added_at"`
	AddedBy string    `json:"added_by,omitempty"`
	Note    string    `json:"note,omitempty"`
}

// PolymarketAccount profiles a known Polymarket trader by wallet address.
type PolymarketAccount struct {
	Address                       string         `json:"address"`
	DisplayName                   string         `json:"display_name,omitempty"`
	FirstSeen                     time.Time      `json:"first_seen"`
	LastActive                    *time.Time     `json:"last_active,omitempty"`
	TotalTrades                   int            `json:"total_trades"`
	TotalVolume                   float64        `json:"total_volume"`
	MarketsEntered                int            `json:"markets_entered"`
	MarketsWon                    int            `json:"markets_won"`
	MarketsLost                   int            `json:"markets_lost"`
	WinRate                       float64        `json:"win_rate"`
	ResolvedMarkets               int            `json:"resolved_markets"`
	BayesianWinRate               float64        `json:"bayesian_win_rate"`
	ConsistencyScore              float64        `json:"consistency_score"`
	CategoryStats                 map[string]any `json:"category_stats,omitempty"`
	AvgPosition                   float64        `json:"avg_position"`
	MaxPosition                   float64        `json:"max_position"`
	AvgEntryHoursBeforeResolution float64        `json:"avg_entry_hours_before_resolution,omitempty"`
	EarlyEntryRate                float64        `json:"early_entry_rate"`
	Tags                          []string       `json:"tags,omitempty"`
	Tracked                       bool           `json:"tracked"`
	UpdatedAt                     time.Time      `json:"updated_at"`
}

// EnrichPolymarketAccountScores fills derived confidence-adjusted account
// metrics from persisted raw win/loss counts.
func EnrichPolymarketAccountScores(acc *PolymarketAccount) {
	if acc == nil {
		return
	}
	acc.ResolvedMarkets = acc.MarketsWon + acc.MarketsLost
	acc.BayesianWinRate = PolymarketBayesianWinRate(acc.MarketsWon, acc.MarketsLost)
	acc.ConsistencyScore = PolymarketConsistencyScore(acc.MarketsWon, acc.MarketsLost)
}

func PolymarketBayesianWinRate(won, lost int) float64 {
	resolved := won + lost
	return (float64(won) + PolymarketWinRatePriorWins) /
		(float64(resolved) + PolymarketWinRatePriorWins + PolymarketWinRatePriorLosses)
}

func PolymarketConsistencyScore(won, lost int) float64 {
	resolved := won + lost
	if resolved <= 0 {
		return 0
	}
	return PolymarketBayesianWinRate(won, lost) * math.Log10(float64(resolved)+1)
}

// PolymarketAccountTrade records a single trade by a known account.
type PolymarketAccountTrade struct {
	ID             string    `json:"id"`
	AccountAddress string    `json:"account_address"`
	MarketSlug     string    `json:"market_slug"`
	Side           string    `json:"side"`   // "YES" or "NO"
	Action         string    `json:"action"` // "buy" or "sell"
	Price          float64   `json:"price"`
	SizeUSDC       float64   `json:"size_usdc"`
	Timestamp      time.Time `json:"timestamp"`
	Outcome        string    `json:"outcome,omitempty"`
	PnL            *float64  `json:"pnl,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}
