package polymarketresearch

import (
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestSimulatePaperFillPostOnlyDoesNotCrossWhenTakerDisabled(t *testing.T) {
	now := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	got := SimulatePaperFill(now, PaperFillRequest{
		Side:       domain.OrderSideBuy,
		LimitPrice: 0.60,
		Quantity:   10,
		CreatedAt:  now,
	}, domain.PolymarketBookSnapshot{BestBid: 0.50, BestAsk: 0.58}, PaperFillConfig{AllowTaker: false})

	if got.Status != PaperFillStatusCancelled {
		t.Fatalf("Status = %q, want cancelled", got.Status)
	}
	if !containsString(got.Reasons, "would_cross_spread") {
		t.Fatalf("Reasons = %v, want would_cross_spread", got.Reasons)
	}
}

func TestSimulatePaperFillCancelsStaleOrder(t *testing.T) {
	now := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	got := SimulatePaperFill(now, PaperFillRequest{
		Side:       domain.OrderSideBuy,
		LimitPrice: 0.54,
		Quantity:   10,
		CreatedAt:  now.Add(-2 * time.Hour),
	}, domain.PolymarketBookSnapshot{BestBid: 0.50, BestAsk: 0.56}, PaperFillConfig{AllowTaker: false, StaleAfter: time.Hour})

	if got.Status != PaperFillStatusCancelled {
		t.Fatalf("Status = %q, want cancelled", got.Status)
	}
	if !containsString(got.Reasons, "stale_order") {
		t.Fatalf("Reasons = %v, want stale_order", got.Reasons)
	}
}

func TestSimulatePaperFillMakerFillWhenMarketMovesFavorably(t *testing.T) {
	now := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	got := SimulatePaperFill(now, PaperFillRequest{
		Side:         domain.OrderSideBuy,
		LimitPrice:   0.56,
		Quantity:     10,
		CreatedAt:    now.Add(-10 * time.Minute),
		RestingSince: now.Add(-9 * time.Minute),
	}, domain.PolymarketBookSnapshot{BestBid: 0.50, BestAsk: 0.54}, PaperFillConfig{AllowTaker: false, StaleAfter: time.Hour})

	if got.Status != PaperFillStatusFilled {
		t.Fatalf("Status = %q, want filled", got.Status)
	}
	if !containsString(got.Reasons, "maker_fill") {
		t.Fatalf("Reasons = %v, want maker_fill", got.Reasons)
	}
	if !almostEqual(got.FilledPrice, 0.54, 1e-9) {
		t.Fatalf("FilledPrice = %v, want 0.54", got.FilledPrice)
	}
	if !almostEqual(got.FilledQuantity, 10, 1e-9) {
		t.Fatalf("FilledQuantity = %v, want 10", got.FilledQuantity)
	}
}
