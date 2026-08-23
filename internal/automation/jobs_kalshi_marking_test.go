package automation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/kalshi"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type kalshiMarkProviderStub struct {
	quotes map[string]kalshi.Snapshot
	errors map[string]error
	calls  []string
}

func (stub *kalshiMarkProviderStub) LoadSnapshot(_ context.Context, ticker string) (kalshi.Snapshot, error) {
	stub.calls = append(stub.calls, ticker)
	if err := stub.errors[ticker]; err != nil {
		return kalshi.Snapshot{}, err
	}
	return stub.quotes[ticker], nil
}

type kalshiProjectionStub struct {
	repository.ProjectionRepository
	lots     []repository.CanonicalOpenLot
	marks    []*ledger.MarkObservation
	rebuilds []ledger.ProjectionRequest
}

func (stub *kalshiProjectionStub) ListCanonicalOpenLots(context.Context, time.Time) ([]repository.CanonicalOpenLot, error) {
	return stub.lots, nil
}

func (stub *kalshiProjectionStub) RecordMarkObservation(_ context.Context, mark *ledger.MarkObservation) (*ledger.MarkObservation, error) {
	stub.marks = append(stub.marks, mark)
	return mark, nil
}

func (stub *kalshiProjectionStub) RebuildPortfolioProjection(_ context.Context, request ledger.ProjectionRequest) (*ledger.PortfolioProjection, error) {
	stub.rebuilds = append(stub.rebuilds, request)
	return &ledger.PortfolioProjection{}, nil
}

func TestKalshiMarkingMarksCanonicalLotsAndRebuildsEachAccountOnce(t *testing.T) {
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	accountID := uuid.New()
	repo := &kalshiProjectionStub{lots: []repository.CanonicalOpenLot{
		{AccountID: accountID, InstrumentID: uuid.New(), VenueContractID: uuid.New(), Side: domain.PositionSideLong, Ticker: "KXONE:YES", Currency: "USD"},
		{AccountID: accountID, InstrumentID: uuid.New(), VenueContractID: uuid.New(), Side: domain.PositionSideLong, Ticker: "KXTWO:NO", Currency: "USD"},
	}}
	provider := &kalshiMarkProviderStub{quotes: map[string]kalshi.Snapshot{
		"KXONE": {Ticker: "KXONE", Status: "active", BestBidYes: 0.4, BestAskYes: 0.42, FetchedAt: now.Add(-time.Second)},
		"KXTWO": {Ticker: "KXTWO", Status: "active", BestBidNo: 0.6, BestAskNo: 0.62, FetchedAt: now.Add(-time.Second)},
	}}
	orch := NewJobOrchestrator(OrchestratorDeps{KalshiMarkProvider: provider, KalshiProjectionRepo: repo, KalshiMarkMaxAge: time.Minute})
	orch.now = func() time.Time { return now }
	orch.RegisterAll()
	if _, ok := orch.jobs["kalshi_marking"]; !ok {
		t.Fatal("kalshi_marking job was not registered")
	}
	if err := orch.kalshiMarking(context.Background()); err != nil {
		t.Fatalf("kalshiMarking() error = %v", err)
	}
	if len(repo.marks) != 2 || len(repo.rebuilds) != 1 {
		t.Fatalf("marks/rebuilds = %d/%d, want 2/1", len(repo.marks), len(repo.rebuilds))
	}
	if repo.rebuilds[0].AccountID != accountID || repo.rebuilds[0].MarkSource != kalshi.KalshiMarkSource ||
		repo.rebuilds[0].MarkNamespace != kalshi.KalshiAccountMarkNamespace(accountID) {
		t.Fatalf("rebuild request = %+v", repo.rebuilds[0])
	}
	if repo.rebuilds[0].AsOf.Before(repo.marks[1].ObservedAt) {
		t.Fatalf("rebuild as-of %s precedes mark observation %s", repo.rebuilds[0].AsOf, repo.marks[1].ObservedAt)
	}
	var metadata struct {
		AccountID string `json:"account_id"`
	}
	if err := json.Unmarshal(repo.marks[0].Metadata, &metadata); err != nil || metadata.AccountID != accountID.String() {
		t.Fatalf("mark account metadata = %q, %v", metadata.AccountID, err)
	}
	if !repo.marks[0].Price.Equal(decimal.RequireFromString("0.4")) || !repo.marks[1].Price.Equal(decimal.RequireFromString("0.6")) {
		t.Fatalf("side-aware marks = %s/%s", repo.marks[0].Price, repo.marks[1].Price)
	}
}

func TestKalshiMarkingLeavesUnavailableLotsUnmarked(t *testing.T) {
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	repo := &kalshiProjectionStub{lots: []repository.CanonicalOpenLot{{
		AccountID: uuid.New(), InstrumentID: uuid.New(), VenueContractID: uuid.New(), Side: domain.PositionSideLong, Ticker: "KXHALT:YES", Currency: "USD",
	}}}
	provider := &kalshiMarkProviderStub{quotes: map[string]kalshi.Snapshot{
		"KXHALT": {Ticker: "KXHALT", Status: "halted", BestBidYes: 0.4, BestAskYes: 0.42, FetchedAt: now},
	}}
	orch := NewJobOrchestrator(OrchestratorDeps{KalshiMarkProvider: provider, KalshiProjectionRepo: repo, KalshiMarkMaxAge: time.Minute})
	orch.now = func() time.Time { return now }
	orch.RegisterAll()
	if err := orch.kalshiMarking(context.Background()); err == nil || !strings.Contains(err.Error(), "canonical lots unmarked") {
		t.Fatalf("kalshiMarking() error = %v, want surfaced unavailable inventory", err)
	}
	if len(repo.marks) != 0 || len(repo.rebuilds) != 0 {
		t.Fatalf("unavailable lot wrote marks/rebuilds = %d/%d", len(repo.marks), len(repo.rebuilds))
	}
}

func TestKalshiMarkingIsolatesOneProviderFailure(t *testing.T) {
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	accountID := uuid.New()
	repo := &kalshiProjectionStub{lots: []repository.CanonicalOpenLot{
		{AccountID: accountID, InstrumentID: uuid.New(), VenueContractID: uuid.New(), Side: domain.PositionSideLong, Ticker: "KXFAIL:YES", Currency: "USD"},
		{AccountID: accountID, InstrumentID: uuid.New(), VenueContractID: uuid.New(), Side: domain.PositionSideLong, Ticker: "KXOK:YES", Currency: "USD"},
	}}
	provider := &kalshiMarkProviderStub{
		errors: map[string]error{"KXFAIL": errors.New("provider unavailable")},
		quotes: map[string]kalshi.Snapshot{"KXOK": {
			Ticker: "KXOK", Status: "active", BestBidYes: 0.4, BestAskYes: 0.42, FetchedAt: now,
		}},
	}
	orch := NewJobOrchestrator(OrchestratorDeps{KalshiMarkProvider: provider, KalshiProjectionRepo: repo, KalshiMarkMaxAge: time.Minute})
	orch.now = func() time.Time { return now }
	if err := orch.kalshiMarking(context.Background()); err == nil || !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("kalshiMarking() error = %v, want aggregated provider failure", err)
	}
	if len(repo.marks) != 1 || len(repo.rebuilds) != 0 || len(provider.calls) != 2 {
		t.Fatalf("partial account marks/rebuilds/calls = %d/%d/%d", len(repo.marks), len(repo.rebuilds), len(provider.calls))
	}
}

func TestKalshiMarkingRejectsShortCanonicalInventory(t *testing.T) {
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	repo := &kalshiProjectionStub{lots: []repository.CanonicalOpenLot{{
		AccountID: uuid.New(), InstrumentID: uuid.New(), VenueContractID: uuid.New(), Side: domain.PositionSideShort, Ticker: "KXSHORT:YES", Currency: "USD",
	}}}
	provider := &kalshiMarkProviderStub{quotes: map[string]kalshi.Snapshot{
		"KXSHORT": {Ticker: "KXSHORT", Status: "active", BestBidYes: 0.4, BestAskYes: 0.42, FetchedAt: now},
	}}
	orch := NewJobOrchestrator(OrchestratorDeps{KalshiMarkProvider: provider, KalshiProjectionRepo: repo, KalshiMarkMaxAge: time.Minute})
	orch.now = func() time.Time { return now }
	err := orch.kalshiMarking(context.Background())
	if err == nil || !strings.Contains(err.Error(), "short canonical lots are unavailable") {
		t.Fatalf("kalshiMarking() error = %v, want short-inventory rejection", err)
	}
	if len(repo.marks) != 0 || len(repo.rebuilds) != 0 {
		t.Fatalf("short inventory wrote marks/rebuilds = %d/%d", len(repo.marks), len(repo.rebuilds))
	}
	if len(provider.calls) != 0 {
		t.Fatalf("short inventory loaded provider %v", provider.calls)
	}
}

func TestKalshiMarkingCapturesAsOfAfterQuoteLoad(t *testing.T) {
	start := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	accountID := uuid.New()
	repo := &kalshiProjectionStub{lots: []repository.CanonicalOpenLot{{
		AccountID: accountID, InstrumentID: uuid.New(), VenueContractID: uuid.New(), Side: domain.PositionSideLong, Ticker: "KXLATE:YES", Currency: "USD",
	}}}
	provider := &kalshiMarkProviderStub{quotes: map[string]kalshi.Snapshot{
		"KXLATE": {Ticker: "KXLATE", Status: "active", BestBidYes: 0.4, BestAskYes: 0.42, FetchedAt: start.Add(time.Second)},
	}}
	orch := NewJobOrchestrator(OrchestratorDeps{KalshiMarkProvider: provider, KalshiProjectionRepo: repo, KalshiMarkMaxAge: time.Minute})
	calls := 0
	orch.now = func() time.Time {
		calls++
		return start.Add(time.Duration(calls-1) * time.Second)
	}
	if err := orch.kalshiMarking(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.rebuilds) != 1 || !repo.rebuilds[0].AsOf.Equal(start.Add(time.Second)) || len(repo.marks) != 1 {
		t.Fatalf("post-load mark/rebuild = %+v/%+v", repo.marks, repo.rebuilds)
	}
	if !repo.marks[0].ObservedAt.Equal(start.Add(time.Second)) {
		t.Fatalf("snapshot observation = %s, want %s", repo.marks[0].ObservedAt, start.Add(time.Second))
	}
}

func TestKalshiMarkingHasNoDiscoveryDependency(t *testing.T) {
	orch := NewJobOrchestrator(OrchestratorDeps{KalshiMarkProvider: &kalshiMarkProviderStub{}, KalshiProjectionRepo: &kalshiProjectionStub{}, KalshiMarkMaxAge: time.Minute})
	orch.RegisterAll()
	if dependencies := orch.jobs["kalshi_marking"].DependsOn; len(dependencies) != 0 {
		t.Fatalf("kalshi_marking dependencies = %v, want none", dependencies)
	}
}

func TestKalshiMarkingNoInventorySucceeds(t *testing.T) {
	repo := &kalshiProjectionStub{}
	orch := NewJobOrchestrator(OrchestratorDeps{
		KalshiMarkProvider: &kalshiMarkProviderStub{}, KalshiProjectionRepo: repo, KalshiMarkMaxAge: time.Minute,
	})
	if err := orch.kalshiMarking(context.Background()); err != nil {
		t.Fatalf("empty inventory error = %v", err)
	}
	if len(repo.marks) != 0 || len(repo.rebuilds) != 0 {
		t.Fatalf("empty inventory wrote marks/rebuilds = %d/%d", len(repo.marks), len(repo.rebuilds))
	}
}
