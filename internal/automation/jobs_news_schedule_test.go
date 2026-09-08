package automation

import (
	"strings"
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/data/stocktwits"
)

func TestNewsScanSpec_ReducesCadenceToThirtyMinutesWhenTwentyMinuteScheduleStillOverlaps(t *testing.T) {
	t.Parallel()

	const want = "7-59/30 * * * 1-5"
	if got := newsScanSpec.Cron; got != want {
		t.Fatalf("newsScanSpec.Cron = %q, want %q", got, want)
	}
}

func TestNewsScanCompletionErrorRejectsPartialCoverage(t *testing.T) {
	t.Parallel()

	if err := newsScanCompletionError(map[string]int{}); err != nil {
		t.Fatalf("newsScanCompletionError(empty) = %v, want nil", err)
	}
	err := newsScanCompletionError(map[string]int{"feeds_attempted": 5, "feeds_succeeded": 4, "feed_errors": 1, "triage_missing": 2})
	if err == nil || !strings.Contains(err.Error(), "feed_errors=1") || !strings.Contains(err.Error(), "triage_missing=2") {
		t.Fatalf("newsScanCompletionError(partial) = %v", err)
	}
}

func TestNewsScanCompletionErrorDegradesUsableProviderCoverage(t *testing.T) {
	t.Parallel()

	err := newsScanCompletionError(map[string]int{"feeds_attempted": 5, "feeds_succeeded": 4, "feed_retries": 1, "feed_errors": 1})
	if !IsDegraded(err) || !strings.Contains(err.Error(), "feeds_succeeded=4") || !strings.Contains(err.Error(), "feed_retries=1") {
		t.Fatalf("newsScanCompletionError(80%% provider coverage) = %v, want detailed degraded result", err)
	}
	if err := newsScanCompletionError(map[string]int{"feeds_attempted": 5, "feeds_succeeded": 3, "feed_errors": 2}); err == nil || IsDegraded(err) {
		t.Fatalf("newsScanCompletionError(60%% provider coverage) = %v, want hard error", err)
	}
}

func TestSocialScanCompletionErrorRejectsErrors(t *testing.T) {
	t.Parallel()

	if err := socialScanCompletionError(map[string]int{}); err != nil {
		t.Fatalf("socialScanCompletionError(empty) = %v, want nil", err)
	}
	if err := socialScanCompletionError(map[string]int{"errors": 3}); err == nil || !strings.Contains(err.Error(), "3 provider or persistence errors") {
		t.Fatalf("socialScanCompletionError(errors) = %v", err)
	}
}

func TestNormalizeStocktwitsSentimentUsesSignedScoreAndRatios(t *testing.T) {
	t.Parallel()

	score, bullish, bearish := normalizeStocktwitsSentiment(&stocktwits.SymbolSentiment{Bullish: 14, Bearish: 1, Total: 15})
	if score != 13.0/15.0 || bullish != 14.0/15.0 || bearish != 1.0/15.0 {
		t.Fatalf("normalized sentiment = (%f, %f, %f), want signed score and ratios", score, bullish, bearish)
	}
}
