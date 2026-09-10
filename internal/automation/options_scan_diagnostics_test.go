package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

type diagnosticOptionsProvider struct {
	data.OptionsDataProvider
	calls int
}

func (p *diagnosticOptionsProvider) GetOptionsChain(context.Context, string, time.Time, domain.OptionType) ([]domain.OptionSnapshot, error) {
	p.calls++
	return make([]domain.OptionSnapshot, 9), nil
}

func TestOptionsScanRejectedInputsRetainTickerDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, reason, summary string
		stale                 bool
		chainCalls            int
	}{
		{"stale price", "price_stale", "price_stale", true, 0},
		{"insufficient chain", "chain_insufficient", "chain_insufficient", false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, time.September, 9, 22, 0, 0, 0, easternTime)
			provider := &partialResultProvider{ohlcv: func(_ string, _, _ time.Time) ([]domain.OHLCV, error) {
				stamp := expectedCompletedNYSESession(now)
				if tc.stale {
					stamp = stamp.AddDate(0, 0, -5)
				}
				return []domain.OHLCV{{Timestamp: stamp, Open: 100, High: 100, Low: 100, Close: 100, Volume: 1000}}, nil
			}}
			orch := partialResultOrchestrator([]string{"AAPL"}, partialResultDataService(provider, &partialResultHistoryRepo{}))
			options := &diagnosticOptionsProvider{}
			orch.deps.OptionsProvider = options
			orch.now = func() time.Time { return now }
			var output bytes.Buffer
			orch.logger = slog.New(slog.NewJSONHandler(&output, nil))
			orch.Register("options_scan", "test", optionsScanSpec, orch.optionsScan)
			if err := orch.optionsScan(t.Context()); err == nil {
				t.Fatal("rejected coverage must not pass")
			}
			if options.calls != tc.chainCalls {
				t.Fatalf("chain calls=%d, want %d", options.calls, tc.chainCalls)
			}
			if summary := singleJobStatus(t, orch, "options_scan").LastSummary; summary[tc.summary] != 1 || summary["setups"] != 0 {
				t.Fatalf("rejection summary changed: %v", summary)
			}
			matches := 0
			for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
				var entry map[string]any
				if err := json.Unmarshal([]byte(line), &entry); err != nil {
					t.Fatal(err)
				}
				if entry["ticker"] == "AAPL" && entry["reason"] == tc.reason && entry["level"] == "WARN" {
					matches++
					if tc.reason == "chain_insufficient" && (entry["contracts"] != float64(9) || entry["minimum_contracts"] != float64(10)) {
						t.Fatalf("incorrect chain diagnostic counts: %v", entry)
					}
				}
			}
			if matches != 1 {
				t.Fatalf("attributable %s diagnostics=%d, want exactly one: %s", tc.reason, matches, output.String())
			}
		})
	}
}
