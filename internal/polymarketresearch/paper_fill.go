package polymarketresearch

import (
	"math"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

type PaperFillStatus string

const (
	PaperFillStatusPosted    PaperFillStatus = "posted"
	PaperFillStatusFilled    PaperFillStatus = "filled"
	PaperFillStatusCancelled PaperFillStatus = "cancelled"
)

// PaperFillRequest describes a resting paper order.
type PaperFillRequest struct {
	Side         domain.OrderSide `json:"side"`
	LimitPrice   float64          `json:"limit_price"`
	Quantity     float64          `json:"quantity"`
	CreatedAt    time.Time        `json:"created_at"`
	RestingSince time.Time        `json:"resting_since,omitempty"`
}

// PaperFillConfig configures maker-first paper fill behavior.
type PaperFillConfig struct {
	AllowTaker bool          `json:"allow_taker"`
	StaleAfter time.Duration `json:"stale_after,omitempty"`
}

// PaperFillResult summarizes the fill simulation.
type PaperFillResult struct {
	Status         PaperFillStatus `json:"status"`
	Reasons        []string        `json:"reasons,omitempty"`
	FilledPrice    float64         `json:"filled_price,omitempty"`
	FilledQuantity float64         `json:"filled_quantity,omitempty"`
}

// SimulatePaperFill applies deterministic maker-first fill logic.
func SimulatePaperFill(now time.Time, req PaperFillRequest, snapshot domain.PolymarketBookSnapshot, cfg PaperFillConfig) PaperFillResult {
	if !isValidPaperFillRequest(req) {
		return PaperFillResult{Status: PaperFillStatusCancelled, Reasons: []string{"invalid_order"}}
	}
	if cfg.StaleAfter > 0 && !req.CreatedAt.IsZero() && now.Sub(req.CreatedAt) > cfg.StaleAfter {
		return PaperFillResult{Status: PaperFillStatusCancelled, Reasons: []string{"stale_order"}}
	}
	if !isFinitePrice(snapshot.BestBid) || !isFinitePrice(snapshot.BestAsk) || snapshot.BestAsk <= snapshot.BestBid {
		return PaperFillResult{Status: PaperFillStatusCancelled, Reasons: []string{"invalid_book"}}
	}

	wouldCross := orderWouldCross(req.Side, req.LimitPrice, snapshot.BestBid, snapshot.BestAsk)
	isResting := !req.RestingSince.IsZero()

	if !isResting && wouldCross && !cfg.AllowTaker {
		return PaperFillResult{Status: PaperFillStatusCancelled, Reasons: []string{"would_cross_spread"}}
	}
	if !isResting && wouldCross && cfg.AllowTaker {
		return PaperFillResult{Status: PaperFillStatusFilled, Reasons: []string{"taker_fill"}, FilledPrice: fillPrice(req.Side, req.LimitPrice, snapshot.BestBid, snapshot.BestAsk), FilledQuantity: req.Quantity}
	}

	if isResting && orderIsExecutable(req.Side, req.LimitPrice, snapshot.BestBid, snapshot.BestAsk) {
		return PaperFillResult{Status: PaperFillStatusFilled, Reasons: []string{"maker_fill"}, FilledPrice: fillPrice(req.Side, req.LimitPrice, snapshot.BestBid, snapshot.BestAsk), FilledQuantity: req.Quantity}
	}

	return PaperFillResult{Status: PaperFillStatusPosted, Reasons: []string{"posted"}}
}

func isValidPaperFillRequest(req PaperFillRequest) bool {
	return req.Side.IsValid() && isFinitePrice(req.LimitPrice) && isFinitePositive(req.Quantity)
}

func orderWouldCross(side domain.OrderSide, limitPrice, bestBid, bestAsk float64) bool {
	switch side {
	case domain.OrderSideBuy:
		return limitPrice >= bestAsk
	case domain.OrderSideSell:
		return limitPrice <= bestBid
	default:
		return false
	}
}

func orderIsExecutable(side domain.OrderSide, limitPrice, bestBid, bestAsk float64) bool {
	switch side {
	case domain.OrderSideBuy:
		return bestAsk <= limitPrice
	case domain.OrderSideSell:
		return bestBid >= limitPrice
	default:
		return false
	}
}

func fillPrice(side domain.OrderSide, limitPrice, bestBid, bestAsk float64) float64 {
	switch side {
	case domain.OrderSideBuy:
		if math.IsNaN(bestAsk) || math.IsInf(bestAsk, 0) || bestAsk <= 0 {
			return limitPrice
		}
		if bestAsk < limitPrice {
			return bestAsk
		}
		return limitPrice
	case domain.OrderSideSell:
		if math.IsNaN(bestBid) || math.IsInf(bestBid, 0) || bestBid <= 0 {
			return limitPrice
		}
		if bestBid > limitPrice {
			return bestBid
		}
		return limitPrice
	default:
		return limitPrice
	}
}
