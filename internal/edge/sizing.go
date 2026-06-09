package edge

import "math"

// BinaryKellyInput configures fractional Kelly sizing.
type BinaryKellyInput struct {
	Probability float64
	Price       float64
	Fraction    float64
	Cap         float64
}

// FractionalKellyCap computes a fractional Kelly position cap.
func FractionalKellyCap(in BinaryKellyInput) float64 {
	if math.IsNaN(in.Probability) || math.IsInf(in.Probability, 0) || math.IsNaN(in.Price) || math.IsInf(in.Price, 0) || math.IsNaN(in.Fraction) || math.IsInf(in.Fraction, 0) || math.IsNaN(in.Cap) || math.IsInf(in.Cap, 0) {
		return 0
	}
	if in.Price <= 0 || in.Price >= 1 || in.Probability <= 0 || in.Probability >= 1 || in.Fraction <= 0 || in.Cap <= 0 {
		return 0
	}

	b := (1 / in.Price) - 1
	if b <= 0 {
		return 0
	}

	q := 1 - in.Probability
	full := (in.Probability*b - q) / b
	if full <= 0 {
		return 0
	}

	sized := full * in.Fraction
	if sized > in.Cap {
		return in.Cap
	}
	if sized < 0 || math.IsNaN(sized) || math.IsInf(sized, 0) {
		return 0
	}
	return sized
}
