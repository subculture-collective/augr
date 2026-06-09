package polymarketresearch

import (
	"math"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestScanBinaryOpportunityAcceptedWithCappedKelly(t *testing.T) {
	now := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	opp := ScanBinaryOpportunity(now, domain.PolymarketBookSnapshot{
		Slug:        "will-thing-happen",
		TokenID:     "token-123",
		Outcome:     "YES",
		BestBid:     0.50,
		BestAsk:     0.55,
		AskDepthUSD: 500,
		DepthUSD:    500,
		ReceivedAt:  now.Add(-time.Minute),
	}, BinaryProbabilityEstimate{Probability: 0.65, Source: "model-a"}, ScanConfig{
		MinNetEdge:             0.01,
		MaxSpread:              0.10,
		MinDepthUSD:            100,
		KellyFraction:          0.50,
		MaxPositionPctBankroll: 0.05,
		Bankroll:               100,
		Fee:                    0,
		Slippage:               0,
		ExitHaircut:            0,
		MaxSnapshotAge:         5 * time.Minute,
	})

	if opp.State != OpportunityStateAccepted {
		t.Fatalf("State = %q, want accepted", opp.State)
	}
	if opp.Decision.MarketType != domain.MarketTypePolymarket {
		t.Fatalf("MarketType = %q, want polymarket", opp.Decision.MarketType)
	}
	if opp.Decision.Status != domain.TradeDecisionStatusCandidate {
		t.Fatalf("Status = %q, want candidate", opp.Decision.Status)
	}
	if !almostEqual(opp.GrossEV, 0.10, 1e-9) || !almostEqual(opp.NetEV, 0.10, 1e-9) {
		t.Fatalf("EV = (%v, %v), want (0.10, 0.10)", opp.GrossEV, opp.NetEV)
	}
	if !almostEqual(opp.KellyFraction, 0.05, 1e-9) {
		t.Fatalf("KellyFraction = %v, want 0.05", opp.KellyFraction)
	}
	if !almostEqual(opp.ApprovedSize, 5, 1e-9) {
		t.Fatalf("ApprovedSize = %v, want 5", opp.ApprovedSize)
	}
	if opp.Decision.InstrumentKey != "token-123" || opp.Decision.Outcome != "YES" {
		t.Fatalf("decision identifiers = (%q,%q)", opp.Decision.InstrumentKey, opp.Decision.Outcome)
	}
	if opp.Decision.ExternalMarketID != "will-thing-happen" {
		t.Fatalf("ExternalMarketID = %q, want slug", opp.Decision.ExternalMarketID)
	}
}

func TestScanBinaryOpportunityRejectsMaxSpreadBreach(t *testing.T) {
	now := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	opp := ScanBinaryOpportunity(now, domain.PolymarketBookSnapshot{
		Slug:       "spready",
		BestBid:    0.50,
		BestAsk:    0.62,
		DepthUSD:   500,
		ReceivedAt: now.Add(-time.Minute),
	}, BinaryProbabilityEstimate{Probability: 0.70}, ScanConfig{
		MinNetEdge:             0.01,
		MaxSpread:              0.08,
		MinDepthUSD:            100,
		KellyFraction:          0.50,
		MaxPositionPctBankroll: 0.05,
		Bankroll:               100,
		MaxSnapshotAge:         5 * time.Minute,
	})

	if opp.State != OpportunityStateRejected {
		t.Fatalf("State = %q, want rejected", opp.State)
	}
	if !containsString(opp.Reasons, "spread_too_wide") {
		t.Fatalf("Reasons = %v, want spread_too_wide", opp.Reasons)
	}
}

func TestScanBinaryOpportunityRejectsMinDepthBreach(t *testing.T) {
	now := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	opp := ScanBinaryOpportunity(now, domain.PolymarketBookSnapshot{
		Slug:       "shallow",
		BestBid:    0.50,
		BestAsk:    0.55,
		DepthUSD:   50,
		ReceivedAt: now.Add(-time.Minute),
	}, BinaryProbabilityEstimate{Probability: 0.65}, ScanConfig{
		MinNetEdge:             0.01,
		MaxSpread:              0.10,
		MinDepthUSD:            100,
		KellyFraction:          0.50,
		MaxPositionPctBankroll: 0.05,
		Bankroll:               100,
		MaxSnapshotAge:         5 * time.Minute,
	})

	if opp.State != OpportunityStateRejected {
		t.Fatalf("State = %q, want rejected", opp.State)
	}
	if !containsString(opp.Reasons, "insufficient_depth") {
		t.Fatalf("Reasons = %v, want insufficient_depth", opp.Reasons)
	}
}

func TestScanBinaryOpportunityRejectsHighBidAggregateButLowAskDepth(t *testing.T) {
	now := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	opp := ScanBinaryOpportunity(now, domain.PolymarketBookSnapshot{
		Slug:        "ask-shallow",
		TokenID:     "token-ask-shallow",
		Outcome:     "YES",
		BestBid:     0.50,
		BestAsk:     0.55,
		DepthUSD:    1000,
		BidDepthUSD: 900,
		AskDepthUSD: 15,
		ReceivedAt:  now.Add(-time.Minute),
	}, BinaryProbabilityEstimate{Probability: 0.65}, ScanConfig{
		MinNetEdge:             0.01,
		MaxSpread:              0.10,
		MinDepthUSD:            100,
		KellyFraction:          0.50,
		MaxPositionPctBankroll: 0.05,
		Bankroll:               100,
		MaxSnapshotAge:         5 * time.Minute,
	})

	if opp.State != OpportunityStateRejected {
		t.Fatalf("State = %q, want rejected", opp.State)
	}
	if !containsString(opp.Reasons, "insufficient_depth") {
		t.Fatalf("Reasons = %v, want insufficient_depth", opp.Reasons)
	}
	if opp.Decision.Depth != 15 {
		t.Fatalf("Depth = %v, want ask-side liquidity only", opp.Decision.Depth)
	}
}

func TestScanBinaryOpportunityRejectsInvalidConfigWithFiniteDecisionFields(t *testing.T) {
	now := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	opp := ScanBinaryOpportunity(now, domain.PolymarketBookSnapshot{
		Slug:        "bad-config",
		TokenID:     "token-bad-config",
		BestBid:     0.50,
		BestAsk:     0.55,
		AskDepthUSD: 500,
		ReceivedAt:  now.Add(-time.Minute),
	}, BinaryProbabilityEstimate{Probability: 0.60}, ScanConfig{
		MinNetEdge:             0.01,
		MaxSpread:              0.10,
		MinDepthUSD:            100,
		KellyFraction:          0.50,
		MaxPositionPctBankroll: 0.05,
		Bankroll:               math.Inf(1),
		MaxSnapshotAge:         5 * time.Minute,
	})

	if opp.State != OpportunityStateRejected {
		t.Fatalf("State = %q, want rejected", opp.State)
	}
	if !containsString(opp.Reasons, "invalid_bankroll") {
		t.Fatalf("Reasons = %v, want invalid_bankroll", opp.Reasons)
	}
	assertOpportunityFiniteRejected(t, opp)
	if opp.ApprovedSize != 0 || opp.Decision.ApprovedSize != 0 {
		t.Fatalf("ApprovedSize = (%v,%v), want zero", opp.ApprovedSize, opp.Decision.ApprovedSize)
	}
}

func TestScanBinaryOpportunityRejectsInvalidProbabilityOrPriceWithFiniteDecisionFields(t *testing.T) {
	now := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	probReject := ScanBinaryOpportunity(now, domain.PolymarketBookSnapshot{
		Slug:        "bad-prob",
		TokenID:     "token-bad-prob",
		BestBid:     0.50,
		BestAsk:     0.55,
		AskDepthUSD: 500,
		ReceivedAt:  now.Add(-time.Minute),
	}, BinaryProbabilityEstimate{Probability: math.NaN()}, ScanConfig{
		MinNetEdge:             0.01,
		MaxSpread:              0.10,
		MinDepthUSD:            100,
		KellyFraction:          0.50,
		MaxPositionPctBankroll: 0.05,
		Bankroll:               100,
		MaxSnapshotAge:         5 * time.Minute,
	})
	if !containsString(probReject.Reasons, "invalid_probability") {
		t.Fatalf("Reasons = %v, want invalid_probability", probReject.Reasons)
	}
	assertOpportunityFiniteRejected(t, probReject)
	if probReject.Decision.ApprovedSize != 0 {
		t.Fatalf("ApprovedSize = %v, want 0", probReject.Decision.ApprovedSize)
	}

	priceReject := ScanBinaryOpportunity(now, domain.PolymarketBookSnapshot{
		Slug:        "bad-price",
		TokenID:     "token-bad-price",
		BestBid:     0.50,
		BestAsk:     math.NaN(),
		AskDepthUSD: 500,
		ReceivedAt:  now.Add(-time.Minute),
	}, BinaryProbabilityEstimate{Probability: 0.60}, ScanConfig{
		MinNetEdge:             0.01,
		MaxSpread:              0.10,
		MinDepthUSD:            100,
		KellyFraction:          0.50,
		MaxPositionPctBankroll: 0.05,
		Bankroll:               100,
		MaxSnapshotAge:         5 * time.Minute,
	})
	if !containsString(priceReject.Reasons, "invalid_executable_price") {
		t.Fatalf("Reasons = %v, want invalid_executable_price", priceReject.Reasons)
	}
	assertOpportunityFiniteRejected(t, priceReject)
	if priceReject.Decision.ApprovedSize != 0 {
		t.Fatalf("ApprovedSize = %v, want 0", priceReject.Decision.ApprovedSize)
	}
}

func TestScanBinaryOpportunityRejectsMissingMarketIdentifiers(t *testing.T) {
	now := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	opp := ScanBinaryOpportunity(now, domain.PolymarketBookSnapshot{
		BestBid:     0.50,
		BestAsk:     0.55,
		AskDepthUSD: 500,
		ReceivedAt:  now.Add(-time.Minute),
	}, BinaryProbabilityEstimate{Probability: 0.60}, ScanConfig{
		MinNetEdge:             0.01,
		MaxSpread:              0.10,
		MinDepthUSD:            100,
		KellyFraction:          0.50,
		MaxPositionPctBankroll: 0.05,
		Bankroll:               100,
		MaxSnapshotAge:         5 * time.Minute,
	})

	if !containsString(opp.Reasons, "missing_market_identifier") {
		t.Fatalf("Reasons = %v, want missing_market_identifier", opp.Reasons)
	}
	if opp.State != OpportunityStateRejected {
		t.Fatalf("State = %q, want rejected", opp.State)
	}
	assertOpportunityFiniteRejected(t, opp)
}

func TestScanBinaryOpportunityRejectsInsufficientEdge(t *testing.T) {
	now := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	opp := ScanBinaryOpportunity(now, domain.PolymarketBookSnapshot{
		Slug:       "thin-edge",
		BestBid:    0.50,
		BestAsk:    0.55,
		DepthUSD:   500,
		ReceivedAt: now.Add(-time.Minute),
	}, BinaryProbabilityEstimate{Probability: 0.56}, ScanConfig{
		MinNetEdge:             0.02,
		MaxSpread:              0.10,
		MinDepthUSD:            100,
		KellyFraction:          0.50,
		MaxPositionPctBankroll: 0.05,
		Bankroll:               100,
		MaxSnapshotAge:         5 * time.Minute,
	})

	if opp.State != OpportunityStateRejected {
		t.Fatalf("State = %q, want rejected", opp.State)
	}
	if !containsString(opp.Reasons, "insufficient_edge") {
		t.Fatalf("Reasons = %v, want insufficient_edge", opp.Reasons)
	}
}

func TestScanBinaryOpportunityRejectsInvalidProbabilityAndPrice(t *testing.T) {
	now := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	probReject := ScanBinaryOpportunity(now, domain.PolymarketBookSnapshot{
		Slug:       "bad-prob",
		BestBid:    0.50,
		BestAsk:    0.55,
		DepthUSD:   500,
		ReceivedAt: now.Add(-time.Minute),
	}, BinaryProbabilityEstimate{Probability: 0}, ScanConfig{
		MinNetEdge:             0.01,
		MaxSpread:              0.10,
		MinDepthUSD:            100,
		KellyFraction:          0.50,
		MaxPositionPctBankroll: 0.05,
		Bankroll:               100,
		MaxSnapshotAge:         5 * time.Minute,
	})
	if !containsString(probReject.Reasons, "invalid_probability") {
		t.Fatalf("Reasons = %v, want invalid_probability", probReject.Reasons)
	}

	priceReject := ScanBinaryOpportunity(now, domain.PolymarketBookSnapshot{
		Slug:       "bad-price",
		BestBid:    0.50,
		BestAsk:    1.20,
		DepthUSD:   500,
		ReceivedAt: now.Add(-time.Minute),
	}, BinaryProbabilityEstimate{Probability: 0.60}, ScanConfig{
		MinNetEdge:             0.01,
		MaxSpread:              1.00,
		MinDepthUSD:            100,
		KellyFraction:          0.50,
		MaxPositionPctBankroll: 0.05,
		Bankroll:               100,
		MaxSnapshotAge:         5 * time.Minute,
	})
	if !containsString(priceReject.Reasons, "invalid_executable_price") {
		t.Fatalf("Reasons = %v, want invalid_executable_price", priceReject.Reasons)
	}
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func almostEqual(got, want, tol float64) bool {
	if got > want {
		return got-want <= tol
	}
	return want-got <= tol
}

func assertOpportunityFiniteRejected(t *testing.T, opp Opportunity) {
	t.Helper()
	if opp.ApprovedSize != 0 || opp.Decision.ApprovedSize != 0 {
		t.Fatalf("ApprovedSize = (%v,%v), want zero", opp.ApprovedSize, opp.Decision.ApprovedSize)
	}
	for _, v := range []float64{
		opp.Estimate.Probability,
		opp.Estimate.Confidence,
		opp.GrossEV,
		opp.NetEV,
		opp.KellyFraction,
		opp.ProposedSize,
		opp.ApprovedSize,
		opp.Decision.FairValue,
		opp.Decision.ExecutablePrice,
		opp.Decision.Spread,
		opp.Decision.Depth,
		opp.Decision.GrossEV,
		opp.Decision.NetEV,
		opp.Decision.KellyFraction,
		opp.Decision.ProposedSize,
		opp.Decision.ApprovedSize,
	} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("found non-finite value: %v", v)
		}
	}
}
