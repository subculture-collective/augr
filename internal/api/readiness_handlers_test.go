package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/risk"
)

type stubKillSwitchState struct{ state risk.KillSwitchState }

func (s stubKillSwitchState) KillSwitchState() risk.KillSwitchState { return s.state }

func newReadinessServer(t *testing.T, deps Deps) *Server {
	t.Helper()
	return newTestServerWithDeps(t, deps)
}

func TestReadyzReportsReadyWhenAllChecksPass(t *testing.T) {
	t.Parallel()

	deps := testDeps()
	deps.Readiness = ReadinessDeps{
		SchemaCheck:       HealthCheckFunc(func(context.Context) error { return nil }),
		KillSwitch:        stubKillSwitchState{},
		SchedulerRequired: true,
		SchedulerPresent:  true,
		Automation:        func() AutomationReadiness { return AutomationReadiness{Present: true, TotalJobs: 3} },
		LLMConfigured:     true,
	}
	srv := newReadinessServer(t, deps)

	rr := doRequest(t, srv, http.MethodGet, "/readyz", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	body := decodeJSON[readinessResponse](t, rr)
	if body.Status != "ready" || len(body.Failing) != 0 {
		t.Fatalf("body = %+v", body)
	}
	for _, check := range []string{"db", "schema", "kill_switch", "scheduler", "automation", "llm"} {
		if body.Checks[check] != "ok" {
			t.Fatalf("check %s = %q, want ok (%+v)", check, body.Checks[check], body.Checks)
		}
	}
}

func TestReadyzListsFailingChecks(t *testing.T) {
	t.Parallel()

	deps := testDeps()
	deps.DBHealth = &stubHealthCheck{err: errors.New("db unavailable")}
	deps.Readiness = ReadinessDeps{
		SchemaCheck: HealthCheckFunc(func(context.Context) error { return errors.New("schema 113 behind required 114") }),
		KillSwitch: stubKillSwitchState{state: risk.KillSwitchState{
			Active: true, Reason: "risk state restore failed: corrupt", RestoreFailed: true, RestoreError: "corrupt",
		}},
		SchedulerRequired: true,
		SchedulerPresent:  false,
		Automation: func() AutomationReadiness {
			return AutomationReadiness{Present: true, Degraded: true, Reason: "all automation jobs are disabled", DisabledJobs: []string{"a", "b"}}
		},
		LLMConfigured: false,
	}
	srv := newReadinessServer(t, deps)

	rr := doRequest(t, srv, http.MethodGet, "/readyz", nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	body := decodeJSON[readinessResponse](t, rr)
	if body.Status != "not_ready" {
		t.Fatalf("status = %q", body.Status)
	}
	joined := strings.Join(body.Failing, "\n")
	for _, want := range []string{
		"db: db unavailable",
		"schema: schema 113 behind required 114",
		"kill_switch: active: risk state restore failed: corrupt (restore_failed=true)",
		"scheduler: ENABLE_SCHEDULER=true but no scheduler was constructed",
		"automation: all automation jobs are disabled; disabled_jobs=a,b",
		"llm: no default LLM provider configured",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("failing = %v, missing %q", body.Failing, want)
		}
	}
}

func TestReadyzSkipsSchedulerChecksWhenNotRequired(t *testing.T) {
	t.Parallel()

	deps := testDeps()
	deps.Readiness = ReadinessDeps{LLMConfigured: true}
	srv := newReadinessServer(t, deps)

	rr := doRequest(t, srv, http.MethodGet, "/readyz", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	body := decodeJSON[readinessResponse](t, rr)
	if _, ok := body.Checks["scheduler"]; ok {
		t.Fatalf("scheduler check present when not required: %+v", body.Checks)
	}
	if _, ok := body.Checks["automation"]; ok {
		t.Fatalf("automation check present when not required: %+v", body.Checks)
	}
}

func TestReadyzIsUnauthenticated(t *testing.T) {
	t.Parallel()

	deps := testDeps()
	deps.Readiness = ReadinessDeps{LLMConfigured: true}
	srv := newReadinessServer(t, deps)
	rr := doRequest(t, srv, http.MethodGet, "/readyz", nil)
	if rr.Code == http.StatusUnauthorized {
		t.Fatal("/readyz must not require authentication")
	}
}

func TestAutomationReadinessOfNilOrchestrator(t *testing.T) {
	t.Parallel()
	got := AutomationReadinessOf(nil)
	if got.Present || got.Degraded || got.DisabledJobs == nil || got.UnavailableJobs == nil {
		t.Fatalf("AutomationReadinessOf(nil) = %+v", got)
	}
}

func TestRiskStatusIncludesRestoreDiagnostics(t *testing.T) {
	t.Parallel()

	engine := &restoreFailedRiskEngine{}
	srv := &Server{risk: engine}
	rec := httptest.NewRecorder()
	srv.handleRiskStatus(rec, httptest.NewRequest(http.MethodGet, "/risk/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeJSON[map[string]any](t, rec)
	if body["kill_switch_restore_failed"] != true || body["kill_switch_restore_error"] != "corrupt state" {
		t.Fatalf("body = %v", body)
	}
	if ks, ok := body["kill_switch"].(map[string]any); !ok || ks["active"] != true {
		t.Fatalf("kill_switch = %v", body["kill_switch"])
	}
}

type restoreFailedRiskEngine struct{ stubRiskEngine }

func (restoreFailedRiskEngine) GetStatus(context.Context) (risk.EngineStatus, error) {
	return risk.EngineStatus{KillSwitch: risk.KillSwitchStatus{Active: true, Reason: "risk state restore failed: corrupt state"}, UpdatedAt: time.Now()}, nil
}

func (restoreFailedRiskEngine) KillSwitchState() risk.KillSwitchState {
	return risk.KillSwitchState{Active: true, Reason: "risk state restore failed: corrupt state", RestoreFailed: true, RestoreError: "corrupt state"}
}

func TestManualRunFailurePersistsAgentEvent(t *testing.T) {
	t.Parallel()

	events := &syncEventRepo{}
	deps := testDeps()
	deps.Events = events
	deps.Runner = &stubStrategyRunner{err: errors.New("preparation rejected: fundamentals_missing")}
	profile, err := domain.NewPaperEvaluationProfile(domain.PaperEvaluationModeScored, 100000, 2, 5, 0.0001)
	if err != nil {
		t.Fatal(err)
	}
	deps.PaperEvaluation = &profile
	srv := newTestServerWithDeps(t, deps)

	rr := doRequest(t, srv, http.MethodPost, "/api/v1/accounts/00000000-0000-4000-8000-000000000064/strategies/"+stratA.ID.String()+"/run", nil)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	body := decodeJSON[StrategyRunAccepted](t, rr)
	if body.Status != "accepted" || body.StrategyID != stratA.ID.String() {
		t.Fatalf("receipt = %+v", body)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && events.count() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if events.count() != 1 {
		t.Fatalf("events = %d, want 1 persisted failure", events.count())
	}
	event := events.first()
	if event.EventKind != "strategy.manual_run_failed" || event.StrategyID == nil || *event.StrategyID != stratA.ID {
		t.Fatalf("event = %+v", event)
	}
	if event.Environment != domain.AccountEnvironmentPaperScored {
		t.Fatalf("environment = %q", event.Environment)
	}
	if !strings.Contains(string(event.Metadata), "fundamentals_missing") || !strings.Contains(string(event.Metadata), "request_id") {
		t.Fatalf("metadata = %s", event.Metadata)
	}
}

// syncEventRepo records events from the asynchronous run goroutine safely.
type syncEventRepo struct {
	stubEventRepo
	mu sync.Mutex
}

func (s *syncEventRepo) Create(ctx context.Context, event *domain.AgentEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stubEventRepo.Create(ctx, event)
}

func (s *syncEventRepo) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

func (s *syncEventRepo) first() domain.AgentEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.events[0]
}
