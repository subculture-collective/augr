package optionsresearch

import (
	"math"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestScannerAcceptsLongCallWithRealizedVolFallback(t *testing.T) {
	now := time.Date(2026, 1, 2, 15, 0, 0, 0, time.UTC)
	expiry := now.AddDate(0, 0, 90)
	contract := domain.OptionContract{
		OCCSymbol:  domain.FormatOCC("AAPL", domain.OptionTypeCall, 90, expiry),
		Underlying: "AAPL",
		OptionType: domain.OptionTypeCall,
		Strike:     90,
		Expiry:     expiry,
		Multiplier: 100,
		Style:      "european",
	}
	snap := domain.OptionSnapshot{
		Contract:     contract,
		Greeks:       domain.OptionGreeks{IV: 0},
		Bid:          10.10,
		Ask:          10.50,
		Mid:          10.30,
		Last:         10.35,
		Volume:       120,
		OpenInterest: 800,
	}
	sc := NewScanner(Config{
		MinOpenInterest:       100,
		MinVolume:             50,
		MaxBidAskSpread:       1.0,
		MinNetEdge:            0.05,
		MaxThetaExposure:      10_000,
		MaxUnderlyingExposure: 1_000_000,
		Bankroll:              10_000,
		FractionalKellyCap:    0.25,
		RiskFreeRate:          0.03,
		DividendYield:         0,
		PeriodsPerYear:        252,
	})

	res := sc.Scan(Input{
		Now:              now,
		UnderlyingPrice:  100,
		UnderlyingPrices: []float64{100, 105, 101, 108},
		Spread: domain.OptionSpread{
			StrategyType: domain.StrategyLongCall,
			Underlying:   "AAPL",
			Legs: []domain.SpreadLeg{{
				Contract:       contract,
				Side:           domain.OrderSideBuy,
				PositionIntent: domain.PositionIntentBuyToOpen,
				Ratio:          1,
				Quantity:       1,
			}},
		},
		Chain: []domain.OptionSnapshot{snap},
	})

	if !res.Accepted {
		t.Fatalf("Scan() accepted = false, reasons=%v", res.Reasons)
	}
	if res.Decision.MarketType != domain.MarketTypeOptions {
		t.Fatalf("MarketType = %q, want %q", res.Decision.MarketType, domain.MarketTypeOptions)
	}
	if res.Decision.Status != domain.TradeDecisionStatusCandidate {
		t.Fatalf("Status = %q, want %q", res.Decision.Status, domain.TradeDecisionStatusCandidate)
	}
	if res.Decision.RiskStatus != domain.RiskDecisionApproved {
		t.Fatalf("RiskStatus = %q, want %q", res.Decision.RiskStatus, domain.RiskDecisionApproved)
	}
	if res.Decision.ApprovedSize <= 0 {
		t.Fatalf("ApprovedSize = %v, want > 0", res.Decision.ApprovedSize)
	}
	assertFiniteTradeDecision(t, res.Decision)
	if res.ModelPrice <= 0 || res.ExecutablePrice <= 0 || res.GrossEdge <= 0 || res.NetEdge <= 0 {
		t.Fatalf("expected positive pricing metrics, got %+v", res)
	}
	if res.Decision.Side != domain.OrderSideBuy {
		t.Fatalf("Side = %q, want %q", res.Decision.Side, domain.OrderSideBuy)
	}
}

func TestScannerAcceptsBalancedDebitVerticals(t *testing.T) {
	now := time.Date(2026, 2, 1, 15, 0, 0, 0, time.UTC)
	expiry := now.AddDate(0, 0, 60)
	callLow := domain.OptionContract{OCCSymbol: domain.FormatOCC("AAPL", domain.OptionTypeCall, 90, expiry), Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 90, Expiry: expiry, Multiplier: 100, Style: "european"}
	callHigh := domain.OptionContract{OCCSymbol: domain.FormatOCC("AAPL", domain.OptionTypeCall, 100, expiry), Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 100, Expiry: expiry, Multiplier: 100, Style: "european"}
	putHigh := domain.OptionContract{OCCSymbol: domain.FormatOCC("AAPL", domain.OptionTypePut, 100, expiry), Underlying: "AAPL", OptionType: domain.OptionTypePut, Strike: 100, Expiry: expiry, Multiplier: 100, Style: "european"}
	putLow := domain.OptionContract{OCCSymbol: domain.FormatOCC("AAPL", domain.OptionTypePut, 90, expiry), Underlying: "AAPL", OptionType: domain.OptionTypePut, Strike: 90, Expiry: expiry, Multiplier: 100, Style: "european"}
	chain := []domain.OptionSnapshot{
		{Contract: callLow, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 9.80, Ask: 10.20, Mid: 10.00, Volume: 500, OpenInterest: 2000},
		{Contract: callHigh, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 4.00, Ask: 4.50, Mid: 4.25, Volume: 450, OpenInterest: 1800},
		{Contract: putHigh, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 4.10, Ask: 4.60, Mid: 4.35, Volume: 450, OpenInterest: 1800},
		{Contract: putLow, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 1.90, Ask: 2.20, Mid: 2.05, Volume: 500, OpenInterest: 2000},
	}
	sc := NewScanner(Config{
		MinOpenInterest:       100,
		MinVolume:             50,
		MaxBidAskSpread:       1.0,
		MinNetEdge:            0.05,
		MaxThetaExposure:      50_000,
		MaxUnderlyingExposure: 50_000,
		Bankroll:              5_000,
		FractionalKellyCap:    0.20,
		RiskFreeRate:          0.02,
		DividendYield:         0,
		PeriodsPerYear:        252,
	})

	t.Run("bull call spread", func(t *testing.T) {
		res := sc.Scan(Input{
			Now:             now,
			UnderlyingPrice: 100,
			Spread: domain.OptionSpread{
				StrategyType: domain.StrategyBullCallSpread,
				Underlying:   "AAPL",
				Legs: []domain.SpreadLeg{
					{Contract: callLow, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToOpen, Ratio: 1, Quantity: 1},
					{Contract: callHigh, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToOpen, Ratio: 1, Quantity: 1},
				},
			},
			Chain: chain[:2],
		})
		if !res.Accepted {
			t.Fatalf("bull call accepted = false, reasons=%v", res.Reasons)
		}
		assertFiniteTradeDecision(t, res.Decision)
	})

	t.Run("bear put spread", func(t *testing.T) {
		res := sc.Scan(Input{
			Now:             now,
			UnderlyingPrice: 100,
			Spread: domain.OptionSpread{
				StrategyType: domain.StrategyBearPutSpread,
				Underlying:   "AAPL",
				Legs: []domain.SpreadLeg{
					{Contract: putHigh, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToOpen, Ratio: 1, Quantity: 1},
					{Contract: putLow, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToOpen, Ratio: 1, Quantity: 1},
				},
			},
			Chain: chain[2:],
		})
		if !res.Accepted {
			t.Fatalf("bear put accepted = false, reasons=%v", res.Reasons)
		}
		assertFiniteTradeDecision(t, res.Decision)
	})
}

func TestScannerUsesAskAndBidExecutablePricing(t *testing.T) {
	now := time.Date(2026, 2, 1, 15, 0, 0, 0, time.UTC)
	expiry := now.AddDate(0, 0, 60)
	long := domain.OptionContract{OCCSymbol: domain.FormatOCC("AAPL", domain.OptionTypeCall, 90, expiry), Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 90, Expiry: expiry, Multiplier: 100, Style: "european"}
	short := domain.OptionContract{OCCSymbol: domain.FormatOCC("AAPL", domain.OptionTypeCall, 100, expiry), Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 100, Expiry: expiry, Multiplier: 100, Style: "european"}
	chain := []domain.OptionSnapshot{
		{Contract: long, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 9.80, Ask: 10.20, Mid: 10.00, Volume: 500, OpenInterest: 2000},
		{Contract: short, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 4.00, Ask: 4.50, Mid: 4.25, Volume: 450, OpenInterest: 1800},
	}
	sc := NewScanner(Config{
		MinOpenInterest:       100,
		MinVolume:             50,
		MaxBidAskSpread:       1.0,
		MinNetEdge:            0.05,
		MaxThetaExposure:      50_000,
		MaxUnderlyingExposure: 50_000,
		Bankroll:              5_000,
		FractionalKellyCap:    0.20,
		RiskFreeRate:          0.02,
		DividendYield:         0,
		PeriodsPerYear:        252,
	})

	res := sc.Scan(Input{
		Now:             now,
		UnderlyingPrice: 100,
		Spread: domain.OptionSpread{
			StrategyType: domain.StrategyBullCallSpread,
			Underlying:   "AAPL",
			Legs: []domain.SpreadLeg{
				{Contract: long, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToOpen, Ratio: 1, Quantity: 1},
				{Contract: short, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToOpen, Ratio: 1, Quantity: 1},
			},
		},
		Chain: chain,
	})

	if !res.Accepted {
		t.Fatalf("Scan() accepted = false, reasons=%v", res.Reasons)
	}
	if !almostEqual(res.ExecutablePrice, 6.20, 1e-9) {
		t.Fatalf("ExecutablePrice = %v, want %v", res.ExecutablePrice, 6.20)
	}
	if !almostEqual(res.Decision.ExecutablePrice, 6.20, 1e-9) {
		t.Fatalf("Decision.ExecutablePrice = %v, want %v", res.Decision.ExecutablePrice, 6.20)
	}
	assertFiniteTradeDecision(t, res.Decision)
}

func TestScannerRejectsUnbalancedDebitVerticalAndMalformedSellToOpenShapes(t *testing.T) {
	now := time.Date(2026, 2, 10, 15, 0, 0, 0, time.UTC)
	expiry := now.AddDate(0, 0, 60)
	long := domain.OptionContract{OCCSymbol: domain.FormatOCC("AAPL", domain.OptionTypeCall, 90, expiry), Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 90, Expiry: expiry, Multiplier: 100, Style: "european"}
	short := domain.OptionContract{OCCSymbol: domain.FormatOCC("AAPL", domain.OptionTypeCall, 100, expiry), Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 100, Expiry: expiry, Multiplier: 100, Style: "european"}
	put := domain.OptionContract{OCCSymbol: domain.FormatOCC("AAPL", domain.OptionTypePut, 95, expiry), Underlying: "AAPL", OptionType: domain.OptionTypePut, Strike: 95, Expiry: expiry, Multiplier: 100, Style: "european"}
	baseChain := []domain.OptionSnapshot{
		{Contract: long, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 9.80, Ask: 10.20, Mid: 10.00, Volume: 500, OpenInterest: 2000},
		{Contract: short, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 4.00, Ask: 4.50, Mid: 4.25, Volume: 450, OpenInterest: 1800},
		{Contract: put, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 2.00, Ask: 2.30, Mid: 2.15, Volume: 300, OpenInterest: 1400},
	}
	sc := NewScanner(Config{MinOpenInterest: 100, MinVolume: 50, MaxBidAskSpread: 1.0, MinNetEdge: 0.05, MaxThetaExposure: 50_000, MaxUnderlyingExposure: 50_000, Bankroll: 5_000, FractionalKellyCap: 0.20, RiskFreeRate: 0.02, DividendYield: 0, PeriodsPerYear: 252})

	t.Run("unbalanced vertical quantity", func(t *testing.T) {
		res := sc.Scan(Input{
			Now:             now,
			UnderlyingPrice: 100,
			Spread: domain.OptionSpread{
				StrategyType: domain.StrategyBullCallSpread,
				Underlying:   "AAPL",
				Legs: []domain.SpreadLeg{
					{Contract: long, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToOpen, Ratio: 1, Quantity: 1},
					{Contract: short, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToOpen, Ratio: 1, Quantity: 2},
				},
			},
			Chain: baseChain[:2],
		})
		if res.Accepted {
			t.Fatal("unbalanced vertical accepted, want rejection")
		}
		if len(res.Reasons) == 0 || res.Reasons[0] != "undefined_risk_rejected" {
			t.Fatalf("unbalanced vertical reasons = %v, want undefined_risk_rejected", res.Reasons)
		}
		if res.Decision.ApprovedSize != 0 {
			t.Fatalf("unbalanced vertical ApprovedSize = %v, want 0", res.Decision.ApprovedSize)
		}
		assertFiniteTradeDecision(t, res.Decision)
	})

	t.Run("mismatched option type with sell leg", func(t *testing.T) {
		res := sc.Scan(Input{
			Now:             now,
			UnderlyingPrice: 100,
			Spread: domain.OptionSpread{
				StrategyType: domain.StrategyBullCallSpread,
				Underlying:   "AAPL",
				Legs: []domain.SpreadLeg{
					{Contract: long, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToOpen, Ratio: 1, Quantity: 1},
					{Contract: put, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToOpen, Ratio: 1, Quantity: 1},
				},
			},
			Chain: baseChain,
		})
		if res.Accepted {
			t.Fatal("mismatched option type accepted, want rejection")
		}
		if len(res.Reasons) == 0 || res.Reasons[0] != "undefined_risk_rejected" {
			t.Fatalf("mismatched option type reasons = %v, want undefined_risk_rejected", res.Reasons)
		}
		assertFiniteTradeDecision(t, res.Decision)
	})

	t.Run("mismatched underlying or expiry with sell leg", func(t *testing.T) {
		otherUnderlying := domain.OptionContract{OCCSymbol: domain.FormatOCC("MSFT", domain.OptionTypeCall, 100, expiry), Underlying: "MSFT", OptionType: domain.OptionTypeCall, Strike: 100, Expiry: expiry, Multiplier: 100, Style: "european"}
		otherExpiry := domain.OptionContract{OCCSymbol: domain.FormatOCC("AAPL", domain.OptionTypeCall, 100, now.AddDate(0, 0, 90)), Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 100, Expiry: now.AddDate(0, 0, 90), Multiplier: 100, Style: "european"}

		cases := []struct {
			name  string
			short domain.OptionContract
			chain []domain.OptionSnapshot
		}{
			{
				name:  "underlying mismatch",
				short: otherUnderlying,
				chain: []domain.OptionSnapshot{
					{Contract: long, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 9.80, Ask: 10.20, Volume: 500, OpenInterest: 2000},
					{Contract: otherUnderlying, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 4.00, Ask: 4.50, Volume: 450, OpenInterest: 1800},
				},
			},
			{
				name:  "expiry mismatch",
				short: otherExpiry,
				chain: []domain.OptionSnapshot{
					{Contract: long, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 9.80, Ask: 10.20, Volume: 500, OpenInterest: 2000},
					{Contract: otherExpiry, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 4.00, Ask: 4.50, Volume: 450, OpenInterest: 1800},
				},
			},
		}

		for _, tt := range cases {
			tt := tt
			t.Run(tt.name, func(t *testing.T) {
				res := sc.Scan(Input{
					Now:             now,
					UnderlyingPrice: 100,
					Spread: domain.OptionSpread{
						StrategyType: domain.StrategyBullCallSpread,
						Underlying:   "AAPL",
						Legs: []domain.SpreadLeg{
							{Contract: long, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToOpen, Ratio: 1, Quantity: 1},
							{Contract: tt.short, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToOpen, Ratio: 1, Quantity: 1},
						},
					},
					Chain: tt.chain,
				})
				if res.Accepted {
					t.Fatal("mismatched vertical accepted, want rejection")
				}
				if len(res.Reasons) == 0 || res.Reasons[0] != "undefined_risk_rejected" {
					t.Fatalf("mismatched vertical reasons = %v, want undefined_risk_rejected", res.Reasons)
				}
				assertFiniteTradeDecision(t, res.Decision)
			})
		}
	})

	t.Run("single-leg shape with extra sell leg", func(t *testing.T) {
		res := sc.Scan(Input{
			Now:             now,
			UnderlyingPrice: 100,
			Spread: domain.OptionSpread{
				StrategyType: domain.StrategyLongCall,
				Underlying:   "AAPL",
				Legs: []domain.SpreadLeg{
					{Contract: long, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToOpen, Ratio: 1, Quantity: 1},
					{Contract: put, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToOpen, Ratio: 1, Quantity: 1},
				},
			},
			Chain: baseChain,
		})
		if res.Accepted {
			t.Fatal("malformed single-leg shape accepted, want rejection")
		}
		if len(res.Reasons) == 0 || res.Reasons[0] != "undefined_risk_rejected" {
			t.Fatalf("malformed single-leg reasons = %v, want undefined_risk_rejected", res.Reasons)
		}
		if res.Decision.ApprovedSize != 0 {
			t.Fatalf("malformed single-leg ApprovedSize = %v, want 0", res.Decision.ApprovedSize)
		}
		assertFiniteTradeDecision(t, res.Decision)
	})

	t.Run("vertical wrong leg count with sell leg", func(t *testing.T) {
		res := sc.Scan(Input{
			Now:             now,
			UnderlyingPrice: 100,
			Spread: domain.OptionSpread{
				StrategyType: domain.StrategyBullCallSpread,
				Underlying:   "AAPL",
				Legs: []domain.SpreadLeg{
					{Contract: long, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToOpen, Ratio: 1, Quantity: 1},
					{Contract: short, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToOpen, Ratio: 1, Quantity: 1},
					{Contract: put, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToOpen, Ratio: 1, Quantity: 1},
				},
			},
			Chain: baseChain,
		})
		if res.Accepted {
			t.Fatal("malformed vertical accepted, want rejection")
		}
		if len(res.Reasons) == 0 || res.Reasons[0] != "undefined_risk_rejected" {
			t.Fatalf("malformed vertical reasons = %v, want undefined_risk_rejected", res.Reasons)
		}
		if res.Decision.ApprovedSize != 0 {
			t.Fatalf("malformed vertical ApprovedSize = %v, want 0", res.Decision.ApprovedSize)
		}
		assertFiniteTradeDecision(t, res.Decision)
	})
}

func TestScannerRejectsMultiLegIlliquidityPerContract(t *testing.T) {
	now := time.Date(2026, 2, 15, 15, 0, 0, 0, time.UTC)
	expiry := now.AddDate(0, 0, 45)
	long := domain.OptionContract{OCCSymbol: domain.FormatOCC("AAPL", domain.OptionTypeCall, 90, expiry), Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 90, Expiry: expiry, Multiplier: 100, Style: "european"}
	short := domain.OptionContract{OCCSymbol: domain.FormatOCC("AAPL", domain.OptionTypeCall, 100, expiry), Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 100, Expiry: expiry, Multiplier: 100, Style: "european"}
	sc := NewScanner(Config{MinOpenInterest: 100, MinVolume: 50, MaxBidAskSpread: 1.0, MinNetEdge: 0.05, Bankroll: 5_000, FractionalKellyCap: 0.2, RiskFreeRate: 0.02, DividendYield: 0, PeriodsPerYear: 252})

	tests := []struct {
		name       string
		longOI     float64
		shortOI    float64
		longVol    float64
		shortVol   float64
		wantReason string
	}{
		{name: "low leg open interest", longOI: 200, shortOI: 10, longVol: 200, shortVol: 200, wantReason: "insufficient_open_interest"},
		{name: "low leg volume", longOI: 200, shortOI: 200, longVol: 200, shortVol: 10, wantReason: "insufficient_volume"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := sc.Scan(Input{
				Now:             now,
				UnderlyingPrice: 100,
				Spread: domain.OptionSpread{
					StrategyType: domain.StrategyBullCallSpread,
					Underlying:   "AAPL",
					Legs: []domain.SpreadLeg{
						{Contract: long, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToOpen, Ratio: 1, Quantity: 1},
						{Contract: short, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToOpen, Ratio: 1, Quantity: 1},
					},
				},
				Chain: []domain.OptionSnapshot{
					{Contract: long, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 9.80, Ask: 10.20, Volume: tt.longVol, OpenInterest: tt.longOI},
					{Contract: short, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 4.00, Ask: 4.50, Volume: tt.shortVol, OpenInterest: tt.shortOI},
				},
			})
			if res.Accepted {
				t.Fatalf("Scan() accepted = true, want false; decision=%+v", res.Decision)
			}
			if len(res.Reasons) == 0 || res.Reasons[0] != tt.wantReason {
				t.Fatalf("reasons = %v, want first reason %q", res.Reasons, tt.wantReason)
			}
			if res.Decision.ApprovedSize != 0 {
				t.Fatalf("ApprovedSize = %v, want 0", res.Decision.ApprovedSize)
			}
			assertFiniteTradeDecision(t, res.Decision)
		})
	}
}

func TestScannerRejectsStaleIlliquidWideAndInvalidContracts(t *testing.T) {
	now := time.Date(2026, 3, 1, 15, 0, 0, 0, time.UTC)
	expiry := now.AddDate(0, 0, 30)
	base := domain.OptionContract{OCCSymbol: domain.FormatOCC("AAPL", domain.OptionTypeCall, 100, expiry), Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 100, Expiry: expiry, Multiplier: 100, Style: "european"}
	sc := NewScanner(Config{MinOpenInterest: 100, MinVolume: 50, MaxBidAskSpread: 1.0, MinNetEdge: 0.05, Bankroll: 1000, FractionalKellyCap: 0.1, RiskFreeRate: 0.02, PeriodsPerYear: 252})

	tests := []struct {
		name       string
		contract   domain.OptionContract
		bid        float64
		ask        float64
		volume     float64
		oi         float64
		expiry     time.Time
		wantReason string
	}{
		{name: "stale", contract: base, bid: 4.8, ask: 5.0, volume: 100, oi: 200, expiry: now.Add(-time.Hour), wantReason: "stale_contract"},
		{name: "illiquid oi", contract: base, bid: 4.8, ask: 5.0, volume: 100, oi: 10, expiry: expiry, wantReason: "insufficient_open_interest"},
		{name: "illiquid volume", contract: base, bid: 4.8, ask: 5.0, volume: 10, oi: 200, expiry: expiry, wantReason: "insufficient_volume"},
		{name: "wide spread", contract: base, bid: 4.0, ask: 5.5, volume: 100, oi: 200, expiry: expiry, wantReason: "wide_spread"},
		{name: "invalid exec", contract: base, bid: 5.2, ask: 5.0, volume: 100, oi: 200, expiry: expiry, wantReason: "invalid_executable_price"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contract := tt.contract
			contract.Expiry = tt.expiry
			contract.OCCSymbol = domain.FormatOCC(contract.Underlying, contract.OptionType, contract.Strike, contract.Expiry)
			res := sc.Scan(Input{
				Now:             now,
				UnderlyingPrice: 100,
				Spread: domain.OptionSpread{
					StrategyType: domain.StrategyLongCall,
					Underlying:   contract.Underlying,
					Legs:         []domain.SpreadLeg{{Contract: contract, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToOpen, Ratio: 1, Quantity: 1}},
				},
				Chain: []domain.OptionSnapshot{{Contract: contract, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: tt.bid, Ask: tt.ask, Volume: tt.volume, OpenInterest: tt.oi}},
			})
			if res.Accepted {
				t.Fatalf("Scan() accepted = true, want false; decision=%+v", res.Decision)
			}
			if len(res.Reasons) == 0 || res.Reasons[0] != tt.wantReason {
				t.Fatalf("reasons = %v, want first reason %q", res.Reasons, tt.wantReason)
			}
			if res.Decision.ApprovedSize != 0 {
				t.Fatalf("ApprovedSize = %v, want 0", res.Decision.ApprovedSize)
			}
			assertFiniteTradeDecision(t, res.Decision)
		})
	}
}

func TestScannerRejectsNakedShortAndUnsupportedStrategies(t *testing.T) {
	now := time.Date(2026, 4, 1, 15, 0, 0, 0, time.UTC)
	expiry := now.AddDate(0, 0, 30)
	call := domain.OptionContract{OCCSymbol: domain.FormatOCC("AAPL", domain.OptionTypeCall, 100, expiry), Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 100, Expiry: expiry, Multiplier: 100, Style: "european"}
	putHigh := domain.OptionContract{OCCSymbol: domain.FormatOCC("AAPL", domain.OptionTypePut, 105, expiry), Underlying: "AAPL", OptionType: domain.OptionTypePut, Strike: 105, Expiry: expiry, Multiplier: 100, Style: "european"}
	putLow := domain.OptionContract{OCCSymbol: domain.FormatOCC("AAPL", domain.OptionTypePut, 95, expiry), Underlying: "AAPL", OptionType: domain.OptionTypePut, Strike: 95, Expiry: expiry, Multiplier: 100, Style: "european"}
	callHigh := domain.OptionContract{OCCSymbol: domain.FormatOCC("AAPL", domain.OptionTypeCall, 105, expiry), Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 105, Expiry: expiry, Multiplier: 100, Style: "european"}
	sc := NewScanner(Config{MinOpenInterest: 1, MinVolume: 1, MaxBidAskSpread: 10, MinNetEdge: 0.01, Bankroll: 1000, FractionalKellyCap: 0.1, RiskFreeRate: 0.02, PeriodsPerYear: 252})

	nakedShort := sc.Scan(Input{
		Now:             now,
		UnderlyingPrice: 100,
		Spread: domain.OptionSpread{
			StrategyType: domain.StrategyLongCall,
			Underlying:   "AAPL",
			Legs:         []domain.SpreadLeg{{Contract: call, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToOpen, Ratio: 1, Quantity: 1}},
		},
		Chain: []domain.OptionSnapshot{{Contract: call, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 4.8, Ask: 5.0, Volume: 100, OpenInterest: 200}},
	})
	if nakedShort.Accepted {
		t.Fatal("naked short accepted, want rejection")
	}
	if len(nakedShort.Reasons) == 0 || nakedShort.Reasons[0] != "undefined_risk_rejected" {
		t.Fatalf("naked short reasons = %v, want undefined_risk_rejected", nakedShort.Reasons)
	}
	if nakedShort.Decision.ApprovedSize != 0 {
		t.Fatalf("naked short ApprovedSize = %v, want 0", nakedShort.Decision.ApprovedSize)
	}
	assertFiniteTradeDecision(t, nakedShort.Decision)

	unsupported := sc.Scan(Input{
		Now:             now,
		UnderlyingPrice: 100,
		Spread: domain.OptionSpread{
			StrategyType: domain.StrategyShortStraddle,
			Underlying:   "AAPL",
			Legs: []domain.SpreadLeg{
				{Contract: call, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToOpen, Ratio: 1, Quantity: 1},
				{Contract: putHigh, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToOpen, Ratio: 1, Quantity: 1},
			},
		},
		Chain: []domain.OptionSnapshot{
			{Contract: call, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 4.8, Ask: 5.0, Volume: 100, OpenInterest: 200},
			{Contract: putHigh, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 5.0, Ask: 5.3, Volume: 100, OpenInterest: 200},
		},
	})
	if unsupported.Accepted {
		t.Fatal("unsupported strategy accepted, want rejection")
	}
	if len(unsupported.Reasons) == 0 || unsupported.Reasons[0] != "undefined_risk_rejected" {
		t.Fatalf("unsupported reasons = %v, want undefined_risk_rejected", unsupported.Reasons)
	}
	if unsupported.Decision.ApprovedSize != 0 {
		t.Fatalf("unsupported ApprovedSize = %v, want 0", unsupported.Decision.ApprovedSize)
	}
	assertFiniteTradeDecision(t, unsupported.Decision)

	buyOnlyUnsupported := sc.Scan(Input{
		Now:             now,
		UnderlyingPrice: 100,
		Spread: domain.OptionSpread{
			StrategyType: domain.StrategyLongStraddle,
			Underlying:   "AAPL",
			Legs: []domain.SpreadLeg{
				{Contract: callHigh, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToOpen, Ratio: 1, Quantity: 1},
				{Contract: putLow, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToOpen, Ratio: 1, Quantity: 1},
			},
		},
		Chain: []domain.OptionSnapshot{
			{Contract: callHigh, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 4.0, Ask: 4.3, Volume: 100, OpenInterest: 200},
			{Contract: putLow, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 2.0, Ask: 2.3, Volume: 100, OpenInterest: 200},
		},
	})
	if buyOnlyUnsupported.Accepted {
		t.Fatal("buy-only unsupported strategy accepted, want rejection")
	}
	if len(buyOnlyUnsupported.Reasons) == 0 || buyOnlyUnsupported.Reasons[0] != "unsupported_strategy" {
		t.Fatalf("buy-only unsupported reasons = %v, want unsupported_strategy", buyOnlyUnsupported.Reasons)
	}
	if buyOnlyUnsupported.Decision.ApprovedSize != 0 {
		t.Fatalf("buy-only unsupported ApprovedSize = %v, want 0", buyOnlyUnsupported.Decision.ApprovedSize)
	}
	assertFiniteTradeDecision(t, buyOnlyUnsupported.Decision)
}

func TestScannerUsesResolvedLegKeysWhenOCCMissing(t *testing.T) {
	now := time.Date(2026, 5, 1, 15, 0, 0, 0, time.UTC)
	expiry := now.AddDate(0, 0, 30)
	long := domain.OptionContract{Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 90, Expiry: expiry, Multiplier: 100, Style: "european"}
	short := domain.OptionContract{Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 100, Expiry: expiry, Multiplier: 100, Style: "european"}
	sc := NewScanner(Config{MinOpenInterest: 100, MinVolume: 50, MaxBidAskSpread: 1.0, MinNetEdge: 0.05, Bankroll: 10_000, FractionalKellyCap: 0.25, RiskFreeRate: 0.02, DividendYield: 0, PeriodsPerYear: 252})

	res := sc.Scan(Input{
		Now:             now,
		UnderlyingPrice: 100,
		Spread: domain.OptionSpread{
			StrategyType: domain.StrategyBullCallSpread,
			Underlying:   "AAPL",
			Legs: []domain.SpreadLeg{
				{Contract: long, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToOpen, Ratio: 1, Quantity: 1},
				{Contract: short, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToOpen, Ratio: 1, Quantity: 1},
			},
		},
		Chain: []domain.OptionSnapshot{
			{Contract: long, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 9.80, Ask: 10.20, Volume: 500, OpenInterest: 1500},
			{Contract: short, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 4.00, Ask: 4.50, Volume: 450, OpenInterest: 1200},
		},
	})

	if !res.Accepted {
		t.Fatalf("Scan() accepted = false, reasons=%v", res.Reasons)
	}
	wantKey := string(domain.StrategyBullCallSpread) + ":" + domain.FormatOCC("AAPL", domain.OptionTypeCall, 90, expiry) + "+" + domain.FormatOCC("AAPL", domain.OptionTypeCall, 100, expiry)
	if res.Decision.InstrumentKey != wantKey {
		t.Fatalf("InstrumentKey = %q, want %q", res.Decision.InstrumentKey, wantKey)
	}
	if res.Decision.InstrumentKey == "" {
		t.Fatal("InstrumentKey is empty")
	}
	assertFiniteTradeDecision(t, res.Decision)
}

func assertFiniteTradeDecision(t *testing.T, d domain.TradeDecision) {
	t.Helper()
	vals := []float64{d.FairValue, d.ExecutablePrice, d.Spread, d.Depth, d.GrossEV, d.NetEV, d.KellyFraction, d.ProposedSize, d.ApprovedSize}
	for i, v := range vals {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("decision numeric field %d is not finite: %v", i, v)
		}
	}
	if d.RiskStatus == "" || d.Status == "" {
		t.Fatalf("decision missing status fields: %+v", d)
	}
}
