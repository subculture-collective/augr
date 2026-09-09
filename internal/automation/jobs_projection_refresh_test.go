package automation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	"github.com/google/uuid"
)

type refreshProjectionStub struct {
	kalshiProjectionStub
	requests []ledger.ProjectionRequest
	err      error
}

func (s *refreshProjectionStub) RebuildPortfolioProjection(_ context.Context, request ledger.ProjectionRequest) (*ledger.PortfolioProjection, error) {
	s.requests = append(s.requests, request)
	return nil, s.err
}

func TestProjectionRefreshBootstrapsAndRefreshesWithoutPositionsOrNewFills(t *testing.T) {
	account := uuid.New()
	binding, err := domain.NewExecutionAccountBinding(account, domain.AccountEnvironmentPaperScored)
	if err != nil {
		t.Fatal(err)
	}
	repo := &refreshProjectionStub{}
	orch := NewJobOrchestrator(OrchestratorDeps{CanonicalAccountID: account, ExecutionAccount: binding, KalshiProjectionRepo: repo, KalshiProjectionOutbox: repo, KalshiMarkMaxAge: time.Minute})
	now := time.Date(2026, 9, 8, 20, 0, 0, 0, time.UTC)
	orch.now = func() time.Time { return now }
	orch.RegisterAll()
	job := orch.jobs["portfolio_projection_refresh"]
	if job == nil {
		t.Fatal("cash-only account has no scheduled projection bootstrap/refresh")
	}
	for range 2 {
		if err := job.Fn(context.Background()); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Minute)
	}
	if len(repo.requests) != 2 {
		t.Fatalf("requests=%d", len(repo.requests))
	}
	a, b := repo.requests[0], repo.requests[1]
	if a.AccountID != account || b.AccountID != account || a.ThroughTransactionID != b.ThroughTransactionID || !b.AsOf.After(a.AsOf) {
		t.Fatalf("refresh must preserve account/frontier and advance observed time: %+v %+v", a, b)
	}
	if len(repo.marks) != 0 {
		t.Fatal("cash-only refresh fabricated marks")
	}
	repo.err = errors.New("projection persistence unavailable")
	if err := job.Fn(context.Background()); !errors.Is(err, repo.err) {
		t.Fatalf("lost persistence failure: %v", err)
	}
}

func TestProjectionRefreshRejectsMissingForeignAndLiveBindings(t *testing.T) {
	account := uuid.New()
	for _, tc := range []struct {
		name        string
		id          uuid.UUID
		environment domain.AccountEnvironment
	}{
		{"missing", uuid.Nil, domain.AccountEnvironmentPaperScored},
		{"foreign", uuid.New(), domain.AccountEnvironmentPaperScored},
		{"live", account, domain.AccountEnvironmentLive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binding, _ := domain.NewExecutionAccountBinding(tc.id, tc.environment)
			repo := &refreshProjectionStub{}
			orch := NewJobOrchestrator(OrchestratorDeps{CanonicalAccountID: account, ExecutionAccount: binding, KalshiProjectionRepo: repo, KalshiProjectionOutbox: repo, KalshiMarkMaxAge: time.Minute})
			orch.RegisterAll()
			if orch.jobs["portfolio_projection_refresh"] != nil {
				t.Fatal("invalid binding registered refresh")
			}
		})
	}
}

func TestProjectionRefreshCancellationDoesNotRebuild(t *testing.T) {
	account := uuid.New()
	binding, _ := domain.NewExecutionAccountBinding(account, domain.AccountEnvironmentPaperScored)
	repo := &refreshProjectionStub{}
	orch := NewJobOrchestrator(OrchestratorDeps{CanonicalAccountID: account, ExecutionAccount: binding, KalshiProjectionRepo: repo, KalshiProjectionOutbox: repo, KalshiMarkMaxAge: time.Minute})
	orch.RegisterAll()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := orch.jobs["portfolio_projection_refresh"].Fn(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if len(repo.requests) != 0 {
		t.Fatal("canceled refresh wrote a checkpoint")
	}
}
