package options

import (
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/backtest"
	"github.com/PatrickFanella/get-rich-quick/internal/discovery"
)

func TestRunOptionsDiscoveryRequiresNineMonthImmutableCutoff(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := RunOptionsDiscovery(t.Context(), OptionsDiscoveryConfig{
		EvaluationStart: start, EvaluationEnd: start.AddDate(0, 9, 0), DecisionCutoff: start.AddDate(0, 8, 0),
	}, OptionsDiscoveryDeps{})
	if err == nil || !strings.Contains(err.Error(), "six calibration months plus three out-of-sample months") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunOptionsDiscoveryRequiresManifestBoundHistoricalReader(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := RunOptionsDiscovery(t.Context(), OptionsDiscoveryConfig{
		EvaluationStart: start, EvaluationEnd: start.AddDate(0, 9, 0), DecisionCutoff: start.AddDate(0, 9, 0),
	}, OptionsDiscoveryDeps{})
	if err == nil || !strings.Contains(err.Error(), "manifest-bound historical options reader is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestHistoricalFramesBetweenDoesNotReuseCalibrationBoundary(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	boundary := start.AddDate(0, 6, 0)
	end := boundary.AddDate(0, 3, 0)
	frames := []HistoricalOptionFrame{{DecisionAt: start}, {DecisionAt: boundary}, {DecisionAt: end}}

	calibration := historicalFramesBetween(frames, start, boundary, false)
	validation := historicalFramesBetween(frames, boundary, end, true)
	if len(calibration) != 1 || len(validation) != 2 || !validation[0].DecisionAt.Equal(boundary) {
		t.Fatalf("calibration/validation = %#v / %#v", calibration, validation)
	}
}

func TestValidateObservedOptionsRequiresTradesInBothWindows(t *testing.T) {
	result := validateObservedOptions(discovery.ValidationConfig{},
		&HistoricalOptionsEvaluation{Metrics: backtest.Metrics{SharpeRatio: 1}, OpenedPackages: 1},
		&HistoricalOptionsEvaluation{Metrics: backtest.Metrics{SharpeRatio: 1}},
	)
	if result.Passed || result.Reason == "" {
		t.Fatalf("validation = %+v", result)
	}
}

func TestValidateObservedOptionsEnforcesOOSRatio(t *testing.T) {
	result := validateObservedOptions(discovery.ValidationConfig{MinOOSRatio: 0.75},
		&HistoricalOptionsEvaluation{Metrics: backtest.Metrics{SharpeRatio: 2}, OpenedPackages: 1},
		&HistoricalOptionsEvaluation{Metrics: backtest.Metrics{SharpeRatio: 1}, OpenedPackages: 1},
	)
	if result.Passed || result.OOSRatio != 0.5 {
		t.Fatalf("validation = %+v", result)
	}
}
