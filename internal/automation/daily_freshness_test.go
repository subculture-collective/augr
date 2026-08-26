package automation

import (
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestCompletedDailyBarFreshUsesPreviousUTCDayForCrypto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		now    time.Time
		latest time.Time
		want   bool
	}{
		{name: "Friday accepts Thursday", now: utcDate(2026, time.August, 7), latest: utcDate(2026, time.August, 6), want: true},
		{name: "Saturday accepts Friday", now: utcDate(2026, time.August, 8), latest: utcDate(2026, time.August, 7), want: true},
		{name: "live Sunday tournament accepts Saturday", now: utcDate(2026, time.August, 9), latest: utcDate(2026, time.August, 8), want: true},
		{name: "Monday accepts Sunday", now: utcDate(2026, time.August, 10), latest: utcDate(2026, time.August, 9), want: true},
		{name: "Sunday rejects stale Friday", now: utcDate(2026, time.August, 9), latest: utcDate(2026, time.August, 7)},
		{name: "Sunday rejects current provisional candle", now: utcDate(2026, time.August, 9), latest: utcDate(2026, time.August, 9)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := completedDailyBarFresh(domain.MarketTypeCrypto, tt.now, tt.latest); got != tt.want {
				t.Fatalf("completedDailyBarFresh(crypto, %s, %s) = %v, want %v", tt.now, tt.latest, got, tt.want)
			}
		})
	}
}

func TestCompletedDailyBarFreshPreservesStockSessionSemantics(t *testing.T) {
	t.Parallel()

	preMarketMonday := time.Date(2026, time.August, 10, 8, 0, 0, 0, easternTime)
	friday := time.Date(2026, time.August, 7, 16, 0, 0, 0, easternTime)
	sunday := time.Date(2026, time.August, 9, 16, 0, 0, 0, easternTime)
	if !completedDailyBarFresh(domain.MarketTypeStock, preMarketMonday, friday) {
		t.Fatal("Friday NYSE session should remain fresh before Monday open")
	}
	if completedDailyBarFresh(domain.MarketTypeStock, preMarketMonday, sunday) {
		t.Fatal("Sunday must not replace the expected completed NYSE session")
	}
}

func utcDate(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
