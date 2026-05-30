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
	Slug       string
	BestBid    float64
	BestAsk    float64
	Bids       []PolymarketBookLevel
	Asks       []PolymarketBookLevel
	ReceivedAt time.Time
	ConnID     int
}
