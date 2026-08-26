package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/automation"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
	"github.com/go-chi/chi/v5"
)

type failingAutomationJobControlRepo struct{}

func (failingAutomationJobControlRepo) List(context.Context) ([]domain.AutomationJobControl, error) {
	return nil, nil
}

func (failingAutomationJobControlRepo) SetEnabled(context.Context, string, bool, string) error {
	return errors.New("database unavailable")
}

// newTestOrchestrator creates a minimal orchestrator with no DB deps.
func newTestOrchestrator() *automation.JobOrchestrator {
	return automation.NewJobOrchestrator(automation.OrchestratorDeps{})
}

// registerJob registers a no-op job on the orchestrator.
func registerJob(o *automation.JobOrchestrator, name string) {
	o.Register(name, "test job", scheduler.ScheduleSpec{Cron: "0 * * * *"},
		func(_ context.Context) error { return nil },
	)
}

// TestAutomationHealth verifies the handler returns 200 with valid JSON schema.
func TestAutomationHealth(t *testing.T) {
	t.Parallel()

	o := newTestOrchestrator()
	s := &Server{automation: o}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/automation/health", nil)
	rr := httptest.NewRecorder()
	s.handleGetAutomationHealth(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp AutomationHealthResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// TestAutomationHealthNilAutomation verifies 503 when automation is nil.
func TestAutomationHealthNilAutomation(t *testing.T) {
	t.Parallel()

	s := &Server{automation: nil}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/automation/health", nil)
	rr := httptest.NewRecorder()
	s.handleGetAutomationHealth(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rr.Code)
	}
}

// TestAutomationHealthAllHealthy verifies healthy=true when all jobs have 0 consecutive failures.
func TestAutomationHealthAllHealthy(t *testing.T) {
	t.Parallel()

	o := newTestOrchestrator()
	registerJob(o, "job-a")
	registerJob(o, "job-b")

	s := &Server{automation: o}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/automation/health", nil)
	rr := httptest.NewRecorder()
	s.handleGetAutomationHealth(rr, req)

	var resp AutomationHealthResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !resp.Healthy {
		t.Errorf("expected healthy=true, got false")
	}
	if resp.TotalJobs != 2 {
		t.Errorf("expected total_jobs=2, got %d", resp.TotalJobs)
	}
	if resp.FailingJobs != 0 {
		t.Errorf("expected failing_jobs=0, got %d", resp.FailingJobs)
	}
}

// TestAutomationHealthUnhealthy verifies healthy=false when any job has >=3 consecutive failures.
func TestAutomationHealthUnhealthy(t *testing.T) {
	t.Parallel()

	o := newTestOrchestrator()
	registerJob(o, "bad-job")
	registerJob(o, "good-job")

	o.SetConsecutiveFailures("bad-job", 3)

	s := &Server{automation: o}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/automation/health", nil)
	rr := httptest.NewRecorder()
	s.handleGetAutomationHealth(rr, req)

	var resp AutomationHealthResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Healthy {
		t.Errorf("expected healthy=false when job has >=3 consecutive failures")
	}
}

// TestAutomationHealthFailingJobsCount verifies failing_jobs and degraded_jobs counts are correct.
func TestAutomationHealthFailingJobsCount(t *testing.T) {
	t.Parallel()

	o := newTestOrchestrator()
	registerJob(o, "job-1")
	registerJob(o, "job-2")
	registerJob(o, "job-3")

	// job-1 and job-3 each have >= 1 but < 3 consecutive failures (degraded, not failing).
	o.SetConsecutiveFailures("job-1", 1)
	o.SetConsecutiveFailures("job-3", 2)

	s := &Server{automation: o}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/automation/health", nil)
	rr := httptest.NewRecorder()
	s.handleGetAutomationHealth(rr, req)

	var resp AutomationHealthResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// No job has >= 3 consecutive failures, so failing_jobs=0.
	if resp.FailingJobs != 0 {
		t.Errorf("expected failing_jobs=0 (none have >=3 consecutive failures), got %d", resp.FailingJobs)
	}
	if resp.DegradedJobs != 2 {
		t.Errorf("expected degraded_jobs=2 (job-1 and job-3 have 1-2 consecutive failures), got %d", resp.DegradedJobs)
	}
	if resp.Healthy {
		t.Errorf("expected healthy=false when jobs are degraded")
	}
}

func TestAutomationHealthClassifiesExplicitDegradedOutcome(t *testing.T) {
	o := newTestOrchestrator()
	o.Register("partial-job", "test job", scheduler.ScheduleSpec{Cron: "* * * * *", Type: scheduler.ScheduleTypeCron},
		func(context.Context) error { return automation.Degradedf("partial provider response") },
	)
	if err := o.RunJob(context.Background(), "partial-job"); err != nil {
		t.Fatalf("RunJob() error = %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if statuses := o.Status(); len(statuses) == 1 && statuses[0].RunCount == 1 && !statuses[0].Running {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if status := o.Status()[0]; status.RunCount != 1 || status.Running {
		t.Fatalf("degraded job did not complete: %+v", status)
	}

	s := &Server{automation: o}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/automation/health", nil)
	rr := httptest.NewRecorder()
	s.handleGetAutomationHealth(rr, req)

	var resp AutomationHealthResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Healthy || resp.DegradedJobs != 1 || resp.FailingJobs != 0 {
		t.Fatalf("degraded health = %+v", resp)
	}
	if len(resp.Jobs) != 1 || resp.Jobs[0].LastResult != "degraded" || resp.Jobs[0].LastDetail != "partial provider response" || resp.Jobs[0].LastError != "" || resp.Jobs[0].ErrorCount != 0 || resp.Jobs[0].ConsecutiveFailures != 0 {
		t.Fatalf("degraded job health = %+v", resp.Jobs)
	}
}

func TestAutomationStatusExposesDegradedDetailNotError(t *testing.T) {
	o := newTestOrchestrator()
	o.Register("partial-job", "test job", scheduler.ScheduleSpec{Cron: "* * * * *", Type: scheduler.ScheduleTypeCron},
		func(context.Context) error { return automation.Degradedf("partial provider response") },
	)
	if err := o.RunJob(context.Background(), "partial-job"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if status := o.Status()[0]; status.RunCount == 1 && !status.Running {
			break
		}
		time.Sleep(time.Millisecond)
	}

	s := &Server{automation: o}
	rr := httptest.NewRecorder()
	s.handleGetAutomationStatus(rr, httptest.NewRequest(http.MethodGet, "/api/v1/automation/status", nil))
	var statuses []automation.JobStatus
	if err := json.NewDecoder(rr.Body).Decode(&statuses); err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].LastDetail != "partial provider response" || statuses[0].LastError != "" {
		t.Fatalf("statuses = %+v", statuses)
	}
}

func TestAutomationStatusIncludesAlpacaReconcileLastSummary(t *testing.T) {
	t.Parallel()

	o := newTestOrchestrator()
	registerJob(o, "alpaca_reconcile")
	o.SetLastSummary("alpaca_reconcile", map[string]int{
		"orders_created":    2,
		"positions_created": 1,
		"trades_created":    3,
	})

	s := &Server{automation: o}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/automation/status", nil)
	rr := httptest.NewRecorder()
	s.handleGetAutomationStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var statuses []automation.JobStatus
	if err := json.NewDecoder(rr.Body).Decode(&statuses); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("len(statuses) = %d, want 1", len(statuses))
	}
	if statuses[0].Name != "alpaca_reconcile" {
		t.Fatalf("status name = %q, want alpaca_reconcile", statuses[0].Name)
	}
	if statuses[0].LastSummary == nil {
		t.Fatal("LastSummary = nil, want non-nil")
	}
	if statuses[0].LastSummary["orders_created"] != 2 {
		t.Fatalf("orders_created = %d, want 2", statuses[0].LastSummary["orders_created"])
	}
	if statuses[0].LastSummary["trades_created"] != 3 {
		t.Fatalf("trades_created = %d, want 3", statuses[0].LastSummary["trades_created"])
	}
}

func TestAutomationEnableAllowsKalshiPreviewSchedulingWithoutGate(t *testing.T) {
	o := automation.NewJobOrchestrator(automation.OrchestratorDeps{})
	o.Register("kalshi_settlement", "test", scheduler.ScheduleSpec{Cron: "0 * * * *"}, func(context.Context) error { return nil })
	if err := o.SetEnabled("kalshi_settlement", false); err != nil {
		t.Fatal(err)
	}
	if err := o.SetEnabled("kalshi_settlement", true); err != nil {
		t.Fatalf("preview scheduling must not require mutation gate: %v", err)
	}
	if err := o.SetEnabled("kalshi_settlement", false); err != nil {
		t.Fatal(err)
	}
	s := &Server{automation: o}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/automation/jobs/kalshi_settlement/enable", strings.NewReader(`{"enabled":true}`))
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("name", "kalshi_settlement")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	rr := httptest.NewRecorder()
	s.handleSetAutomationJobEnabled(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200: %s", rr.Code, rr.Body.String())
	}
}

func TestAutomationEnablePersistenceFailureIsServiceUnavailable(t *testing.T) {
	t.Parallel()

	o := automation.NewJobOrchestrator(automation.OrchestratorDeps{JobControlRepo: failingAutomationJobControlRepo{}})
	registerJob(o, "job")
	s := &Server{automation: o}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/automation/jobs/job/enable", strings.NewReader(`{"enabled":false}`))
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("name", "job")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	rr := httptest.NewRecorder()

	s.handleSetAutomationJobEnabled(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503: %s", rr.Code, rr.Body.String())
	}
}
