package discovery

import (
	"math"
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/backtest"
)

func TestScoreMetricsCannotSubstituteBarsForTradeEvidence(t *testing.T) {
	t.Parallel()
	metrics := backtest.Metrics{
		TotalBars: 1000, OrderAttempts: 0, OrderFills: 0,
		SharpeRatio: 1.5, SortinoRatio: 2, MaxDrawdown: 0.1,
	}
	if score := ScoreMetrics(metrics, DefaultScoringConfig()); !math.IsInf(score, -1) {
		t.Fatalf("zero trade evidence qualified using bar count: score=%v", score)
	}
}

func TestScoreMetricsClosedTradeThreshold(t *testing.T) {
	for _, closed := range []int{-1, 0, 9, 10, 11} {
		metrics := backtest.Metrics{
			TotalBars: 1000, OrderAttempts: 100, OrderFills: 100,
			ClosedTrades: closed, SharpeRatio: 1.5, SortinoRatio: 2, MaxDrawdown: 0.1,
		}
		score := ScoreMetrics(metrics, DefaultScoringConfig())
		if rejected := math.IsInf(score, -1); rejected != (closed < 10) {
			t.Fatalf("closed=%d score=%v", closed, score)
		}
	}
}
