package domain

import "time"

type PolymarketTick struct {
	Slug       string
	Side       string
	Price      float64
	Size       float64
	ReceivedAt time.Time
	SeqHint    int64
	ConnID     int
}

type PolymarketBookLevel struct {
	Price float64
	Size  float64
}

type PolymarketBookSnapshot struct {
	Slug        string
	TokenID     string `json:"token_id,omitempty"`
	Outcome     string `json:"outcome,omitempty"`
	BestBid     float64
	BestAsk     float64
	Midpoint    float64 `json:"midpoint,omitempty"`
	Spread      float64 `json:"spread,omitempty"`
	BidDepthUSD float64 `json:"bid_depth_usd,omitempty"`
	AskDepthUSD float64 `json:"ask_depth_usd,omitempty"`
	DepthUSD    float64 `json:"depth_usd,omitempty"`
	Bids        []PolymarketBookLevel
	Asks        []PolymarketBookLevel
	ReceivedAt  time.Time
	ConnID      int
}
