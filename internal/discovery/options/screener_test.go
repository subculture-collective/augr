package options

import (
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestOptionsScreenCompletionError(t *testing.T) {
	for _, tc := range []struct {
		name     string
		attempts int64
		errors   int64
		wantErr  bool
	}{
		{name: "no eligible tickers", attempts: 0, errors: 0},
		{name: "valid empty screen", attempts: 3, errors: 0},
		{name: "partial provider failure", attempts: 3, errors: 2},
		{name: "all provider lookups failed", attempts: 3, errors: 3, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := optionsScreenCompletionError(tc.attempts, tc.errors)
			if (err != nil) != tc.wantErr {
				t.Fatalf("optionsScreenCompletionError(%d, %d) error = %v, wantErr %v", tc.attempts, tc.errors, err, tc.wantErr)
			}
		})
	}
}

func TestNearestExpiryChainUsesDeterministicCompleteExpiry(t *testing.T) {
	target := time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC)
	earlier := target.AddDate(0, 0, -1)
	later := target.AddDate(0, 0, 1)
	chain := []domain.OptionSnapshot{
		{Contract: domain.OptionContract{OCCSymbol: "LATER-1", Expiry: later}},
		{Contract: domain.OptionContract{OCCSymbol: "EARLIER-1", Expiry: earlier}},
		{Contract: domain.OptionContract{OCCSymbol: "EARLIER-2", Expiry: earlier}},
		{Contract: domain.OptionContract{OCCSymbol: "LATER-2", Expiry: later}},
	}

	selected := nearestExpiryChain(chain, target)
	if len(selected) != 2 || selected[0].Contract.OCCSymbol != "EARLIER-1" || selected[1].Contract.OCCSymbol != "EARLIER-2" {
		t.Fatalf("nearest expiry chain = %#v", selected)
	}
}
