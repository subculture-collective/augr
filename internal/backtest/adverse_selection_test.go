package backtest

import (
	"math"
	"testing"
)

func TestAdverseModel_BuyMovesDown(t *testing.T) {
	t.Parallel()
	const n = 5000
	model := NewAdverseModel(DefaultAdverseSelectionConfig(), 1)
	var buySum, sellSum float64
	for i := 0; i < n; i++ {
		buySum += model.AdverseMove(FillBuy)
	}
	model = NewAdverseModel(DefaultAdverseSelectionConfig(), 1)
	for i := 0; i < n; i++ {
		sellSum += model.AdverseMove(FillSell)
	}
	buyMean := buySum / n
	sellMean := sellSum / n
	if math.Abs(buyMean-(-DefaultAdverseSelectionConfig().BiasBps)) > DefaultAdverseSelectionConfig().BiasBps*0.2 {
		t.Fatalf("buy mean %v outside tolerance", buyMean)
	}
	if math.Abs(sellMean-DefaultAdverseSelectionConfig().BiasBps) > DefaultAdverseSelectionConfig().BiasBps*0.2 {
		t.Fatalf("sell mean %v outside tolerance", sellMean)
	}
	if buyMean >= 0 || sellMean <= 0 {
		t.Fatalf("unexpected signs buy=%v sell=%v", buyMean, sellMean)
	}
}

func TestAdverseModel_GhostRateApproximatesConfig(t *testing.T) {
	t.Parallel()
	cfg := DefaultAdverseSelectionConfig()
	cfg.GhostFillRate = 0.02
	model := NewAdverseModel(cfg, 2)
	const n = 50000
	ghosts := 0
	for i := 0; i < n; i++ {
		if model.IsGhost() {
			ghosts++
		}
	}
	rate := float64(ghosts) / n
	if math.Abs(rate-cfg.GhostFillRate) > cfg.GhostFillRate*0.2 {
		t.Fatalf("rate %v outside tolerance", rate)
	}
}

func TestAdverseModel_Deterministic(t *testing.T) {
	t.Parallel()
	cfg := DefaultAdverseSelectionConfig()
	a := NewAdverseModel(cfg, 99)
	b := NewAdverseModel(cfg, 99)
	for i := 0; i < 1000; i++ {
		if gotA, gotB := a.AdverseMove(FillBuy), b.AdverseMove(FillBuy); gotA != gotB {
			t.Fatalf("sequence diverged at %d: %v vs %v", i, gotA, gotB)
		}
		if gotA, gotB := a.IsGhost(), b.IsGhost(); gotA != gotB {
			t.Fatalf("ghost sequence diverged at %d: %v vs %v", i, gotA, gotB)
		}
	}
}
