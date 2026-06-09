package edge

import (
	"math"
	"testing"
)

func TestBlackScholesCallPut(t *testing.T) {
	in := BlackScholesInput{
		Spot:              100,
		Strike:            100,
		Rate:              0.05,
		DividendYield:     0,
		Volatility:        0.20,
		TimeToExpiryYears: 1,
	}

	call := BlackScholesCall(in)
	if !call.OK {
		t.Fatal("BlackScholesCall() OK = false, want true")
	}
	if !almostEqual(call.Price, 10.4506, 1e-3) {
		t.Fatalf("call.Price = %v, want about %v", call.Price, 10.4506)
	}
	if !almostEqual(call.Greeks.Delta, 0.6368, 1e-3) {
		t.Fatalf("call.Delta = %v, want about %v", call.Greeks.Delta, 0.6368)
	}
	if !almostEqual(call.Greeks.Gamma, 0.01876, 1e-4) {
		t.Fatalf("call.Gamma = %v, want about %v", call.Greeks.Gamma, 0.01876)
	}
	if !almostEqual(call.Greeks.Vega, 37.524, 1e-3) {
		t.Fatalf("call.Vega = %v, want about %v", call.Greeks.Vega, 37.524)
	}
	if call.Greeks.Theta >= 0 {
		t.Fatalf("call.Theta = %v, want negative per year", call.Greeks.Theta)
	}
	if call.Greeks.Rho <= 0 {
		t.Fatalf("call.Rho = %v, want positive", call.Greeks.Rho)
	}

	put := BlackScholesPut(in)
	if !put.OK {
		t.Fatal("BlackScholesPut() OK = false, want true")
	}
	if !almostEqual(put.Price, 5.5735, 1e-3) {
		t.Fatalf("put.Price = %v, want about %v", put.Price, 5.5735)
	}
	if !almostEqual(put.Greeks.Delta, -0.3632, 1e-3) {
		t.Fatalf("put.Delta = %v, want about %v", put.Greeks.Delta, -0.3632)
	}
	if !almostEqual(put.Greeks.Gamma, call.Greeks.Gamma, 1e-9) {
		t.Fatalf("put.Gamma = %v, want call gamma %v", put.Greeks.Gamma, call.Greeks.Gamma)
	}
	if put.Greeks.Rho >= 0 {
		t.Fatalf("put.Rho = %v, want negative", put.Greeks.Rho)
	}
}

func TestBlackScholesRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name string
		in   BlackScholesInput
	}{
		{name: "zero spot", in: BlackScholesInput{Spot: 0, Strike: 100, Rate: 0.05, DividendYield: 0, Volatility: 0.2, TimeToExpiryYears: 1}},
		{name: "negative spot", in: BlackScholesInput{Spot: -1, Strike: 100, Rate: 0.05, DividendYield: 0, Volatility: 0.2, TimeToExpiryYears: 1}},
		{name: "zero strike", in: BlackScholesInput{Spot: 100, Strike: 0, Rate: 0.05, DividendYield: 0, Volatility: 0.2, TimeToExpiryYears: 1}},
		{name: "negative strike", in: BlackScholesInput{Spot: 100, Strike: -1, Rate: 0.05, DividendYield: 0, Volatility: 0.2, TimeToExpiryYears: 1}},
		{name: "zero vol", in: BlackScholesInput{Spot: 100, Strike: 100, Rate: 0.05, DividendYield: 0, Volatility: 0, TimeToExpiryYears: 1}},
		{name: "negative vol", in: BlackScholesInput{Spot: 100, Strike: 100, Rate: 0.05, DividendYield: 0, Volatility: -0.1, TimeToExpiryYears: 1}},
		{name: "zero time", in: BlackScholesInput{Spot: 100, Strike: 100, Rate: 0.05, DividendYield: 0, Volatility: 0.2, TimeToExpiryYears: 0}},
		{name: "negative time", in: BlackScholesInput{Spot: 100, Strike: 100, Rate: 0.05, DividendYield: 0, Volatility: 0.2, TimeToExpiryYears: -1}},
		{name: "nan inputs", in: BlackScholesInput{Spot: math.NaN(), Strike: 100, Rate: 0.05, DividendYield: 0, Volatility: 0.2, TimeToExpiryYears: 1}},
		{name: "inf inputs", in: BlackScholesInput{Spot: 100, Strike: math.Inf(1), Rate: 0.05, DividendYield: 0, Volatility: 0.2, TimeToExpiryYears: 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name+" call", func(t *testing.T) {
			got := BlackScholesCall(tt.in)
			if got.OK {
				t.Fatal("BlackScholesCall() OK = true, want false")
			}
			if got.Price != 0 || got.Greeks != (BlackScholesGreeks{}) {
				t.Fatalf("BlackScholesCall() = %+v, want zero result", got)
			}
		})
		t.Run(tt.name+" put", func(t *testing.T) {
			got := BlackScholesPut(tt.in)
			if got.OK {
				t.Fatal("BlackScholesPut() OK = true, want false")
			}
			if got.Price != 0 || got.Greeks != (BlackScholesGreeks{}) {
				t.Fatalf("BlackScholesPut() = %+v, want zero result", got)
			}
		})
	}
}

func TestRealizedVolatility(t *testing.T) {
	prices := []float64{100, 110, 105, 115}
	got := RealizedVolatility(prices, 252)
	if !got.OK {
		t.Fatal("RealizedVolatility() OK = false, want true")
	}

	returns := []float64{math.Log(110.0 / 100.0), math.Log(105.0 / 110.0), math.Log(115.0 / 105.0)}
	mean := (returns[0] + returns[1] + returns[2]) / 3
	varSum := 0.0
	for _, r := range returns {
		d := r - mean
		varSum += d * d
	}
	want := math.Sqrt(varSum/2) * math.Sqrt(252)
	if !almostEqual(got.Annualized, want, 1e-12) {
		t.Fatalf("RealizedVolatility() = %v, want %v", got.Annualized, want)
	}
}

func TestRealizedVolatilityRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name   string
		prices []float64
		ppy    float64
	}{
		{name: "too few prices", prices: []float64{100, 101}, ppy: 252},
		{name: "zero periods", prices: []float64{100, 101, 102}, ppy: 0},
		{name: "negative periods", prices: []float64{100, 101, 102}, ppy: -1},
		{name: "zero price", prices: []float64{100, 0, 102}, ppy: 252},
		{name: "negative price", prices: []float64{100, -1, 102}, ppy: 252},
		{name: "nan price", prices: []float64{100, math.NaN(), 102}, ppy: 252},
		{name: "inf price", prices: []float64{100, math.Inf(1), 102}, ppy: 252},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RealizedVolatility(tt.prices, tt.ppy)
			if got.OK {
				t.Fatal("RealizedVolatility() OK = true, want false")
			}
			if got.Annualized != 0 {
				t.Fatalf("RealizedVolatility() = %+v, want zero result", got)
			}
		})
	}
}
