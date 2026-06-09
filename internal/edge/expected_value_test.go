package edge

import "testing"

func TestBinaryNetEV(t *testing.T) {
	got := BinaryNetEV(BinaryEVInput{Probability: 0.62, Price: 0.55, Fee: 0.005, Slippage: 0.002, ExitHaircut: 0.003})
	if !almostEqual(got.GrossEV, 0.07, 1e-9) {
		t.Fatalf("GrossEV = %v, want %v", got.GrossEV, 0.07)
	}
	if !almostEqual(got.NetEV, 0.06, 1e-9) {
		t.Fatalf("NetEV = %v, want %v", got.NetEV, 0.06)
	}
	if !almostEqual(got.Edge, 0.07, 1e-9) {
		t.Fatalf("Edge = %v, want %v", got.Edge, 0.07)
	}
}

func TestOptionEdge(t *testing.T) {
	got := OptionEdge(OptionEdgeInput{ModelPrice: 2.40, ExecutablePrice: 2.10, Commission: 0.01, Slippage: 0.04, ModelHaircut: 0.05})
	if !almostEqual(got.GrossEdge, 0.30, 1e-9) {
		t.Fatalf("GrossEdge = %v, want %v", got.GrossEdge, 0.30)
	}
	if !almostEqual(got.NetEdge, 0.20, 1e-9) {
		t.Fatalf("NetEdge = %v, want %v", got.NetEdge, 0.20)
	}
}

func almostEqual(got, want, tol float64) bool {
	if got > want {
		return got-want <= tol
	}
	return want-got <= tol
}
