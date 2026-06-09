package edge

import (
	"math"
	"testing"
)

func TestFractionalKellyCap(t *testing.T) {
	tests := []struct {
		name string
		in   BinaryKellyInput
		want float64
	}{
		{name: "cap negative", in: BinaryKellyInput{Probability: 0.6, Price: 0.5, Fraction: 0.5, Cap: -0.01}, want: 0},
		{name: "cap zero", in: BinaryKellyInput{Probability: 0.6, Price: 0.5, Fraction: 0.5, Cap: 0}, want: 0},
		{name: "fraction negative", in: BinaryKellyInput{Probability: 0.6, Price: 0.5, Fraction: -0.25, Cap: 1}, want: 0},
		{name: "fraction zero", in: BinaryKellyInput{Probability: 0.6, Price: 0.5, Fraction: 0, Cap: 1}, want: 0},
		{name: "no edge trade", in: BinaryKellyInput{Probability: 0.4, Price: 0.5, Fraction: 0.5, Cap: 1}, want: 0},
		{name: "uncapped positive kelly", in: BinaryKellyInput{Probability: 0.6, Price: 0.5, Fraction: 0.5, Cap: 1}, want: 0.1},
		{name: "invalid probability low", in: BinaryKellyInput{Probability: 0, Price: 0.5, Fraction: 0.5, Cap: 1}, want: 0},
		{name: "invalid probability high", in: BinaryKellyInput{Probability: 1, Price: 0.5, Fraction: 0.5, Cap: 1}, want: 0},
		{name: "invalid price low", in: BinaryKellyInput{Probability: 0.6, Price: 0, Fraction: 0.5, Cap: 1}, want: 0},
		{name: "invalid price high", in: BinaryKellyInput{Probability: 0.6, Price: 1, Fraction: 0.5, Cap: 1}, want: 0},
		{name: "nan inputs", in: BinaryKellyInput{Probability: math.NaN(), Price: 0.5, Fraction: 0.5, Cap: 1}, want: 0},
		{name: "inf inputs", in: BinaryKellyInput{Probability: 0.6, Price: math.Inf(1), Fraction: 0.5, Cap: 1}, want: 0},
		{name: "nan fraction", in: BinaryKellyInput{Probability: 0.6, Price: 0.5, Fraction: math.NaN(), Cap: 1}, want: 0},
		{name: "inf cap", in: BinaryKellyInput{Probability: 0.6, Price: 0.5, Fraction: 0.5, Cap: math.Inf(1)}, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FractionalKellyCap(tt.in)
			if got < 0 || math.IsNaN(got) || math.IsInf(got, 0) {
				t.Fatalf("FractionalKellyCap = %v, want finite non-negative", got)
			}
			if !almostEqual(got, tt.want, 1e-9) {
				t.Fatalf("FractionalKellyCap = %v, want %v", got, tt.want)
			}
		})
	}
}
