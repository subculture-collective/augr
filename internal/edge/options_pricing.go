package edge

import "math"

// BlackScholesInput configures a European option valuation.
// TimeToExpiryYears is in years, volatility is annualized, and Greeks are
// returned in standard mathematical units: delta per $1 move in spot, gamma
// per $1^2, vega per 1.0 volatility point, theta per year, and rho per 1.0
// rate point.
type BlackScholesInput struct {
	Spot              float64
	Strike            float64
	Rate              float64
	DividendYield     float64
	Volatility        float64
	TimeToExpiryYears float64
}

// BlackScholesGreeks contains the sensitivities for a European option.
type BlackScholesGreeks struct {
	Delta float64
	Gamma float64
	Vega  float64
	Theta float64
	Rho   float64
}

// BlackScholesResult returns a price, Greeks, and an OK flag.
type BlackScholesResult struct {
	Price  float64
	Greeks BlackScholesGreeks
	OK     bool
}

// RealizedVolatilityResult returns an annualized standard deviation of log returns.
type RealizedVolatilityResult struct {
	Annualized float64
	OK         bool
}

// BlackScholesCall prices a European call option and computes Greeks.
func BlackScholesCall(in BlackScholesInput) BlackScholesResult {
	return blackScholes(in, true)
}

// BlackScholesPut prices a European put option and computes Greeks.
func BlackScholesPut(in BlackScholesInput) BlackScholesResult {
	return blackScholes(in, false)
}

// RealizedVolatility computes the annualized sample standard deviation of
// log returns. At least 3 valid prices are required so there are at least two
// returns to measure.
func RealizedVolatility(prices []float64, periodsPerYear float64) RealizedVolatilityResult {
	if !isFinitePositive(periodsPerYear) || len(prices) < 3 {
		return RealizedVolatilityResult{}
	}

	returns := make([]float64, 0, len(prices)-1)
	for i := 1; i < len(prices); i++ {
		prev := prices[i-1]
		curr := prices[i]
		if !isFinitePositive(prev) || !isFinitePositive(curr) {
			return RealizedVolatilityResult{}
		}
		returns = append(returns, math.Log(curr/prev))
	}

	if len(returns) < 2 {
		return RealizedVolatilityResult{}
	}

	mean := 0.0
	for _, r := range returns {
		mean += r
	}
	mean /= float64(len(returns))

	varSum := 0.0
	for _, r := range returns {
		d := r - mean
		varSum += d * d
	}
	stdDev := math.Sqrt(varSum / float64(len(returns)-1))
	annualized := stdDev * math.Sqrt(periodsPerYear)
	if !isFinite(annualized) {
		return RealizedVolatilityResult{}
	}
	return RealizedVolatilityResult{Annualized: annualized, OK: true}
}

func blackScholes(in BlackScholesInput, isCall bool) BlackScholesResult {
	if !isFinitePositive(in.Spot) || !isFinitePositive(in.Strike) || !isFinitePositive(in.Volatility) || !isFinitePositive(in.TimeToExpiryYears) || !isFinite(in.Rate) || !isFinite(in.DividendYield) {
		return BlackScholesResult{}
	}

	volSqrtT := in.Volatility * math.Sqrt(in.TimeToExpiryYears)
	if !isFinitePositive(volSqrtT) {
		return BlackScholesResult{}
	}

	logMoneyness := math.Log(in.Spot / in.Strike)
	if !isFinite(logMoneyness) {
		return BlackScholesResult{}
	}

	d1 := (logMoneyness + (in.Rate-in.DividendYield+0.5*in.Volatility*in.Volatility)*in.TimeToExpiryYears) / volSqrtT
	d2 := d1 - volSqrtT
	if !isFinite(d1) || !isFinite(d2) {
		return BlackScholesResult{}
	}

	spotDisc := in.Spot * math.Exp(-in.DividendYield*in.TimeToExpiryYears)
	strikeDisc := in.Strike * math.Exp(-in.Rate*in.TimeToExpiryYears)
	phi := normPDF(d1)
	if !isFinite(spotDisc) || !isFinite(strikeDisc) || !isFinite(phi) {
		return BlackScholesResult{}
	}

	var price, delta, theta, rho float64
	gamma := math.Exp(-in.DividendYield*in.TimeToExpiryYears) * phi / (in.Spot * in.Volatility * math.Sqrt(in.TimeToExpiryYears))
	vega := spotDisc * phi * math.Sqrt(in.TimeToExpiryYears)
	if !isFinite(gamma) || !isFinite(vega) {
		return BlackScholesResult{}
	}

	if isCall {
		nd1 := normCDF(d1)
		nd2 := normCDF(d2)
		price = spotDisc*nd1 - strikeDisc*nd2
		delta = math.Exp(-in.DividendYield*in.TimeToExpiryYears) * nd1
		theta = -spotDisc*phi*in.Volatility/(2*math.Sqrt(in.TimeToExpiryYears)) - in.Rate*strikeDisc*nd2 + in.DividendYield*spotDisc*nd1
		rho = in.Strike * in.TimeToExpiryYears * math.Exp(-in.Rate*in.TimeToExpiryYears) * nd2
	} else {
		nd1 := normCDF(-d1)
		nd2 := normCDF(-d2)
		price = strikeDisc*nd2 - spotDisc*nd1
		delta = math.Exp(-in.DividendYield*in.TimeToExpiryYears) * (normCDF(d1) - 1)
		theta = -spotDisc*phi*in.Volatility/(2*math.Sqrt(in.TimeToExpiryYears)) + in.Rate*strikeDisc*nd2 - in.DividendYield*spotDisc*nd1
		rho = -in.Strike * in.TimeToExpiryYears * math.Exp(-in.Rate*in.TimeToExpiryYears) * nd2
	}

	if !isFinite(price) || !isFinite(delta) || !isFinite(theta) || !isFinite(rho) {
		return BlackScholesResult{}
	}

	return BlackScholesResult{
		Price: price,
		Greeks: BlackScholesGreeks{
			Delta: delta,
			Gamma: gamma,
			Vega:  vega,
			Theta: theta,
			Rho:   rho,
		},
		OK: true,
	}
}

func normCDF(x float64) float64 {
	return 0.5 * math.Erfc(-x/math.Sqrt2)
}

func normPDF(x float64) float64 {
	return math.Exp(-0.5*x*x) / math.Sqrt(2*math.Pi)
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func isFinitePositive(v float64) bool {
	return isFinite(v) && v > 0
}
