package polymarket

import "time"

type Tick struct {
	Slug       string
	Side       string
	Price      float64
	Size       float64
	ReceivedAt time.Time
	SeqHint    uint64
	ConnID     int
}

type Level struct {
	Price float64
	Size  float64
}

type BookSnapshot struct {
	Slug       string
	BestBid    float64
	BestAsk    float64
	Bids       []Level
	Asks       []Level
	ReceivedAt time.Time
	ConnID     int
}
