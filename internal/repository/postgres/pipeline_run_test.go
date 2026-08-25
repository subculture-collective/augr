package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/agent"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/runcontrol"
	"github.com/PatrickFanella/get-rich-quick/internal/service"
)

func TestBuildPipelineRunListQuery_NoFilters(t *testing.T) {
	query, args := buildPipelineRunListQuery(repository.PipelineRunFilter{}, 10, 0)

	if len(args) != 2 {
		t.Fatalf("expected 2 args (limit, offset), got %d", len(args))
	}

	if args[0] != 10 {
		t.Errorf("expected limit=10, got %v", args[0])
	}

	if args[1] != 0 {
		t.Errorf("expected offset=0, got %v", args[1])
	}

	assertContains(t, query, "FROM pipeline_runs")
	assertContains(t, query, "ORDER BY started_at DESC, id DESC")
	assertContains(t, query, "LIMIT $1 OFFSET $2")
	assertNotContains(t, query, "WHERE")
}

func TestBuildPipelineRunListQuery_AllFilters(t *testing.T) {
	strategyID := uuid.New()
	tradeDate := time.Date(2026, time.March, 14, 9, 30, 0, 0, time.UTC)
	startedAfter := tradeDate.Add(-2 * time.Hour)
	startedBefore := tradeDate.Add(2 * time.Hour)

	filter := repository.PipelineRunFilter{
		StrategyID:    &strategyID,
		Ticker:        "AAPL",
		Status:        domain.PipelineStatusRunning,
		TradeDate:     &tradeDate,
		StartedAfter:  &startedAfter,
		StartedBefore: &startedBefore,
	}

	query, args := buildPipelineRunListQuery(filter, 25, 50)

	if len(args) != 8 {
		t.Fatalf("expected 8 args, got %d: %v", len(args), args)
	}

	assertContains(t, query, "strategy_id = $1")
	assertContains(t, query, "ticker = $2")
	assertContains(t, query, "status = $3")
	assertContains(t, query, "trade_date = $4::date")
	assertContains(t, query, "started_at >= $5")
	assertContains(t, query, "started_at <= $6")
	assertContains(t, query, "LIMIT $7 OFFSET $8")

	if args[0] != strategyID {
		t.Errorf("expected strategy_id arg %s, got %v", strategyID, args[0])
	}

	if args[1] != "AAPL" {
		t.Errorf("expected ticker arg AAPL, got %v", args[1])
	}

	if args[2] != domain.PipelineStatusRunning {
		t.Errorf("expected status arg running, got %v", args[2])
	}
}

func TestBuildPipelineRunListQuery_PartialFilters(t *testing.T) {
	strategyID := uuid.New()
	filter := repository.PipelineRunFilter{
		StrategyID: &strategyID,
		Status:     domain.PipelineStatusFailed,
	}

	query, args := buildPipelineRunListQuery(filter, 10, 0)

	if len(args) != 4 {
		t.Fatalf("expected 4 args, got %d: %v", len(args), args)
	}

	assertContains(t, query, "strategy_id = $1")
	assertNotContains(t, query, "ticker =")
	assertContains(t, query, "status = $2")
	assertNotContains(t, query, "trade_date =")
	assertContains(t, query, "LIMIT $3 OFFSET $4")
}

func TestMarshalConfigSnapshot_ValidJSON(t *testing.T) {
	input := json.RawMessage(`{"lookback":20,"threshold":0.5}`)

	got, err := marshalConfigSnapshot(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(got) != `{"lookback":20,"threshold":0.5}` {
		t.Errorf("expected config snapshot pass-through, got %s", got)
	}
}

func TestMarshalConfigSnapshot_NilDefaultsToNull(t *testing.T) {
	got, err := marshalConfigSnapshot(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != nil {
		t.Errorf("expected nil config snapshot, got %s", got)
	}
}

func TestMarshalConfigSnapshot_EmptyDefaultsToNull(t *testing.T) {
	got, err := marshalConfigSnapshot(json.RawMessage{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != nil {
		t.Errorf("expected nil config snapshot, got %s", got)
	}
}

func TestMarshalConfigSnapshot_InvalidJSON(t *testing.T) {
	_, err := marshalConfigSnapshot(json.RawMessage(`{not valid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestValidatePipelineRunFinalization(t *testing.T) {
	runID := uuid.New()
	otherRunID := uuid.New()
	now := time.Now()
	invalidSignal := domain.PipelineSignal("wait")

	tests := []struct {
		name         string
		finalization repository.PipelineRunFinalization
	}{
		{name: "running outcome", finalization: repository.PipelineRunFinalization{Status: domain.PipelineStatusRunning, CompletedAt: now}},
		{name: "completed error", finalization: repository.PipelineRunFinalization{Status: domain.PipelineStatusCompleted, CompletedAt: now, ErrorMessage: "boom"}},
		{name: "failed without error", finalization: repository.PipelineRunFinalization{Status: domain.PipelineStatusFailed, CompletedAt: now}},
		{name: "cancelled whitespace error", finalization: repository.PipelineRunFinalization{Status: domain.PipelineStatusCancelled, CompletedAt: now, ErrorMessage: "  "}},
		{name: "zero completion", finalization: repository.PipelineRunFinalization{Status: domain.PipelineStatusCompleted}},
		{name: "invalid signal", finalization: repository.PipelineRunFinalization{Status: domain.PipelineStatusCompleted, CompletedAt: now, Signal: &invalidSignal}},
		{name: "invalid timings", finalization: repository.PipelineRunFinalization{Status: domain.PipelineStatusCompleted, CompletedAt: now, PhaseTimings: json.RawMessage(`{`)}},
		{name: "event wrong run", finalization: repository.PipelineRunFinalization{Status: domain.PipelineStatusCompleted, CompletedAt: now, Event: &domain.AgentEvent{PipelineRunID: &otherRunID, EventKind: "pipeline_completed"}}},
		{name: "event wrong outcome", finalization: repository.PipelineRunFinalization{Status: domain.PipelineStatusCompleted, CompletedAt: now, Event: &domain.AgentEvent{PipelineRunID: &runID, EventKind: "pipeline_failed"}}},
		{name: "event invalid metadata", finalization: repository.PipelineRunFinalization{Status: domain.PipelineStatusCompleted, CompletedAt: now, Event: &domain.AgentEvent{PipelineRunID: &runID, EventKind: "pipeline_completed", Metadata: json.RawMessage(`{`)}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validatePipelineRunFinalization(runID, tt.finalization); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidatePipelineRunFinalization_CancelledEvent(t *testing.T) {
	runID := uuid.New()
	value := repository.PipelineRunFinalization{Status: domain.PipelineStatusCancelled, CompletedAt: time.Now(), ErrorMessage: "operator", Event: &domain.AgentEvent{PipelineRunID: &runID, EventKind: "pipeline_cancelled"}}
	if err := validatePipelineRunFinalization(runID, value); err != nil {
		t.Fatal(err)
	}
	value.Event.EventKind = "pipeline_failed"
	if err := validatePipelineRunFinalization(runID, value); err == nil {
		t.Fatal("pipeline_failed accepted for cancelled status")
	}
}

func TestPipelineRunRepoIntegration_FinalizationReceiptsAndSignalPreservation(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newPipelineRunIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewPipelineRunRepo(pool)
	run := createRunningPipelineRun(t, ctx, repo, domain.PipelineSignalBuy)
	completedAt := run.StartedAt.Add(time.Minute)
	runID := run.ID
	event := &domain.AgentEvent{PipelineRunID: &runID, EventKind: "pipeline_completed", Title: "done", Metadata: json.RawMessage(`{"ok":true}`)}

	receipt, err := repo.Finalize(ctx, run.ID, run.TradeDate, repository.PipelineRunFinalization{
		Status: domain.PipelineStatusCompleted, CompletedAt: completedAt,
		PhaseTimings: json.RawMessage(`{"analysis_ms":12}`), Event: event,
	})
	if err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if !receipt.Applied || receipt.Run.Status != domain.PipelineStatusCompleted || receipt.Run.Signal != domain.PipelineSignalBuy {
		t.Fatalf("unexpected applied receipt: %+v", receipt)
	}
	if receipt.Run.CompletedAt == nil || !receipt.Run.CompletedAt.Equal(completedAt) || !jsonBytesEqual(receipt.Run.PhaseTimings, json.RawMessage(`{"analysis_ms":12}`)) {
		t.Fatalf("receipt omitted terminal fields: %+v", receipt.Run)
	}
	var eventCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_events WHERE pipeline_run_id = $1`, run.ID).Scan(&eventCount); err != nil || eventCount != 1 {
		t.Fatalf("terminal event count = %d, err = %v", eventCount, err)
	}

	loser, err := repo.Finalize(ctx, run.ID, run.TradeDate, repository.PipelineRunFinalization{
		Status: domain.PipelineStatusFailed, CompletedAt: completedAt.Add(time.Second), ErrorMessage: "late failure",
	})
	if err != nil {
		t.Fatalf("losing Finalize() error = %v", err)
	}
	if loser.Applied || loser.Run.Status != receipt.Run.Status || loser.Run.ErrorMessage != receipt.Run.ErrorMessage || loser.Run.Signal != receipt.Run.Signal {
		t.Fatalf("loser did not receive canonical winner: %+v", loser)
	}
}

func TestPipelineRunRepoIntegration_CreatePreservesIDAndGeneratesMissingID(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newPipelineRunIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewPipelineRunRepo(pool)
	tradeDate := time.Date(2026, time.March, 14, 0, 0, 0, 0, time.UTC)

	providedID := uuid.New()
	provided := &domain.PipelineRun{ID: providedID, StrategyID: uuid.New(), Ticker: "AAPL", TradeDate: tradeDate, Status: domain.PipelineStatusRunning, StartedAt: time.Now().UTC()}
	if err := repo.Create(ctx, provided); err != nil {
		t.Fatal(err)
	}
	if provided.ID != providedID {
		t.Fatalf("Create() ID = %s, want caller ID %s", provided.ID, providedID)
	}
	stored, err := repo.GetByID(ctx, providedID)
	if err != nil || stored.ID != providedID {
		t.Fatalf("GetByID() = (%+v, %v), want durable caller ID", stored, err)
	}

	generated := &domain.PipelineRun{StrategyID: uuid.New(), Ticker: "MSFT", TradeDate: tradeDate, Status: domain.PipelineStatusRunning, StartedAt: time.Now().UTC()}
	if err := repo.Create(ctx, generated); err != nil {
		t.Fatal(err)
	}
	if generated.ID == uuid.Nil {
		t.Fatal("Create() left missing ID at zero")
	}
	if _, err := repo.GetByID(ctx, generated.ID); err != nil {
		t.Fatalf("generated ID %s was not durable: %v", generated.ID, err)
	}
}

func TestPipelineRunRepoIntegration_RegisteredIDSurvivesCreateAndCancel(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newPipelineRunIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewPipelineRunRepo(pool)
	run := &domain.PipelineRun{ID: uuid.New(), StrategyID: uuid.New(), Ticker: "AAPL", TradeDate: time.Date(2026, time.March, 14, 0, 0, 0, 0, time.UTC), Status: domain.PipelineStatusRunning, StartedAt: time.Now().UTC()}
	registry := agent.NewRunContextRegistry()
	runCtx, cancelRun := context.WithCancelCause(context.Background())
	if err := registry.Register(run.ID, run.TradeDate, cancelRun); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := service.NewRunService(repo, registry).Cancel(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(context.Cause(runCtx), runcontrol.Operator) {
		t.Fatalf("registry cancellation cause = %v, want operator", context.Cause(runCtx))
	}
	stored, err := repo.GetByID(ctx, run.ID)
	if err != nil || stored.Status != domain.PipelineStatusCancelled {
		t.Fatalf("durable run = (%+v, %v), want cancelled registered ID", stored, err)
	}
}

func TestPipelineRunRepoIntegration_FinalizationMissingAndEventRollback(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newPipelineRunIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewPipelineRunRepo(pool)
	tradeDate := time.Date(2026, time.March, 14, 0, 0, 0, 0, time.UTC)
	_, err := repo.Finalize(ctx, uuid.New(), tradeDate, repository.PipelineRunFinalization{
		Status: domain.PipelineStatusCompleted, CompletedAt: time.Now(),
	})
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("Finalize() missing error = %v, want ErrNotFound", err)
	}

	run := createRunningPipelineRun(t, ctx, repo, domain.PipelineSignalHold)
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_terminal_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'event rejected'; END $$`); err != nil {
		t.Fatalf("create rejecting trigger function: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE TRIGGER reject_terminal_event BEFORE INSERT ON agent_events FOR EACH ROW EXECUTE FUNCTION reject_terminal_event()`); err != nil {
		t.Fatalf("create rejecting trigger: %v", err)
	}
	runID := run.ID
	_, err = repo.Finalize(ctx, run.ID, run.TradeDate, repository.PipelineRunFinalization{
		Status: domain.PipelineStatusCompleted, CompletedAt: time.Now(),
		Event: &domain.AgentEvent{PipelineRunID: &runID, EventKind: "pipeline_completed", Title: "done"},
	})
	if err == nil {
		t.Fatal("Finalize() succeeded despite event insert failure")
	}
	durable, getErr := repo.Get(ctx, run.ID, run.TradeDate)
	if getErr != nil || durable.Status != domain.PipelineStatusRunning || durable.CompletedAt != nil {
		t.Fatalf("event failure did not roll back run: run=%+v err=%v", durable, getErr)
	}
}

func TestPipelineRunRepoIntegration_ConcurrentFinalizersHaveOneWinner(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newPipelineRunIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewPipelineRunRepo(pool)
	run := createRunningPipelineRun(t, ctx, repo, domain.PipelineSignalHold)
	inputs := []repository.PipelineRunFinalization{
		{Status: domain.PipelineStatusCompleted, CompletedAt: time.Now()},
		{Status: domain.PipelineStatusFailed, CompletedAt: time.Now().Add(time.Second), ErrorMessage: "failed"},
	}
	type result struct {
		receipt repository.PipelineRunFinalizationReceipt
		err     error
	}
	results := make(chan result, len(inputs))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, input := range inputs {
		wg.Add(1)
		go func(input repository.PipelineRunFinalization) {
			defer wg.Done()
			<-start
			receipt, err := repo.Finalize(ctx, run.ID, run.TradeDate, input)
			results <- result{receipt: receipt, err: err}
		}(input)
	}
	close(start)
	wg.Wait()
	close(results)
	applied := 0
	var winner domain.PipelineStatus
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent Finalize() error = %v", result.err)
		}
		if winner == "" {
			winner = result.receipt.Run.Status
		}
		if result.receipt.Run.Status != winner {
			t.Fatalf("receipts disagree on winner: %q and %q", winner, result.receipt.Run.Status)
		}
		if result.receipt.Applied {
			applied++
		}
	}
	if applied != 1 {
		t.Fatalf("applied finalizers = %d, want 1", applied)
	}
}

func TestPipelineRunRepoIntegration_RefineCompletedSignal(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newPipelineRunIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewPipelineRunRepo(pool)
	run := createRunningPipelineRun(t, ctx, repo, domain.PipelineSignalBuy)
	running, err := repo.RefineCompletedSignal(ctx, run.ID, run.TradeDate, domain.PipelineSignalBuy, domain.PipelineSignalSell)
	if err != nil || running.Applied || running.Run.Status != domain.PipelineStatusRunning || running.Run.Signal != domain.PipelineSignalBuy {
		t.Fatalf("running signal refinement = %+v, %v", running, err)
	}
	completedAt := time.Now()
	finalized, err := repo.Finalize(ctx, run.ID, run.TradeDate, repository.PipelineRunFinalization{Status: domain.PipelineStatusCompleted, CompletedAt: completedAt})
	if err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}

	refined, err := repo.RefineCompletedSignal(ctx, run.ID, run.TradeDate, domain.PipelineSignalBuy, domain.PipelineSignalSell)
	if err != nil || !refined.Applied || refined.Run.Signal != domain.PipelineSignalSell {
		t.Fatalf("RefineCompletedSignal() = %+v, %v", refined, err)
	}
	if refined.Run.Status != finalized.Run.Status || refined.Run.ErrorMessage != finalized.Run.ErrorMessage || refined.Run.CompletedAt == nil || !refined.Run.CompletedAt.Equal(*finalized.Run.CompletedAt) {
		t.Fatalf("signal refinement altered terminal fields: before=%+v after=%+v", finalized.Run, refined.Run)
	}
	retry, err := repo.RefineCompletedSignal(ctx, run.ID, run.TradeDate, domain.PipelineSignalBuy, domain.PipelineSignalSell)
	if err != nil || !retry.Applied {
		t.Fatalf("same-value retry = %+v, %v", retry, err)
	}
	loser, err := repo.RefineCompletedSignal(ctx, run.ID, run.TradeDate, domain.PipelineSignalHold, domain.PipelineSignalBuy)
	if err != nil || loser.Applied || loser.Run.Signal != domain.PipelineSignalSell {
		t.Fatalf("refinement CAS loser = %+v, %v", loser, err)
	}
	_, err = repo.RefineCompletedSignal(ctx, uuid.New(), run.TradeDate, "", domain.PipelineSignalBuy)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("missing refinement error = %v, want ErrNotFound", err)
	}
}

func createRunningPipelineRun(t *testing.T, ctx context.Context, repo *PipelineRunRepo, signal domain.PipelineSignal) *domain.PipelineRun {
	t.Helper()
	run := &domain.PipelineRun{
		StrategyID: uuid.New(), Ticker: "AAPL",
		TradeDate: time.Date(2026, time.March, 14, 0, 0, 0, 0, time.UTC),
		Status:    domain.PipelineStatusRunning, Signal: signal, StartedAt: time.Now(),
	}
	if err := repo.Create(ctx, run); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return run
}

func TestPipelineRunRepoIntegration_CRUDAndFilters(t *testing.T) {
	t.Helper()

	ctx := context.Background()
	pool, cleanup := newPipelineRunIntegrationPool(t, ctx)
	defer cleanup()

	repo := NewPipelineRunRepo(pool)
	strategyID := uuid.New()

	tradeDate1 := time.Date(2026, time.March, 14, 0, 0, 0, 0, time.UTC)
	tradeDate2 := time.Date(2026, time.March, 15, 0, 0, 0, 0, time.UTC)
	tradeDate3 := time.Date(2027, time.January, 3, 0, 0, 0, 0, time.UTC)
	startedAt1 := time.Date(2026, time.March, 14, 9, 30, 0, 0, time.UTC)
	startedAt2 := time.Date(2026, time.March, 15, 10, 0, 0, 0, time.UTC)
	startedAt3 := time.Date(2027, time.January, 3, 11, 0, 0, 0, time.UTC)

	run1 := &domain.PipelineRun{
		StrategyID:     strategyID,
		Ticker:         "AAPL",
		TradeDate:      tradeDate1,
		Status:         domain.PipelineStatusRunning,
		Signal:         domain.PipelineSignalBuy,
		StartedAt:      startedAt1,
		ConfigSnapshot: json.RawMessage(`{"window":20}`),
	}
	run2 := &domain.PipelineRun{
		StrategyID: strategyID,
		Ticker:     "AAPL",
		TradeDate:  tradeDate2,
		Status:     domain.PipelineStatusFailed,
		Signal:     domain.PipelineSignalSell,
		StartedAt:  startedAt2,
	}
	run3 := &domain.PipelineRun{
		StrategyID: uuid.New(),
		Ticker:     "MSFT",
		TradeDate:  tradeDate3,
		Status:     domain.PipelineStatusCompleted,
		Signal:     domain.PipelineSignalHold,
		StartedAt:  startedAt3,
	}

	for _, run := range []*domain.PipelineRun{run1, run2, run3} {
		if err := repo.Create(ctx, run); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if run.ID == uuid.Nil {
			t.Fatal("expected Create() to populate ID")
		}
	}

	byID1, err := repo.GetByID(ctx, run1.ID)
	if err != nil {
		t.Fatalf("GetByID() run1 error = %v", err)
	}
	if byID1.ID != run1.ID || byID1.TradeDate.Format("2006-01-02") != run1.TradeDate.Format("2006-01-02") {
		t.Fatalf("GetByID() run1 returned unexpected row: %+v", byID1)
	}

	byID2, err := repo.GetByID(ctx, run2.ID)
	if err != nil {
		t.Fatalf("GetByID() run2 error = %v", err)
	}
	if byID2.ID != run2.ID || byID2.TradeDate.Format("2006-01-02") != run2.TradeDate.Format("2006-01-02") {
		t.Fatalf("GetByID() run2 returned unexpected row: %+v", byID2)
	}

	got, err := repo.Get(ctx, run1.ID, run1.TradeDate)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if got.StrategyID != run1.StrategyID || got.Signal != run1.Signal || !jsonBytesEqual(got.ConfigSnapshot, run1.ConfigSnapshot) {
		t.Fatalf("Get() returned unexpected run: %+v", got)
	}

	count, err := repo.Count(ctx, repository.PipelineRunFilter{Status: domain.PipelineStatusCompleted})
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	var sqlCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM pipeline_runs WHERE status = $1`, domain.PipelineStatusCompleted).Scan(&sqlCount); err != nil {
		t.Fatalf("direct SQL count error = %v", err)
	}
	if count != sqlCount {
		t.Fatalf("Count() = %d, direct SQL = %d", count, sqlCount)
	}

	completedAt := startedAt1.Add(30 * time.Minute)
	if _, err := repo.Finalize(ctx, run1.ID, run1.TradeDate, repository.PipelineRunFinalization{
		Status:       domain.PipelineStatusCompleted,
		CompletedAt:  completedAt,
		ErrorMessage: "",
	}); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}

	updated, err := repo.Get(ctx, run1.ID, run1.TradeDate)
	if err != nil {
		t.Fatalf("Get() after update error = %v", err)
	}

	if updated.Status != domain.PipelineStatusCompleted {
		t.Fatalf("expected updated status %q, got %q", domain.PipelineStatusCompleted, updated.Status)
	}

	if updated.CompletedAt == nil || !updated.CompletedAt.Equal(completedAt) {
		t.Fatalf("expected completed_at %v, got %v", completedAt, updated.CompletedAt)
	}

	filteredByStrategy, err := repo.List(ctx, repository.PipelineRunFilter{
		StrategyID: &strategyID,
	}, 10, 0)
	if err != nil {
		t.Fatalf("List() by strategy error = %v", err)
	}

	if len(filteredByStrategy) != 2 {
		t.Fatalf("expected 2 runs for strategy filter, got %d", len(filteredByStrategy))
	}

	filteredByStatus, err := repo.List(ctx, repository.PipelineRunFilter{
		Status: domain.PipelineStatusCompleted,
	}, 10, 0)
	if err != nil {
		t.Fatalf("List() by status error = %v", err)
	}

	if len(filteredByStatus) != 2 {
		t.Fatalf("expected 2 completed runs, got %d", len(filteredByStatus))
	}

	filteredByTradeDate, err := repo.List(ctx, repository.PipelineRunFilter{
		TradeDate: &tradeDate2,
	}, 10, 0)
	if err != nil {
		t.Fatalf("List() by trade date error = %v", err)
	}

	if len(filteredByTradeDate) != 1 || filteredByTradeDate[0].ID != run2.ID {
		t.Fatalf("expected trade date filter to return run2, got %+v", filteredByTradeDate)
	}

	startedAfter := startedAt1.Add(15 * time.Minute)
	startedBefore := startedAt3.Add(-15 * time.Minute)
	filteredByWindow, err := repo.List(ctx, repository.PipelineRunFilter{
		StartedAfter:  &startedAfter,
		StartedBefore: &startedBefore,
	}, 10, 0)
	if err != nil {
		t.Fatalf("List() by started_at window error = %v", err)
	}

	if len(filteredByWindow) != 1 || filteredByWindow[0].ID != run2.ID {
		t.Fatalf("expected time-window filter to return run2, got %+v", filteredByWindow)
	}
}

func TestPipelineRunRepoIntegration_GetByIDUsesRunIDOnly(t *testing.T) {
	t.Helper()

	ctx := context.Background()
	pool, cleanup := newPipelineRunIntegrationPool(t, ctx)
	defer cleanup()

	repo := NewPipelineRunRepo(pool)
	sharedID := uuid.New()
	tradeDate1 := time.Date(2026, time.March, 14, 0, 0, 0, 0, time.UTC)
	tradeDate2 := time.Date(2027, time.January, 3, 0, 0, 0, 0, time.UTC)
	startedAt1 := time.Date(2026, time.March, 14, 9, 30, 0, 0, time.UTC)
	startedAt2 := time.Date(2027, time.January, 3, 11, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		tradeDate time.Time
		ticker    string
		status    domain.PipelineStatus
		startedAt time.Time
	}{
		{tradeDate: tradeDate1, ticker: "AAPL", status: domain.PipelineStatusRunning, startedAt: startedAt1},
		{tradeDate: tradeDate2, ticker: "MSFT", status: domain.PipelineStatusFailed, startedAt: startedAt2},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO pipeline_runs (
			id, strategy_id, ticker, trade_date, status, signal, started_at, error_message
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			sharedID,
			uuid.New(),
			tc.ticker,
			tc.tradeDate,
			tc.status,
			"",
			tc.startedAt,
			"",
		); err != nil {
			t.Fatalf("failed to seed duplicate id rows: %v", err)
		}
	}

	got, err := repo.GetByID(ctx, sharedID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}

	if got.Ticker != "MSFT" || got.TradeDate.Format("2006-01-02") != tradeDate2.Format("2006-01-02") {
		t.Fatalf("expected GetByID() to return the newest row for shared ID, got %+v", got)
	}
}

func TestPipelineRunRepoIntegration_NotFound(t *testing.T) {
	t.Helper()

	ctx := context.Background()
	pool, cleanup := newPipelineRunIntegrationPool(t, ctx)
	defer cleanup()

	repo := NewPipelineRunRepo(pool)
	missingID := uuid.New()

	missingTradeDate := time.Date(2026, time.March, 14, 0, 0, 0, 0, time.UTC)
	_, err := repo.GetByID(ctx, missingID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected GetByID() ErrNotFound, got %v", err)
	}

	_, err = repo.Get(ctx, missingID, missingTradeDate)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected Get() ErrNotFound, got %v", err)
	}

	_, err = repo.Finalize(ctx, missingID, missingTradeDate, repository.PipelineRunFinalization{
		Status: domain.PipelineStatusFailed, CompletedAt: time.Now().UTC(), ErrorMessage: "failed",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected Finalize() ErrNotFound, got %v", err)
	}
}

func TestPipelineRunRepoIntegration_UsesCompositeKey(t *testing.T) {
	t.Helper()

	ctx := context.Background()
	pool, cleanup := newPipelineRunIntegrationPool(t, ctx)
	defer cleanup()

	repo := NewPipelineRunRepo(pool)
	sharedID := uuid.New()
	tradeDate1 := time.Date(2026, time.March, 14, 0, 0, 0, 0, time.UTC)
	tradeDate2 := time.Date(2027, time.January, 3, 0, 0, 0, 0, time.UTC)
	startedAt1 := time.Date(2026, time.March, 14, 9, 30, 0, 0, time.UTC)
	startedAt2 := time.Date(2027, time.January, 3, 11, 0, 0, 0, time.UTC)

	insertSQL := `INSERT INTO pipeline_runs (
		id, strategy_id, ticker, trade_date, status, signal, started_at, error_message
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	for _, tc := range []struct {
		tradeDate time.Time
		ticker    string
		status    domain.PipelineStatus
		startedAt time.Time
	}{
		{tradeDate: tradeDate1, ticker: "AAPL", status: domain.PipelineStatusRunning, startedAt: startedAt1},
		{tradeDate: tradeDate2, ticker: "MSFT", status: domain.PipelineStatusFailed, startedAt: startedAt2},
	} {
		if _, err := pool.Exec(ctx, insertSQL,
			sharedID,
			uuid.New(),
			tc.ticker,
			tc.tradeDate,
			tc.status,
			"",
			tc.startedAt,
			"",
		); err != nil {
			t.Fatalf("failed to seed duplicate id rows: %v", err)
		}
	}

	got, err := repo.Get(ctx, sharedID, tradeDate2)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if got.Ticker != "MSFT" || got.TradeDate.Format("2006-01-02") != tradeDate2.Format("2006-01-02") {
		t.Fatalf("expected Get() to return the row for trade date %s, got %+v", tradeDate2.Format("2006-01-02"), got)
	}

	completedAt := startedAt1.Add(time.Hour)
	if _, err := repo.Finalize(ctx, sharedID, tradeDate1, repository.PipelineRunFinalization{
		Status:       domain.PipelineStatusCompleted,
		CompletedAt:  completedAt,
		ErrorMessage: "",
	}); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}

	firstRun, err := repo.Get(ctx, sharedID, tradeDate1)
	if err != nil {
		t.Fatalf("Get() for first run error = %v", err)
	}
	secondRun, err := repo.Get(ctx, sharedID, tradeDate2)
	if err != nil {
		t.Fatalf("Get() for second run error = %v", err)
	}

	if firstRun.Status != domain.PipelineStatusCompleted {
		t.Fatalf("expected first run status completed, got %q", firstRun.Status)
	}
	if secondRun.Status != domain.PipelineStatusFailed {
		t.Fatalf("expected second run status to remain failed, got %q", secondRun.Status)
	}
}

func newPipelineRunIntegrationPool(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	connString := os.Getenv("DB_URL")
	if connString == "" {
		connString = os.Getenv("DATABASE_URL")
	}
	if connString == "" {
		t.Skip("skipping integration test: DB_URL or DATABASE_URL is not set")
	}

	adminPool, err := pgxpool.New(ctx, connString)
	if err != nil {
		t.Fatalf("failed to create admin pool: %v", err)
	}

	if _, err := adminPool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pgcrypto`); err != nil {
		adminPool.Close()
		t.Fatalf("failed to ensure pgcrypto extension: %v", err)
	}

	schemaName := "integration_pipeline_run_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	if _, err := adminPool.Exec(ctx, `CREATE SCHEMA "`+schemaName+`"`); err != nil {
		adminPool.Close()
		t.Fatalf("failed to create test schema: %v", err)
	}

	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		_, _ = adminPool.Exec(ctx, `DROP SCHEMA "`+schemaName+`" CASCADE`)
		adminPool.Close()
		t.Fatalf("failed to parse pool config: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schemaName + ",public"

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = adminPool.Exec(ctx, `DROP SCHEMA "`+schemaName+`" CASCADE`)
		adminPool.Close()
		t.Fatalf("failed to create test pool: %v", err)
	}

	ddl := []string{
		`CREATE TYPE pipeline_status AS ENUM ('running', 'completed', 'failed', 'cancelled')`,
		`CREATE TABLE pipeline_runs (
			id              UUID            NOT NULL DEFAULT gen_random_uuid(),
			strategy_id     UUID            NOT NULL,
			ticker          TEXT            NOT NULL,
			trade_date      DATE            NOT NULL,
			status          pipeline_status NOT NULL DEFAULT 'running',
			signal          TEXT            NOT NULL DEFAULT '',
			started_at      TIMESTAMPTZ     NOT NULL,
			completed_at    TIMESTAMPTZ,
			error_message   TEXT            NOT NULL DEFAULT '',
			config_snapshot JSONB,
			phase_timings   JSONB,
			PRIMARY KEY (id, trade_date)
		) PARTITION BY RANGE (trade_date)`,
		`CREATE TABLE pipeline_runs_2026_q1 PARTITION OF pipeline_runs
			FOR VALUES FROM ('2026-01-01') TO ('2026-04-01')`,
		`CREATE TABLE pipeline_runs_default PARTITION OF pipeline_runs DEFAULT`,
		`CREATE INDEX idx_pipeline_runs_strategy_id ON pipeline_runs (strategy_id)`,
		`CREATE INDEX idx_pipeline_runs_ticker ON pipeline_runs (ticker)`,
		`CREATE INDEX idx_pipeline_runs_status ON pipeline_runs (status)`,
		`CREATE INDEX idx_pipeline_runs_trade_date ON pipeline_runs (trade_date)`,
		`CREATE TABLE agent_events (
			id UUID NOT NULL DEFAULT gen_random_uuid(), pipeline_run_id UUID, strategy_id UUID,
			agent_role TEXT, event_kind TEXT NOT NULL, title TEXT NOT NULL, summary TEXT,
			tags TEXT[], metadata JSONB, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (id, created_at)
		) PARTITION BY RANGE (created_at)`,
		`CREATE TABLE agent_events_default PARTITION OF agent_events DEFAULT`,
	}

	for _, stmt := range ddl {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			pool.Close()
			_, _ = adminPool.Exec(ctx, `DROP SCHEMA "`+schemaName+`" CASCADE`)
			adminPool.Close()
			t.Fatalf("failed to apply test schema DDL: %v", err)
		}
	}

	cleanup := func() {
		pool.Close()
		_, _ = adminPool.Exec(ctx, `DROP SCHEMA "`+schemaName+`" CASCADE`)
		adminPool.Close()
	}

	return pool, cleanup
}
