package kalshi

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
)

func TestSnapshotValidateExecutableSide(t *testing.T) {
	now := time.Date(2026, time.June, 13, 12, 0, 0, 0, time.UTC)
	future := now.Add(2 * time.Hour)

	valid := Snapshot{
		Ticker:       "KXTEST-YESNO",
		Title:        "Will test happen?",
		Status:       "active",
		BestBidYes:   0.45,
		BestAskYes:   0.47,
		BestBidNo:    0.53,
		BestAskNo:    0.55,
		Volume:       1000,
		OpenInterest: 500,
		CloseTime:    future,
		FetchedAt:    now,
	}
	for _, side := range []string{"YES", "NO"} {
		if err := valid.ValidateExecutableSide(side, 100, now); err != nil {
			t.Fatalf("valid %s snapshot rejected: %v", side, err)
		}
	}

	cases := []struct {
		name string
		side string
		mod  func(*Snapshot)
	}{
		{name: "missing ticker", side: "YES", mod: func(s *Snapshot) { s.Ticker = "" }},
		{name: "missing title", side: "YES", mod: func(s *Snapshot) { s.Title = "" }},
		{name: "missing status", side: "YES", mod: func(s *Snapshot) { s.Status = "" }},
		{name: "closed status", side: "YES", mod: func(s *Snapshot) { s.Status = "closed" }},
		{name: "settled status", side: "YES", mod: func(s *Snapshot) { s.Status = "settled" }},
		{name: "expired status", side: "YES", mod: func(s *Snapshot) { s.Status = "expired" }},
		{name: "nil close time", side: "YES", mod: func(s *Snapshot) { s.CloseTime = time.Time{} }},
		{name: "past close time", side: "YES", mod: func(s *Snapshot) { past := now.Add(-time.Minute); s.CloseTime = past }},
		{name: "low liquidity", side: "YES", mod: func(s *Snapshot) { s.Volume = 10; s.OpenInterest = 5 }},
		{name: "invalid side", side: "MAYBE", mod: func(*Snapshot) {}},
		{name: "malformed yes quotes", side: "YES", mod: func(s *Snapshot) { s.BestBidYes = 0.5; s.BestAskYes = 0.4 }},
		{name: "malformed no quotes", side: "NO", mod: func(s *Snapshot) { s.BestBidNo = 0.6; s.BestAskNo = 0.5 }},
		{name: "missing no quote", side: "NO", mod: func(s *Snapshot) { s.BestBidNo = 0; s.BestAskNo = 0 }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := valid
			tc.mod(&s)
			if err := s.ValidateExecutableSide(tc.side, 100, now); err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}
}

func TestNewMarkObservationUsesOutcomeBidAndStableSourceIdentity(t *testing.T) {
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	base := KalshiMarkInput{
		AccountID: uuid.New(), InstrumentID: uuid.New(), VenueContractID: uuid.New(), Side: domain.PositionSideLong,
		Ticker: "KXTEST:YES", ObservedAt: now, MaxAge: time.Minute,
		Quote: Snapshot{Ticker: "KXTEST", Status: "active", BestBidYes: 0.41, BestAskYes: 0.43, BestBidNo: 0.57, BestAskNo: 0.59, FetchedAt: now.Add(-time.Second)},
	}
	yes, err := NewMarkObservation(base)
	if err != nil {
		t.Fatalf("NewMarkObservation(YES) error = %v", err)
	}
	if !yes.Price.Equal(decimal.RequireFromString("0.41")) || yes.PriceCurrency != "USD" {
		t.Fatalf("YES mark = %s %s, want 0.41 USD", yes.Price, yes.PriceCurrency)
	}
	retry, err := NewMarkObservation(base)
	if err != nil || retry.ID != yes.ID || !ledger.SameMarkObservation(yes, retry) {
		t.Fatalf("identical mark retry did not converge: mark=%v err=%v", retry, err)
	}
	base.ObservedAt = now.Add(30 * time.Second)
	laterRetry, err := NewMarkObservation(base)
	if err != nil || laterRetry.ID != yes.ID || !ledger.SameMarkObservation(yes, laterRetry) {
		t.Fatalf("later retry of same snapshot conflicted: mark=%v err=%v", laterRetry, err)
	}
	otherAccount := base
	otherAccount.AccountID = uuid.New()
	other, err := NewMarkObservation(otherAccount)
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == yes.ID || other.SourceNamespace == yes.SourceNamespace {
		t.Fatalf("account-scoped marks crossed identity/namespace: %s/%s", yes.ID, other.ID)
	}
	base.InstrumentID = uuid.New()
	base.VenueContractID = uuid.New()
	base.Ticker = "KXTEST:NO"
	no, err := NewMarkObservation(base)
	if err != nil {
		t.Fatalf("NewMarkObservation(NO) error = %v", err)
	}
	if !no.Price.Equal(decimal.RequireFromString("0.57")) {
		t.Fatalf("NO mark = %s, want 0.57", no.Price)
	}
}

func TestNewMarkObservationRejectsUnavailableEvidence(t *testing.T) {
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	valid := KalshiMarkInput{
		AccountID: uuid.New(), InstrumentID: uuid.New(), VenueContractID: uuid.New(), Side: domain.PositionSideLong,
		Ticker: "KXTEST:YES", ObservedAt: now, MaxAge: time.Minute,
		Quote: Snapshot{Ticker: "KXTEST", Status: "active", BestBidYes: 0.41, BestAskYes: 0.43, FetchedAt: now.Add(-time.Second)},
	}
	tests := map[string]func(*KalshiMarkInput){
		"missing identity": func(v *KalshiMarkInput) { v.InstrumentID = uuid.Nil },
		"short lot":        func(v *KalshiMarkInput) { v.Side = domain.PositionSideShort },
		"ticker mismatch":  func(v *KalshiMarkInput) { v.Quote.Ticker = "OTHER" },
		"halted":           func(v *KalshiMarkInput) { v.Quote.Status = "halted" },
		"zero bid":         func(v *KalshiMarkInput) { v.Quote.BestBidYes = 0 },
		"crossed":          func(v *KalshiMarkInput) { v.Quote.BestBidYes = 0.5; v.Quote.BestAskYes = 0.4 },
		"stale":            func(v *KalshiMarkInput) { v.Quote.FetchedAt = now.Add(-2 * time.Minute) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			input := valid
			mutate(&input)
			if mark, err := NewMarkObservation(input); err == nil || mark != nil {
				t.Fatalf("NewMarkObservation() = %v, %v; want unavailable", mark, err)
			}
		})
	}
}

func TestEntryPriceAndSpreadForSideRequireExplicitQuotes(t *testing.T) {
	s := Snapshot{
		BestBidYes: 0.45,
		BestAskYes: 0.47,
	}

	if got, ok := s.EntryPriceForSide("YES"); !ok || got != 0.47 {
		t.Fatalf("YES entry price = %v, %v; want 0.47, true", got, ok)
	}
	if got, ok := s.SpreadForSide("YES"); !ok || math.Abs(got-0.02) > 1e-9 {
		t.Fatalf("YES spread = %v, %v; want 0.02, true", got, ok)
	}
	if got, ok := s.EntryPriceForSide("NO"); ok || got != 0 {
		t.Fatalf("NO entry price = %v, %v; want 0, false without NO quotes", got, ok)
	}
	if got, ok := s.SpreadForSide("NO"); ok || got != 0 {
		t.Fatalf("NO spread = %v, %v; want 0, false without NO quotes", got, ok)
	}

	s.BestBidNo = 0.53
	s.BestAskNo = 0.55
	if got, ok := s.EntryPriceForSide("NO"); !ok || got != 0.55 {
		t.Fatalf("NO entry price = %v, %v; want 0.55, true", got, ok)
	}
	if got, ok := s.SpreadForSide("NO"); !ok || math.Abs(got-0.02) > 1e-9 {
		t.Fatalf("NO spread = %v, %v; want 0.02, true", got, ok)
	}
}
