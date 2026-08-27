package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/eventmarkets"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	kalshiexecution "github.com/PatrickFanella/get-rich-quick/internal/execution/kalshi"
	polymarketexecution "github.com/PatrickFanella/get-rich-quick/internal/execution/polymarket"
	predictionexecution "github.com/PatrickFanella/get-rich-quick/internal/execution/prediction"
	kalshidiscovery "github.com/PatrickFanella/get-rich-quick/internal/kalshidiscovery"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	pgrepo "github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

func TestDiscoveryReadinessLockOmitsFiveJobsAndRecordsSortedDiagnostics(t *testing.T) {
	readiness := &DiscoveryReadiness{Reason: pgrepo.DiscoveryDeploymentUnavailableReason, Err: pgrepo.ErrDiscoveryDeploymentImmutableBinding}
	orch := NewJobOrchestrator(OrchestratorDeps{DiscoveryReadiness: readiness})
	orch.RegisterAll()

	for _, name := range discoveryDeploymentJobNames {
		if _, ok := orch.jobs[name]; ok {
			t.Fatalf("readiness-locked job %q registered", name)
		}
	}
	for _, name := range []string{"history_refresh", "overnight_sweep", "earnings_scanner", "filing_monitor", "position_review"} {
		if _, ok := orch.jobs[name]; !ok {
			t.Fatalf("unrelated job %q omitted", name)
		}
	}
	diagnostics := orch.UnavailableJobs()
	if len(diagnostics) != len(discoveryDeploymentJobNames) {
		t.Fatalf("unavailable diagnostics = %#v", diagnostics)
	}
	for i, diagnostic := range diagnostics {
		if i > 0 && diagnostics[i-1].Name >= diagnostic.Name {
			t.Fatalf("unavailable diagnostics not sorted: %#v", diagnostics)
		}
		if diagnostic.Reason != pgrepo.DiscoveryDeploymentUnavailableReason {
			t.Fatalf("unavailable reason = %q", diagnostic.Reason)
		}
	}
	statuses := orch.Status()
	for _, status := range statuses {
		for _, diagnostic := range diagnostics {
			if status.Name == diagnostic.Name {
				t.Fatalf("unavailable job %q exposed as runnable status", status.Name)
			}
		}
	}
}

func TestDiscoveryReadinessEvaluationFailureUsesGenericDiagnostics(t *testing.T) {
	readiness := &DiscoveryReadiness{Reason: pgrepo.DiscoveryDeploymentUnavailableReason, Err: errors.New("readiness database unavailable")}
	orch := NewJobOrchestrator(OrchestratorDeps{DiscoveryReadiness: readiness})
	orch.RegisterAll()

	diagnostics := orch.UnavailableJobs()
	if len(diagnostics) != len(discoveryDeploymentJobNames) {
		t.Fatalf("unavailable diagnostics = %#v", diagnostics)
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Reason != DiscoveryReadinessEvaluationErrorReason {
			t.Fatalf("evaluation failure reason = %q", diagnostic.Reason)
		}
	}
}

func TestJobOrchestratorRunJob_TracksFailureFieldsAndReset(t *testing.T) {
	t.Parallel()

	orch := NewJobOrchestrator(OrchestratorDeps{})
	shouldFail := true
	orch.Register("job", "test job", schedulerSpecEveryMinute(), func(context.Context) error {
		if shouldFail {
			return errors.New("boom")
		}
		return nil
	})

	if err := orch.RunJob(context.Background(), "job"); err != nil {
		t.Fatalf("RunJob(first) error = %v", err)
	}
	waitForJobRuns(t, orch, "job", 1)

	status := singleJobStatus(t, orch, "job")
	if status.LastResult != "failed" {
		t.Fatalf("LastResult = %q, want failed", status.LastResult)
	}
	if status.LastError != "boom" {
		t.Fatalf("LastError = %q, want boom", status.LastError)
	}
	if status.LastErrorAt == nil {
		t.Fatal("LastErrorAt = nil, want timestamp")
	}
	if status.ConsecutiveFailures != 1 {
		t.Fatalf("ConsecutiveFailures = %d, want 1", status.ConsecutiveFailures)
	}

	shouldFail = false
	if err := orch.RunJob(context.Background(), "job"); err != nil {
		t.Fatalf("RunJob(second) error = %v", err)
	}
	waitForJobRuns(t, orch, "job", 2)

	status = singleJobStatus(t, orch, "job")
	if status.LastResult != "success" {
		t.Fatalf("LastResult = %q, want success", status.LastResult)
	}
	if status.LastError != "" {
		t.Fatalf("LastError = %q, want empty", status.LastError)
	}
	if status.ConsecutiveFailures != 0 {
		t.Fatalf("ConsecutiveFailures = %d, want 0", status.ConsecutiveFailures)
	}
}

func TestJobOrchestrator_ContainsJobPanicsAsFailures(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		run  func(*JobOrchestrator, *RegisteredJob)
	}{
		{
			name: "manual",
			run: func(orch *JobOrchestrator, job *RegisteredJob) {
				orch.runDirect(job)
			},
		},
		{
			name: "scheduled",
			run: func(orch *JobOrchestrator, job *RegisteredJob) {
				orch.wrapAndRun(job)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			orch := NewJobOrchestrator(OrchestratorDeps{})
			orch.Register("job", "panicking job", schedulerSpecEveryMinute(), func(context.Context) error {
				panic("credential-shaped panic value must not be logged")
			})

			tc.run(orch, orch.jobs["job"])

			status := singleJobStatus(t, orch, "job")
			if status.Running {
				t.Fatal("Running = true after panic")
			}
			if status.RunCount != 1 || status.ErrorCount != 1 || status.ConsecutiveFailures != 1 {
				t.Fatalf("failure counters = runs:%d errors:%d consecutive:%d, want 1/1/1",
					status.RunCount, status.ErrorCount, status.ConsecutiveFailures)
			}
			if !strings.Contains(status.LastError, "job panicked (string)") {
				t.Fatalf("LastError = %q, want redacted panic type", status.LastError)
			}
			if strings.Contains(status.LastError, "credential-shaped") {
				t.Fatalf("LastError leaked panic value: %q", status.LastError)
			}
		})
	}
}

func TestJobOrchestratorStatus_IncludesStuckForWhenRunning(t *testing.T) {
	t.Parallel()

	orch := NewJobOrchestrator(OrchestratorDeps{})
	started := make(chan struct{})
	release := make(chan struct{})
	orch.Register("job", "blocking job", schedulerSpecEveryMinute(), func(context.Context) error {
		close(started)
		<-release
		return nil
	})

	if err := orch.RunJob(context.Background(), "job"); err != nil {
		t.Fatalf("RunJob() error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("job did not start")
	}

	status := singleJobStatus(t, orch, "job")
	if !status.Running {
		t.Fatal("Running = false, want true")
	}
	if status.StuckFor == nil || *status.StuckFor <= 0 {
		t.Fatalf("StuckFor = %v, want > 0", status.StuckFor)
	}

	close(release)
	waitForJobRuns(t, orch, "job", 1)
}

func TestJobOrchestratorPersistsRunningRowBeforeExecuting(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*JobOrchestrator, *RegisteredJob)
	}{
		{name: "manual", run: func(o *JobOrchestrator, job *RegisteredJob) { o.runDirect(job) }},
		{name: "scheduled", run: func(o *JobOrchestrator, job *RegisteredJob) { o.wrapAndRun(job) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := newRecordingAutomationJobRunRepo()
			started := make(chan struct{})
			release := make(chan struct{})
			done := make(chan struct{})
			orch := NewJobOrchestrator(OrchestratorDeps{JobRunRepo: repo})
			orch.Register("job", "durable running job", schedulerSpecEveryMinute(), func(context.Context) error {
				close(started)
				<-release
				return nil
			})

			go func() {
				defer close(done)
				tc.run(orch, orch.jobs["job"])
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("job did not start")
			}

			running := repo.singleRun(t)
			if running.Status != "running" || running.CompletedAt != nil || running.ID == uuid.Nil {
				t.Fatalf("running row = %+v", running)
			}

			close(release)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("job did not finish")
			}
			completed := repo.singleRun(t)
			if completed.ID != running.ID || completed.Status != "ok" || completed.CompletedAt == nil {
				t.Fatalf("completed row = %+v", completed)
			}
		})
	}
}

func TestJobOrchestratorPersistsDegradedOutcomeWithoutFailureAccounting(t *testing.T) {
	for _, tc := range []struct {
		name       string
		run        func(*JobOrchestrator, *RegisteredJob)
		wantResult func(string) bool
	}{
		{name: "manual", run: func(o *JobOrchestrator, job *RegisteredJob) { o.runDirect(job) }, wantResult: func(result string) bool { return result == "degraded" }},
		{name: "scheduled", run: func(o *JobOrchestrator, job *RegisteredJob) { o.wrapAndRun(job) }, wantResult: func(result string) bool { return strings.HasPrefix(result, "degraded after ") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRecordingAutomationJobRunRepo()
			metrics := &stubAutomationMetrics{}
			var logs bytes.Buffer
			orch := NewJobOrchestrator(OrchestratorDeps{JobRunRepo: repo, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
			orch.WithJobMetrics(metrics)
			orch.Register("job", "degraded job", schedulerSpecEveryMinute(), func(context.Context) error {
				orch.SetLastSummary("job", map[string]int{"processed": 7})
				return Degradedf("provider returned partial data")
			})
			job := orch.jobs["job"]
			priorErrorAt := time.Now()
			job.ErrorCount = 4
			job.ConsecutiveFailures = 2
			job.LastError = "prior failure"
			job.LastErrorAt = &priorErrorAt

			tc.run(orch, job)

			status := singleJobStatus(t, orch, "job")
			if !tc.wantResult(status.LastResult) {
				t.Fatalf("LastResult = %q, want degraded result", status.LastResult)
			}
			if status.RunCount != 1 || status.ErrorCount != 4 || status.ConsecutiveFailures != 0 {
				t.Fatalf("counters = runs:%d errors:%d consecutive:%d, want 1/4/0", status.RunCount, status.ErrorCount, status.ConsecutiveFailures)
			}
			if status.LastError != "" || status.LastDetail != "provider returned partial data" || status.LastErrorAt != nil || !status.Enabled {
				t.Fatalf("degraded fields = error:%q detail:%q at:%v enabled:%t", status.LastError, status.LastDetail, status.LastErrorAt, status.Enabled)
			}
			persisted := repo.singleRun(t)
			if persisted.Status != "degraded" || persisted.Error != "" || persisted.Detail != "provider returned partial data" || persisted.ConsecutiveFailures != 0 || persisted.LastErrorAt != nil {
				t.Fatalf("persisted degraded run = %+v", persisted)
			}
			if persisted.Result["processed"] != 7 {
				t.Fatalf("persisted summary = %#v, want processed=7", persisted.Result)
			}
			if metrics.jobErrors != 0 {
				t.Fatalf("automation error metrics = %d, want 0", metrics.jobErrors)
			}
			if output := logs.String(); !strings.Contains(output, "level=WARN") || !strings.Contains(output, "job degraded") || strings.Contains(output, "job failed") {
				t.Fatalf("degraded logs = %q, want warning only", output)
			}
		})
	}
}

func TestJobOrchestratorHydratesLatestDegradedOutcome(t *testing.T) {
	repo := newRecordingAutomationJobRunRepo()
	lastRun := time.Now().UTC()
	priorErrorAt := lastRun.Add(-time.Hour)
	repo.summaries = []pgrepo.JobRunSummary{
		{JobName: "job", LastRun: &lastRun, LastResult: "degraded", LastSummary: map[string]int{"processed": 7}, LastError: "stale failure", LastDetail: "partial provider response", LastErrorAt: &priorErrorAt, RunCount: 9, ErrorCount: 4, ConsecutiveFailures: 3},
	}
	orch := NewJobOrchestrator(OrchestratorDeps{JobRunRepo: repo})
	orch.Register("job", "degraded job", schedulerSpecEveryMinute(), func(context.Context) error { return nil })

	orch.hydrateFromDB()

	status := singleJobStatus(t, orch, "job")
	if status.LastResult != "degraded" || status.ConsecutiveFailures != 0 || status.LastError != "" || status.LastErrorAt != nil {
		t.Fatalf("hydrated degraded status = %+v", status)
	}
	if status.LastDetail != "partial provider response" {
		t.Fatalf("LastDetail = %q", status.LastDetail)
	}
	if status.ErrorCount != 4 || status.RunCount != 9 || !status.Enabled {
		t.Fatalf("hydrated lifetime counters = %+v", status)
	}
	if status.LastSummary["processed"] != 7 {
		t.Fatalf("LastSummary = %#v, want persisted result counts", status.LastSummary)
	}
}

func TestJobOrchestratorTreatsDegradedRunPersistenceFailureAsError(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*JobOrchestrator, *RegisteredJob)
	}{
		{name: "manual", run: func(o *JobOrchestrator, job *RegisteredJob) { o.runDirect(job) }},
		{name: "scheduled", run: func(o *JobOrchestrator, job *RegisteredJob) { o.wrapAndRun(job) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRecordingAutomationJobRunRepo()
			repo.completeErr = errors.New("database unavailable")
			metrics := &stubAutomationMetrics{}
			orch := NewJobOrchestrator(OrchestratorDeps{JobRunRepo: repo})
			orch.WithJobMetrics(metrics)
			orch.Register("job", "degraded job", schedulerSpecEveryMinute(), func(context.Context) error {
				return Degradedf("partial data")
			})

			tc.run(orch, orch.jobs["job"])

			status := singleJobStatus(t, orch, "job")
			if status.LastResult != "failed: run persistence" || status.ErrorCount != 1 || status.ConsecutiveFailures != 1 || status.LastErrorAt == nil {
				t.Fatalf("status after degraded persistence failure = %+v", status)
			}
			if metrics.jobErrors != 1 {
				t.Fatalf("automation error metrics = %d, want 1", metrics.jobErrors)
			}
		})
	}
}

func TestJobOrchestratorDirectRunUsesOneTerminalTimestampAcrossHourlyBoundary(t *testing.T) {
	for _, test := range []struct {
		name   string
		jobErr error
	}{
		{name: "success"},
		{name: "error", jobErr: errors.New("boom")},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := newRecordingAutomationJobRunRepo()
			orch := NewJobOrchestrator(OrchestratorDeps{JobRunRepo: repo})
			orch.Register("current_data_refresh", "upstream", currentDataRefreshSpec, func(context.Context) error {
				if test.jobErr == nil {
					orch.setRefreshedTickers([]string{"AAPL"})
				}
				return test.jobErr
			})
			orch.Register("deep_scan", "consumer", deepScanSpec, func(context.Context) error { return nil }, "current_data_refresh")

			startedAt := time.Date(2026, time.August, 6, 9, 59, 58, 0, easternTime)
			terminalAt := startedAt.Add(time.Second)
			boundary := terminalAt.Add(time.Second)
			calls := 0
			orch.now = func() time.Time {
				calls++
				switch calls {
				case 1:
					return startedAt
				case 2:
					return terminalAt
				default:
					return boundary
				}
			}

			orch.runDirect(orch.jobs["current_data_refresh"])
			status := singleJobStatus(t, orch, "current_data_refresh")
			persisted := repo.singleRun(t)
			if status.LastRun == nil || persisted.CompletedAt == nil || !status.LastRun.Equal(*persisted.CompletedAt) {
				t.Fatalf("live LastRun = %v, persisted CompletedAt = %v", status.LastRun, persisted.CompletedAt)
			}
			if !status.LastRun.Equal(terminalAt) {
				t.Fatalf("terminal timestamp = %v, want %v", status.LastRun, terminalAt)
			}
			if test.jobErr == nil && strings.Join(persisted.Tickers, ",") != "AAPL" {
				t.Fatalf("persisted tickers = %v, want exact refresh payload", persisted.Tickers)
			}
			if test.jobErr != nil && (status.LastErrorAt == nil || !status.LastErrorAt.Equal(terminalAt)) {
				t.Fatalf("LastErrorAt = %v, want %v", status.LastErrorAt, terminalAt)
			}
			if test.jobErr != nil && (persisted.Error != "boom" || persisted.Detail != "") {
				t.Fatalf("persisted failure = %+v, want error only", persisted)
			}

			if test.jobErr == nil {
				liveDep, liveReason := orch.dependencyBlocker(orch.jobs["deep_scan"], boundary)
				hydratedRepo := newRecordingAutomationJobRunRepo()
				hydratedRepo.summaries = []pgrepo.JobRunSummary{{JobName: "current_data_refresh", LastRun: persisted.CompletedAt, LastResult: "ok", LastTickers: []string{"AAPL"}, RunCount: 1}}
				hydrated := NewJobOrchestrator(OrchestratorDeps{JobRunRepo: hydratedRepo})
				hydrated.Register("current_data_refresh", "upstream", currentDataRefreshSpec, func(context.Context) error { return nil })
				hydrated.Register("deep_scan", "consumer", deepScanSpec, func(context.Context) error { return nil }, "current_data_refresh")
				hydrated.hydrateFromDB()
				hydratedDep, hydratedReason := hydrated.dependencyBlocker(hydrated.jobs["deep_scan"], boundary)
				if liveDep != "current_data_refresh" || liveReason != "latest successful run is from a prior hourly cycle" {
					t.Fatalf("live blocker = (%q, %q)", liveDep, liveReason)
				}
				if hydratedDep != liveDep || hydratedReason != liveReason {
					t.Fatalf("hydrated blocker = (%q, %q), live = (%q, %q)", hydratedDep, hydratedReason, liveDep, liveReason)
				}
			}
		})
	}
}

func TestJobOrchestratorDoesNotExecuteWithoutDurableRunningRow(t *testing.T) {
	t.Parallel()
	repo := newRecordingAutomationJobRunRepo()
	repo.createErr = errors.New("database unavailable")
	executed := false
	orch := NewJobOrchestrator(OrchestratorDeps{JobRunRepo: repo})
	orch.Register("job", "must be durable", schedulerSpecEveryMinute(), func(context.Context) error {
		executed = true
		return nil
	})

	orch.wrapAndRun(orch.jobs["job"])
	if executed {
		t.Fatal("job executed without a durable running row")
	}
	status := singleJobStatus(t, orch, "job")
	if status.Running || status.ErrorCount != 1 || status.ConsecutiveFailures != 1 {
		t.Fatalf("status after start persistence failure = %+v", status)
	}
}

func TestJobOrchestratorJobRunHydrationFailuresDisableAll(t *testing.T) {
	for _, tc := range []struct {
		name string
		prep func(*recordingAutomationJobRunRepo)
	}{
		{name: "orphan recovery", prep: func(r *recordingAutomationJobRunRepo) { r.failIncompleteErr = errors.New("recovery unavailable") }},
		{name: "summary read", prep: func(r *recordingAutomationJobRunRepo) { r.summariesErr = errors.New("summary unavailable") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := newRecordingAutomationJobRunRepo()
			tc.prep(repo)
			orch := NewJobOrchestrator(OrchestratorDeps{JobRunRepo: repo})
			orch.Register("first", "job", schedulerSpecEveryMinute(), func(context.Context) error { return nil })
			orch.Register("second", "job", schedulerSpecEveryMinute(), func(context.Context) error { return nil })
			orch.hydrateFromDB()
			for _, status := range orch.Status() {
				if status.Enabled {
					t.Fatalf("job %s remained enabled after %s failure", status.Name, tc.name)
				}
			}
		})
	}
}

type recordingAutomationJobRunRepo struct {
	mu                sync.Mutex
	runs              []pgrepo.JobRun
	createErr         error
	completeErr       error
	failIncompleteErr error
	summariesErr      error
	summaries         []pgrepo.JobRunSummary
}

func newRecordingAutomationJobRunRepo() *recordingAutomationJobRunRepo {
	return &recordingAutomationJobRunRepo{runs: make([]pgrepo.JobRun, 0)}
}

func (r *recordingAutomationJobRunRepo) Create(_ context.Context, run *pgrepo.JobRun) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.createErr != nil {
		return r.createErr
	}
	if run.ID == uuid.Nil {
		run.ID = uuid.New()
	}
	r.runs = append(r.runs, *run)
	return nil
}

func (r *recordingAutomationJobRunRepo) Complete(_ context.Context, run *pgrepo.JobRun) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.completeErr != nil {
		return r.completeErr
	}
	for i := range r.runs {
		if r.runs[i].ID == run.ID {
			r.runs[i] = *run
			return nil
		}
	}
	return repository.ErrNotFound
}

func (r *recordingAutomationJobRunRepo) FailIncomplete(_ context.Context, _ time.Time, _ string) (int, error) {
	return 0, r.failIncompleteErr
}

func (r *recordingAutomationJobRunRepo) Summaries(context.Context) ([]pgrepo.JobRunSummary, error) {
	return append([]pgrepo.JobRunSummary(nil), r.summaries...), r.summariesErr
}

func (r *recordingAutomationJobRunRepo) singleRun(t *testing.T) pgrepo.JobRun {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.runs) != 1 {
		t.Fatalf("job runs = %+v, want one", r.runs)
	}
	return r.runs[0]
}

var _ AutomationJobRunRepository = (*recordingAutomationJobRunRepo)(nil)

func TestJobOrchestratorRunJob_AutoDisablesAfterThreshold(t *testing.T) {
	t.Parallel()

	orch := NewJobOrchestrator(OrchestratorDeps{})
	orch.Register("job", "always fails", schedulerSpecEveryMinute(), func(context.Context) error {
		return errors.New("boom")
	})
	orch.SetConsecutiveFailures("job", autoDisableThreshold-1)

	if err := orch.RunJob(context.Background(), "job"); err != nil {
		t.Fatalf("RunJob() error = %v", err)
	}
	waitForJobRuns(t, orch, "job", 1)

	status := singleJobStatus(t, orch, "job")
	if status.ConsecutiveFailures != autoDisableThreshold {
		t.Fatalf("ConsecutiveFailures = %d, want %d", status.ConsecutiveFailures, autoDisableThreshold)
	}
	if status.Enabled {
		t.Fatal("Enabled = true, want false after reaching auto-disable threshold")
	}
}

func TestAutoDisableThresholdSurvivesHydration(t *testing.T) {
	t.Parallel()

	if shouldDisableAfterHydration(autoDisableThreshold - 1) {
		t.Fatal("below-threshold failure count should remain enabled")
	}
	if !shouldDisableAfterHydration(autoDisableThreshold) {
		t.Fatal("threshold failure count should hydrate disabled")
	}
}

func TestJobOrchestratorRunJob_RejectsDisabledJob(t *testing.T) {
	t.Parallel()

	var calls int
	orch := NewJobOrchestrator(OrchestratorDeps{})
	orch.Register("job", "disabled job", schedulerSpecEveryMinute(), func(context.Context) error {
		calls++
		return nil
	})
	if err := orch.SetEnabled("job", false); err != nil {
		t.Fatalf("SetEnabled(false) error = %v", err)
	}

	err := orch.RunJob(context.Background(), "job")
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("RunJob() error = %v, want disabled rejection", err)
	}
	time.Sleep(20 * time.Millisecond)
	if calls != 0 {
		t.Fatalf("disabled job calls = %d, want 0", calls)
	}
	if status := singleJobStatus(t, orch, "job"); status.RunCount != 0 {
		t.Fatalf("disabled job RunCount = %d, want 0", status.RunCount)
	}
}

type automationJobControlRepoStub struct {
	controls []domain.AutomationJobControl
	listErr  error
	setErr   error
	writes   []domain.AutomationJobControl
}

func (r *automationJobControlRepoStub) List(context.Context) ([]domain.AutomationJobControl, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	return append([]domain.AutomationJobControl(nil), r.controls...), nil
}

func (r *automationJobControlRepoStub) SetEnabled(_ context.Context, name string, enabled bool, actor string) error {
	if r.setErr != nil {
		return r.setErr
	}
	r.writes = append(r.writes, domain.AutomationJobControl{JobName: name, Enabled: enabled, UpdatedBy: actor})
	return nil
}

func TestJobOrchestratorSetEnabledPersistsBeforeMemory(t *testing.T) {
	t.Parallel()

	controls := &automationJobControlRepoStub{setErr: errors.New("database unavailable")}
	orch := NewJobOrchestrator(OrchestratorDeps{JobControlRepo: controls})
	orch.Register("job", "controlled job", schedulerSpecEveryMinute(), func(context.Context) error { return nil })

	err := orch.SetEnabled("job", false)
	if err == nil || !strings.Contains(err.Error(), "database unavailable") {
		t.Fatalf("SetEnabled() error = %v, want persistence failure", err)
	}
	if status := singleJobStatus(t, orch, "job"); !status.Enabled {
		t.Fatal("job changed in memory after control persistence failed")
	}
}

func TestJobOrchestratorSetEnabledPersistsActor(t *testing.T) {
	t.Parallel()

	controls := &automationJobControlRepoStub{}
	orch := NewJobOrchestrator(OrchestratorDeps{JobControlRepo: controls})
	orch.Register("job", "controlled job", schedulerSpecEveryMinute(), func(context.Context) error { return nil })

	if err := orch.SetEnabledBy(context.Background(), "job", false, "operator@example.com"); err != nil {
		t.Fatalf("SetEnabledBy() error = %v", err)
	}
	if len(controls.writes) != 1 || controls.writes[0].JobName != "job" || controls.writes[0].Enabled || controls.writes[0].UpdatedBy != "operator@example.com" {
		t.Fatalf("control writes = %+v", controls.writes)
	}
	if status := singleJobStatus(t, orch, "job"); status.Enabled {
		t.Fatal("durably disabled job remained enabled in memory")
	}
}

func TestJobOrchestratorHydratesDurableDisabledControl(t *testing.T) {
	t.Parallel()

	controls := &automationJobControlRepoStub{controls: []domain.AutomationJobControl{{JobName: "job", Enabled: false, UpdatedBy: "operator"}}}
	orch := NewJobOrchestrator(OrchestratorDeps{JobControlRepo: controls})
	orch.Register("job", "controlled job", schedulerSpecEveryMinute(), func(context.Context) error { return nil })
	orch.hydrateFromDB()

	if status := singleJobStatus(t, orch, "job"); status.Enabled {
		t.Fatal("durably disabled job hydrated enabled")
	}
}

func TestJobOrchestratorDurableEnableOverridesHistoricalAutoDisable(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, time.August, 6, 10, 0, 0, 0, time.UTC)
	runs := newRecordingAutomationJobRunRepo()
	runs.summaries = []pgrepo.JobRunSummary{{
		JobName:             "job",
		LastResult:          "skipped",
		LastError:           "market calendar unavailable",
		LastDetail:          "market closed",
		LastErrorAt:         &completedAt,
		RunCount:            8,
		ErrorCount:          5,
		ConsecutiveFailures: autoDisableThreshold,
	}}
	controls := &automationJobControlRepoStub{controls: []domain.AutomationJobControl{{JobName: "job", Enabled: true, UpdatedBy: "operator"}}}
	orch := NewJobOrchestrator(OrchestratorDeps{JobRunRepo: runs, JobControlRepo: controls})
	orch.Register("job", "controlled job", schedulerSpecEveryMinute(), func(context.Context) error { return nil })
	orch.hydrateFromDB()

	status := singleJobStatus(t, orch, "job")
	if !status.Enabled || status.LastResult != "skipped" || status.LastError != "market calendar unavailable" || status.LastDetail != "market closed" || status.LastErrorAt == nil || !status.LastErrorAt.Equal(completedAt) {
		t.Fatalf("durably re-enabled job = %+v", status)
	}
	if status.RunCount != 8 || status.ErrorCount != 5 || status.ConsecutiveFailures != autoDisableThreshold {
		t.Fatalf("durably re-enabled job counters = %+v", status)
	}
}

func TestJobOrchestratorControlHydrationFailureDisablesAll(t *testing.T) {
	t.Parallel()

	controls := &automationJobControlRepoStub{listErr: errors.New("database unavailable")}
	orch := NewJobOrchestrator(OrchestratorDeps{JobControlRepo: controls})
	orch.Register("first", "controlled job", schedulerSpecEveryMinute(), func(context.Context) error { return nil })
	orch.Register("second", "controlled job", schedulerSpecEveryMinute(), func(context.Context) error { return nil })
	orch.hydrateFromDB()

	for _, status := range orch.Status() {
		if status.Enabled {
			t.Fatalf("job %s remained enabled after control hydration failure", status.Name)
		}
	}
}

func TestJobOrchestratorRunJobRejectsOutsideConfiguredSession(t *testing.T) {
	t.Parallel()

	var calls int
	orch := NewJobOrchestrator(OrchestratorDeps{})
	orch.now = func() time.Time {
		return time.Date(2026, time.August, 6, 21, 0, 0, 0, time.UTC) // 5:00 PM ET
	}
	orch.Register("market_job", "market-window job", scheduler.ScheduleSpec{
		Type:         scheduler.ScheduleTypeMarketHours,
		MarketType:   string(domain.MarketTypeStock),
		SkipWeekends: true,
		SkipHolidays: true,
	}, func(context.Context) error {
		calls++
		return nil
	})

	err := orch.RunJob(context.Background(), "market_job")
	if err == nil || !strings.Contains(err.Error(), "outside configured session") {
		t.Fatalf("RunJob() error = %v, want configured-session rejection", err)
	}
	time.Sleep(20 * time.Millisecond)
	if calls != 0 {
		t.Fatalf("job calls = %d, want 0", calls)
	}
}

func TestJobOrchestratorRunJobAllowsRerunInsideConfiguredSession(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	orch := NewJobOrchestrator(OrchestratorDeps{})
	orch.now = func() time.Time {
		return time.Date(2026, time.August, 6, 15, 0, 0, 0, time.UTC) // 11:00 AM ET
	}
	orch.Register("market_job", "market-window job", scheduler.ScheduleSpec{
		Type:         scheduler.ScheduleTypeMarketHours,
		MarketType:   string(domain.MarketTypeStock),
		SkipWeekends: true,
		SkipHolidays: true,
	}, func(context.Context) error {
		close(done)
		return nil
	})

	if err := orch.RunJob(context.Background(), "market_job"); err != nil {
		t.Fatalf("RunJob() error = %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("market-session rerun did not start")
	}
}

func TestCurrentDataRefreshManualRunUsesPostCloseGrace(t *testing.T) {
	tests := []struct {
		name    string
		now     time.Time
		wantRun bool
	}{
		{name: "closing refresh", now: time.Date(2026, time.August, 6, 16, 45, 0, 0, easternTime), wantRun: true},
		{name: "last grace minute", now: time.Date(2026, time.August, 6, 16, 45, 59, 999999999, easternTime), wantRun: true},
		{name: "after grace", now: time.Date(2026, time.August, 6, 16, 46, 0, 0, easternTime)},
		{name: "holiday", now: time.Date(2026, time.December, 25, 16, 45, 0, 0, easternTime)},
		{name: "weekend", now: time.Date(2026, time.August, 8, 16, 45, 0, 0, easternTime)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			done := make(chan struct{})
			orch := NewJobOrchestrator(OrchestratorDeps{})
			orch.now = func() time.Time { return test.now }
			orch.Register("current_data_refresh", "test", currentDataRefreshSpec, func(context.Context) error {
				close(done)
				return nil
			})

			err := orch.RunJob(context.Background(), "current_data_refresh")
			if test.wantRun {
				if err != nil {
					t.Fatalf("RunJob() error = %v", err)
				}
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("current_data_refresh did not run")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "outside configured session") {
				t.Fatalf("RunJob() error = %v, want session rejection", err)
			}
		})
	}
}

func TestJobOrchestratorRunJobRejectsOverlapSynchronously(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	release := make(chan struct{})
	orch := NewJobOrchestrator(OrchestratorDeps{})
	orch.Register("job", "blocking job", schedulerSpecEveryMinute(), func(context.Context) error {
		close(started)
		<-release
		return nil
	})

	if err := orch.RunJob(context.Background(), "job"); err != nil {
		t.Fatalf("first RunJob() error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first run did not start")
	}
	if err := orch.RunJob(context.Background(), "job"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second RunJob() error = %v, want synchronous overlap rejection", err)
	}
	close(release)
	waitForJobRuns(t, orch, "job", 1)
}

func TestJobOrchestratorRunDirectEnforcesTimeout(t *testing.T) {
	orch := NewJobOrchestrator(OrchestratorDeps{JobTimeout: 10 * time.Millisecond})
	orch.Register("job", "test", schedulerSpecEveryMinute(), func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	orch.runDirect(orch.jobs["job"])
	status := singleJobStatus(t, orch, "job")
	if status.LastResult != "failed" {
		t.Fatalf("LastResult = %q, want failed", status.LastResult)
	}
	if !strings.Contains(status.LastError, context.DeadlineExceeded.Error()) {
		t.Fatalf("LastError = %q, want deadline exceeded", status.LastError)
	}
}

func TestJobOrchestratorWrapAndRun_AutoDisabledJobsAreSkipped(t *testing.T) {
	t.Parallel()

	orch := NewJobOrchestrator(OrchestratorDeps{})
	orch.Register("job", "always fails", schedulerSpecEveryMinute(), func(context.Context) error {
		return errors.New("boom")
	})
	orch.SetConsecutiveFailures("job", autoDisableThreshold-1)

	job := orch.jobs["job"]
	orch.wrapAndRun(job)

	status := singleJobStatus(t, orch, "job")
	if status.ConsecutiveFailures != autoDisableThreshold {
		t.Fatalf("ConsecutiveFailures = %d, want %d", status.ConsecutiveFailures, autoDisableThreshold)
	}
	if status.Enabled {
		t.Fatal("Enabled = true, want false after reaching auto-disable threshold")
	}
	if status.RunCount != 1 {
		t.Fatalf("RunCount after first run = %d, want 1", status.RunCount)
	}

	orch.wrapAndRun(job)
	status = singleJobStatus(t, orch, "job")
	if status.RunCount != 1 {
		t.Fatalf("RunCount after disabled scheduled invocation = %d, want 1", status.RunCount)
	}
}

func TestDependencyBlockerRequiresSuccessfulSameDayRun(t *testing.T) {
	t.Parallel()

	orch := NewJobOrchestrator(OrchestratorDeps{})
	orch.Register("upstream", "upstream", schedulerSpecEveryMinute(), func(context.Context) error { return nil })
	orch.Register("consumer", "consumer", schedulerSpecEveryMinute(), func(context.Context) error { return nil }, "upstream")
	now := time.Date(2026, time.August, 6, 8, 30, 0, 0, easternTime)
	upstream := orch.jobs["upstream"]
	consumer := orch.jobs["consumer"]

	if dep, reason := orch.dependencyBlocker(consumer, now); dep != "upstream" || !strings.Contains(reason, "has not completed") {
		t.Fatalf("never-run blocker = (%q, %q)", dep, reason)
	}
	priorDay := now.AddDate(0, 0, -1)
	upstream.LastRun = &priorDay
	upstream.LastResult = "ok"
	if dep, reason := orch.dependencyBlocker(consumer, now); dep != "upstream" || !strings.Contains(reason, "prior") {
		t.Fatalf("prior-day blocker = (%q, %q)", dep, reason)
	}
	upstream.LastRun = &now
	upstream.LastResult = "error"
	if dep, reason := orch.dependencyBlocker(consumer, now); dep != "upstream" || !strings.Contains(reason, "not successful") {
		t.Fatalf("failed blocker = (%q, %q)", dep, reason)
	}
	upstream.LastResult = "ok in 2s"
	if dep, reason := orch.dependencyBlocker(consumer, now); dep != "" || reason != "" {
		t.Fatalf("successful blocker = (%q, %q), want none", dep, reason)
	}
	upstream.LastResult = "degraded after 2s"
	if dep, reason := orch.dependencyBlocker(consumer, now); dep != "" || reason != "" {
		t.Fatalf("degraded blocker = (%q, %q), want none", dep, reason)
	}
}

func TestMarketDependencyCycleCheckMatchesHydratedState(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, time.August, 6, 9, 45, 0, 0, easternTime)
	now := time.Date(2026, time.August, 6, 10, 0, 0, 0, easternTime)
	newOrchestrator := func(repo AutomationJobRunRepository) *JobOrchestrator {
		orch := NewJobOrchestrator(OrchestratorDeps{JobRunRepo: repo})
		orch.Register("current_data_refresh", "upstream", currentDataRefreshSpec, func(context.Context) error { return nil })
		orch.Register("hot_scan", "consumer", hotScanSpec, func(context.Context) error { return nil }, "current_data_refresh")
		return orch
	}

	inMemory := newOrchestrator(nil)
	upstream := inMemory.jobs["current_data_refresh"]
	upstream.mu.Lock()
	upstream.LastRun = &completedAt
	upstream.LastResult = "ok"
	upstream.mu.Unlock()
	inMemory.setRefreshedTickers([]string{"AAPL", "MSFT"})
	inMemoryDep, inMemoryReason := inMemory.dependencyBlocker(inMemory.jobs["hot_scan"], now)

	repo := newRecordingAutomationJobRunRepo()
	repo.summaries = []pgrepo.JobRunSummary{{JobName: "current_data_refresh", LastRun: &completedAt, LastResult: "ok", LastTickers: []string{"AAPL", "MSFT"}, RunCount: 1}}
	hydrated := newOrchestrator(repo)
	hydrated.hydrateFromDB()
	hydratedDep, hydratedReason := hydrated.dependencyBlocker(hydrated.jobs["hot_scan"], now)

	if hydratedDep != inMemoryDep || hydratedReason != inMemoryReason || hydratedDep != "" {
		t.Fatalf("hydrated blocker = (%q, %q), in-memory = (%q, %q)", hydratedDep, hydratedReason, inMemoryDep, inMemoryReason)
	}
}

func TestCurrentDataRefreshPayloadSurvivesRestartAndBlocksLegacyHandoff(t *testing.T) {
	completedAt := time.Date(2026, time.August, 6, 9, 45, 0, 0, easternTime)
	now := completedAt.Add(5 * time.Minute)
	newOrchestrator := func(summary pgrepo.JobRunSummary) *JobOrchestrator {
		repo := newRecordingAutomationJobRunRepo()
		repo.summaries = []pgrepo.JobRunSummary{summary}
		orch := NewJobOrchestrator(OrchestratorDeps{JobRunRepo: repo})
		orch.Register("current_data_refresh", "upstream", currentDataRefreshSpec, func(context.Context) error { return nil })
		orch.Register("hot_scan", "consumer", hotScanSpec, func(context.Context) error { return nil }, "current_data_refresh")
		orch.hydrateFromDB()
		return orch
	}

	hydrated := newOrchestrator(pgrepo.JobRunSummary{JobName: "current_data_refresh", LastRun: &completedAt, LastResult: "degraded", LastDetail: "partial refresh", LastTickers: []string{"AAPL", "MSFT"}})
	if got := strings.Join(hydrated.getRefreshedTickers(), ","); got != "AAPL,MSFT" {
		t.Fatalf("hydrated tickers = %q", got)
	}
	if dep, reason := hydrated.dependencyBlocker(hydrated.jobs["hot_scan"], now); dep != "" || reason != "" {
		t.Fatalf("hydrated payload blocker = (%q, %q)", dep, reason)
	}

	legacy := newOrchestrator(pgrepo.JobRunSummary{JobName: "current_data_refresh", LastRun: &completedAt, LastResult: "ok"})
	if dep, reason := legacy.dependencyBlocker(legacy.jobs["hot_scan"], now); dep != "current_data_refresh" || reason != "fresh ticker payload unavailable" {
		t.Fatalf("legacy blocker = (%q, %q)", dep, reason)
	}
	legacy.now = func() time.Time { return now }
	legacy.runDirect(legacy.jobs["hot_scan"])
	status := singleJobStatus(t, legacy, "hot_scan")
	if status.LastResult != "skipped: dependency current_data_refresh fresh ticker payload unavailable" {
		t.Fatalf("legacy hot scan result = %q", status.LastResult)
	}
	persisted := legacy.deps.JobRunRepo.(*recordingAutomationJobRunRepo).singleRun(t)
	if persisted.Status != "skipped" {
		t.Fatalf("legacy hot scan persisted status = %q", persisted.Status)
	}
	wantReason := "dependency current_data_refresh fresh ticker payload unavailable"
	if persisted.Error != "" || persisted.Detail != wantReason || persisted.Result["dependency_blocked"] != 1 {
		t.Fatalf("persisted dependency skip = %+v", persisted)
	}
	if status.LastError != "" || status.LastDetail != wantReason {
		t.Fatalf("in-memory dependency skip = %+v", status)
	}
}

func TestClosingCurrentDataRefreshPersistsAndHydratesNoTickerPayload(t *testing.T) {
	completedAt := time.Date(2026, time.August, 26, 16, 45, 0, 0, easternTime)
	repo := newRecordingAutomationJobRunRepo()
	orch := NewJobOrchestrator(OrchestratorDeps{JobRunRepo: repo})
	orch.Register("current_data_refresh", "closing refresh", currentDataRefreshSpec, func(context.Context) error {
		orch.SetLastSummary("current_data_refresh", map[string]int{"closing_mode": 1, "daily_closing_updated": 1})
		orch.setRefreshedTickers([]string{"MUST_NOT_PERSIST"})
		return nil
	})
	orch.now = func() time.Time { return completedAt }
	orch.runDirect(orch.jobs["current_data_refresh"])
	persisted := repo.singleRun(t)
	if persisted.Tickers != nil {
		t.Fatalf("closing persisted tickers = %v, want nil", persisted.Tickers)
	}

	hydratedRepo := newRecordingAutomationJobRunRepo()
	hydratedRepo.summaries = []pgrepo.JobRunSummary{{
		JobName: "current_data_refresh", LastRun: persisted.CompletedAt, LastResult: "ok",
		LastSummary: map[string]int{"closing_mode": 1, "daily_closing_updated": 1},
		LastTickers: []string{"LEGACY_SHOULD_NOT_HYDRATE"}, RunCount: 1,
	}}
	hydrated := NewJobOrchestrator(OrchestratorDeps{JobRunRepo: hydratedRepo})
	hydrated.Register("current_data_refresh", "closing refresh", currentDataRefreshSpec, func(context.Context) error { return nil })
	hydrated.Register("hot_scan", "consumer", hotScanSpec, func(context.Context) error { return nil }, "current_data_refresh")
	hydrated.hydrateFromDB()
	status := singleJobStatus(t, hydrated, "current_data_refresh")
	if status.LastSummary["daily_closing_updated"] != 1 || len(hydrated.getRefreshedTickers()) != 0 {
		t.Fatalf("hydrated closing state = summary:%v tickers:%v", status.LastSummary, hydrated.getRefreshedTickers())
	}
	if dep, reason := hydrated.dependencyBlocker(hydrated.jobs["hot_scan"], completedAt.AddDate(0, 0, 1)); dep != "current_data_refresh" || reason != "latest run is from a prior automation day" {
		t.Fatalf("next-day blocker = (%q, %q)", dep, reason)
	}
}

func TestJobOrchestratorHydratesLatestOutcomeWithRetainedFailureStreak(t *testing.T) {
	completedAt := time.Date(2026, time.August, 6, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name           string
		lastResult     string
		lastError      string
		lastDetail     string
		wantLastResult string
		wantLastError  string
		enabled        bool
	}{
		{name: "dependency skip", lastResult: "skipped", lastError: "stale failure", lastDetail: "dependency upstream still running", wantLastResult: "skipped: dependency upstream still running", enabled: true},
		{name: "legacy explicit dependency skip", lastResult: "skipped: dependency upstream still running", wantLastResult: "skipped: dependency upstream still running", enabled: true},
		{name: "ordinary skip", lastResult: "skipped", lastError: "market calendar unavailable", lastDetail: "market closed", wantLastResult: "skipped", wantLastError: "market calendar unavailable", enabled: false},
		{name: "error", lastResult: "error", wantLastResult: "error", enabled: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := newRecordingAutomationJobRunRepo()
			repo.summaries = []pgrepo.JobRunSummary{{
				JobName:             "consumer",
				LastRun:             &completedAt,
				LastResult:          tc.lastResult,
				LastError:           tc.lastError,
				LastDetail:          tc.lastDetail,
				LastErrorAt:         &completedAt,
				RunCount:            8,
				ErrorCount:          5,
				ConsecutiveFailures: autoDisableThreshold,
			}}
			orch := NewJobOrchestrator(OrchestratorDeps{JobRunRepo: repo})
			orch.Register("consumer", "consumer", schedulerSpecEveryMinute(), func(context.Context) error { return nil })
			orch.hydrateFromDB()

			status := singleJobStatus(t, orch, "consumer")
			if status.Enabled != tc.enabled {
				t.Fatalf("Enabled = %t, want %t; status = %+v", status.Enabled, tc.enabled, status)
			}
			if status.LastResult != tc.wantLastResult || status.LastError != tc.wantLastError || status.LastDetail != tc.lastDetail || status.LastErrorAt == nil || !status.LastErrorAt.Equal(completedAt) {
				t.Fatalf("hydrated outcome = %+v", status)
			}
			if status.RunCount != 8 || status.ConsecutiveFailures != autoDisableThreshold || status.ErrorCount != 5 {
				t.Fatalf("hydrated historical failures = %+v", status)
			}
		})
	}
}

func TestJobOrchestratorDurableDisableOverridesDependencySkip(t *testing.T) {
	t.Parallel()

	runs := newRecordingAutomationJobRunRepo()
	runs.summaries = []pgrepo.JobRunSummary{{
		JobName:             "job",
		LastResult:          "skipped",
		LastDetail:          "dependency upstream disabled",
		ConsecutiveFailures: autoDisableThreshold,
	}}
	controls := &automationJobControlRepoStub{controls: []domain.AutomationJobControl{{JobName: "job", Enabled: false, UpdatedBy: "operator"}}}
	orch := NewJobOrchestrator(OrchestratorDeps{JobRunRepo: runs, JobControlRepo: controls})
	orch.Register("job", "controlled job", schedulerSpecEveryMinute(), func(context.Context) error { return nil })
	orch.hydrateFromDB()

	status := singleJobStatus(t, orch, "job")
	if status.Enabled || status.ConsecutiveFailures != autoDisableThreshold {
		t.Fatalf("durably disabled dependency-skipped job = %+v", status)
	}
}

type stubAutomationMetrics struct {
	alpacaRuns     map[string]int
	kalshiDryRuns  map[string]int
	kalshiOutcomes map[string]int
	transitions    []string
	jobErrors      int
}

func (m *stubAutomationMetrics) RecordAutomationJobError(string) { m.jobErrors++ }

func (m *stubAutomationMetrics) RecordAlpacaReconcileRun(result string) {
	if m.alpacaRuns == nil {
		m.alpacaRuns = make(map[string]int)
	}
	m.alpacaRuns[result]++
}

func (m *stubAutomationMetrics) RecordKalshiReconcileRun(result string) {
	m.RecordAlpacaReconcileRun("kalshi_" + result)
}

func (m *stubAutomationMetrics) RecordKalshiSettlementDryRun(result string) {
	if m.kalshiDryRuns == nil {
		m.kalshiDryRuns = make(map[string]int)
	}
	m.kalshiDryRuns[result]++
}

func (m *stubAutomationMetrics) RecordKalshiSettlementOutcome(result string) {
	if m.kalshiOutcomes == nil {
		m.kalshiOutcomes = make(map[string]int)
	}
	m.kalshiOutcomes[result]++
}

func (m *stubAutomationMetrics) RecordKalshiSettlementTransition(from, to string) {
	m.transitions = append(m.transitions, from+"->"+to)
}

func TestJobOrchestratorStatus_IncludesLastSummary(t *testing.T) {
	t.Parallel()

	orch := NewJobOrchestrator(OrchestratorDeps{})
	orch.Register("alpaca_reconcile", "test job", schedulerSpecEveryMinute(), func(context.Context) error { return nil })
	orch.SetLastSummary("alpaca_reconcile", map[string]int{"orders_created": 2, "trades_created": 3})

	status := singleJobStatus(t, orch, "alpaca_reconcile")
	if status.LastSummary == nil {
		t.Fatal("LastSummary = nil, want populated")
	}
	if status.LastSummary["orders_created"] != 2 {
		t.Fatalf("orders_created = %d, want 2", status.LastSummary["orders_created"])
	}
	status.LastSummary["orders_created"] = 99
	statusAgain := singleJobStatus(t, orch, "alpaca_reconcile")
	if statusAgain.LastSummary["orders_created"] != 2 {
		t.Fatalf("mutated summary leaked into orchestrator: %d", statusAgain.LastSummary["orders_created"])
	}
}

func TestJobOrchestratorRegisterAllAddsCurrentDataRefreshBeforeHotScan(t *testing.T) {
	t.Parallel()

	orch := NewJobOrchestrator(OrchestratorDeps{})
	orch.RegisterAll()

	if _, ok := orch.jobs["current_data_refresh"]; !ok {
		t.Fatal("current_data_refresh job not registered")
	}
	hotScan, ok := orch.jobs["hot_scan"]
	if !ok {
		t.Fatal("hot_scan job not registered")
	}
	if len(hotScan.DependsOn) != 1 || hotScan.DependsOn[0] != "current_data_refresh" {
		t.Fatalf("hot_scan depends_on = %#v, want [current_data_refresh]", hotScan.DependsOn)
	}
}

func TestJobOrchestratorRegisterAllAddsPolymarketReconcile(t *testing.T) {
	t.Parallel()

	reconciler := polymarketexecution.NewReconciler(polymarketexecution.ReconcilerDeps{
		Broker: &polymarketBrokerStub{positions: []domain.Position{{Ticker: "market-one:YES", Side: domain.PositionSideLong, Quantity: 10}}},
		PositionRepo: &polymarketPositionRepoStub{positions: []domain.Position{{
			MarketType: domain.MarketTypePolymarket,
			Ticker:     "market-one",
			Side:       domain.PositionSideLong,
			Quantity:   10,
		}}},
		AuditLogRepo: &polymarketAuditRepoStub{},
		Metrics:      &polymarketReconcilerMetricsStub{},
		Logger:       slog.Default(),
	})
	orch := NewJobOrchestrator(OrchestratorDeps{PolymarketReconciler: reconciler})
	orch.RegisterAll()

	status := singleJobStatus(t, orch, "polymarket_reconcile")
	if status.Schedule == "" {
		t.Fatal("polymarket_reconcile schedule is empty")
	}

	if err := orch.RunJob(context.Background(), "polymarket_reconcile"); err != nil {
		t.Fatalf("RunJob() error = %v", err)
	}
	waitForJobRuns(t, orch, "polymarket_reconcile", 1)

	status = singleJobStatus(t, orch, "polymarket_reconcile")
	if status.LastResult != "success" {
		t.Fatalf("LastResult = %q, want success", status.LastResult)
	}
	if status.LastSummary == nil || status.LastSummary["drifts"] != 0 {
		t.Fatalf("LastSummary = %#v, want drifts=0", status.LastSummary)
	}
}

func TestJobOrchestratorRegisterAllCanDisablePolymarketAutomation(t *testing.T) {
	t.Parallel()

	reconciler := polymarketexecution.NewReconciler(polymarketexecution.ReconcilerDeps{
		Broker:       &polymarketBrokerStub{},
		PositionRepo: &polymarketPositionRepoStub{},
		AuditLogRepo: &polymarketAuditRepoStub{},
		Metrics:      &polymarketReconcilerMetricsStub{},
		Logger:       slog.Default(),
	})
	orch := NewJobOrchestrator(OrchestratorDeps{
		PolymarketReconciler:        reconciler,
		DisablePolymarketAutomation: true,
	})
	orch.RegisterAll()

	for _, name := range []string{"polymarket_profiles", "polymarket_reconcile", "polymarket_resolutions", "polymarket_strategy_discovery"} {
		if _, ok := orch.jobs[name]; ok {
			t.Fatalf("job %q registered with DisablePolymarketAutomation=true", name)
		}
	}
}

func TestJobOrchestratorRegisterAllAddsKalshiDiscovery(t *testing.T) {
	t.Parallel()

	origRun := kalshiDiscoveryRun
	defer func() { kalshiDiscoveryRun = origRun }()

	var gotCfg kalshidiscovery.Config
	var gotDeps kalshidiscovery.Deps
	kalshiDiscoveryRun = func(_ context.Context, cfg kalshidiscovery.Config, deps kalshidiscovery.Deps) (*kalshidiscovery.Result, error) {
		gotCfg = cfg
		gotDeps = deps
		return &kalshidiscovery.Result{FetchedAll: 50, Screened: 15, Proposed: 2, Skipped: 1, Deployed: []kalshidiscovery.DeployedStrategy{{Reused: true}}, Errors: []string{"snapshot failed"}}, nil
	}

	orch := NewJobOrchestrator(OrchestratorDeps{
		StrategyRepo:              &kalshiStrategyRepoStub{},
		KalshiCatalog:             kalshiCatalogStub{},
		KalshiWatchedRepo:         &kalshiWatchedRepoStub{},
		KalshiMarketSnapshotsRepo: &kalshiSnapshotsRepoStub{},
		KalshiDiscoveryRuns:       &kalshiDiscoveryRunsRepoStub{},
	})
	orch.RegisterAll()

	status := singleJobStatus(t, orch, "kalshi_discovery")
	if status.Schedule == "" || !strings.Contains(status.Schedule, "15 * * * *") {
		t.Fatalf("kalshi_discovery schedule = %q, want cron 15 * * * *", status.Schedule)
	}

	if err := orch.RunJob(context.Background(), "kalshi_discovery"); err != nil {
		t.Fatalf("RunJob() error = %v", err)
	}
	waitForJobRuns(t, orch, "kalshi_discovery", 1)
	status = singleJobStatus(t, orch, "kalshi_discovery")
	if status.LastSummary["fetched"] != 50 || status.LastSummary["screened"] != 15 || status.LastSummary["deployed"] != 1 || status.LastSummary["created"] != 0 || status.LastSummary["reused"] != 1 || status.LastSummary["errors"] != 1 {
		t.Fatalf("Kalshi discovery summary = %#v, want persisted result counts", status.LastSummary)
	}

	if gotCfg.DryRun {
		t.Fatal("Kalshi discovery cfg.DryRun = true, want false")
	}
	if gotCfg.FetchLimit != 50 || gotCfg.MaxDeployments != 1 || gotCfg.MinConviction != 0.70 {
		t.Fatalf("Kalshi discovery cfg = %#v, want conservative paper settings", gotCfg)
	}
	if gotCfg.Screener.MaxCandidates != 15 || gotCfg.Screener.MinVolume != 1000 || gotCfg.Screener.MinOpenInterest != 500 || gotCfg.Screener.MaxSpreadPct != 12 || gotCfg.Screener.MinDaysToClose != 3 {
		t.Fatalf("Kalshi discovery screener = %#v, want default conservative config", gotCfg.Screener)
	}
	if gotDeps.Catalog == nil || gotDeps.Strategies == nil || gotDeps.Watched == nil || gotDeps.Snapshots == nil || gotDeps.DiscoveryRuns == nil {
		t.Fatalf("Kalshi discovery deps = %#v, want all persistence dependencies wired", gotDeps)
	}
	if gotDeps.Logger == nil {
		t.Fatal("Kalshi discovery logger = nil")
	}
}

func TestJobOrchestratorRegisterAllAddsKalshiSettlement(t *testing.T) {
	t.Parallel()
	orch := NewJobOrchestrator(OrchestratorDeps{
		KalshiCatalog:     kalshiCatalogStub{},
		PredictionSettler: predictionexecution.NewSettler(nil, nil, nil, nil, nil),
	})
	orch.RegisterAll()
	status := singleJobStatus(t, orch, "kalshi_settlement")
	if status.Schedule == "" || status.Schedule == "Manual only" {
		t.Fatalf("kalshi settlement schedule = %q", status.Schedule)
	}
	if !status.Enabled {
		t.Fatal("kalshi_settlement enabled = false, want preview scheduler active")
	}
}

func TestJobOrchestratorRegisterAllAddsKalshiReconciliation(t *testing.T) {
	t.Parallel()
	orch := NewJobOrchestrator(OrchestratorDeps{KalshiReconciler: kalshiexecution.NewReconciler(kalshiexecution.ReconcilerDeps{})})
	orch.RegisterAll()
	status := singleJobStatus(t, orch, "kalshi_reconcile")
	if status.Schedule == "" || status.Schedule == "Manual only" {
		t.Fatalf("kalshi reconciliation schedule = %q", status.Schedule)
	}
}

type kalshiSettlementGateStub struct {
	state       *domain.KalshiSettlementGateState
	err         error
	failPersist bool
}

func (s *kalshiSettlementGateStub) Get(context.Context, string) (*domain.KalshiSettlementGateState, error) {
	return s.state, s.err
}

func (s *kalshiSettlementGateStub) RecordSuccess(context.Context, string, int, int, int, int, int, string, time.Time) (*domain.KalshiSettlementGateState, error) {
	if s.failPersist {
		return nil, errors.New("persist failed")
	}
	return s.state, nil
}

func (s *kalshiSettlementGateStub) RecordFailure(context.Context, string, int, int, int, int, int, time.Time, string) (*domain.KalshiSettlementGateState, error) {
	if s.failPersist {
		return nil, errors.New("persist failed")
	}
	return s.state, nil
}

func TestJobOrchestratorSetEnabledKalshiSettlementDoesNotRequireEligibility(t *testing.T) {
	orch := NewJobOrchestrator(OrchestratorDeps{KalshiSettlementGateRepo: &kalshiSettlementGateStub{state: &domain.KalshiSettlementGateState{Eligible: false}}})
	orch.Register("kalshi_settlement", "test", schedulerSpecEveryMinute(), func(context.Context) error { return nil })
	if err := orch.SetEnabled("kalshi_settlement", true); err != nil {
		t.Fatalf("SetEnabled(true) error = %v, preview scheduling must not require eligibility", err)
	}
	orch.deps.KalshiSettlementGateRepo = &kalshiSettlementGateStub{state: &domain.KalshiSettlementGateState{Eligible: true}}
	if err := orch.SetEnabled("kalshi_settlement", false); err != nil {
		t.Fatalf("SetEnabled(false) error = %v", err)
	}
}

func TestJobOrchestratorSetEnabledKalshiSettlementAllowsPreviewAfterGateLoadError(t *testing.T) {
	orch := NewJobOrchestrator(OrchestratorDeps{KalshiSettlementGateRepo: &kalshiSettlementGateStub{err: errors.New("boom")}})
	orch.Register("kalshi_settlement", "test", schedulerSpecEveryMinute(), func(context.Context) error { return nil })
	if err := orch.SetEnabled("kalshi_settlement", true); err != nil {
		t.Fatalf("SetEnabled(true) error = %v, job itself enforces the mutation gate", err)
	}
}

func TestJobOrchestratorSetEnabledKalshiSettlementDoesNotUseHealthLatchForScheduling(t *testing.T) {
	orch := NewJobOrchestrator(OrchestratorDeps{KalshiSettlementGateRepo: &kalshiSettlementGateStub{state: &domain.KalshiSettlementGateState{Eligible: true}}})
	orch.Register("kalshi_settlement", "test", schedulerSpecEveryMinute(), func(context.Context) error { return nil })
	orch.kalshiGateUnhealthy = true
	if err := orch.SetEnabled("kalshi_settlement", false); err != nil {
		t.Fatalf("SetEnabled(false) error = %v", err)
	}
	if err := orch.SetEnabled("kalshi_settlement", true); err != nil {
		t.Fatalf("SetEnabled(true) error = %v, preview scheduler should recover health", err)
	}
}

func TestStrategyResweepSkipsKalshiBeforeOHLCVDownload(t *testing.T) {
	t.Parallel()

	orch := NewJobOrchestrator(OrchestratorDeps{
		StrategyRepo: &kalshiStrategyRepoStub{strategies: []domain.Strategy{
			{
				ID:         uuid.New(),
				Name:       "auto: kalshi KXMENWORLDCUP-26-US",
				Ticker:     "KXMENWORLDCUP-26-US",
				MarketType: domain.MarketTypeKalshi,
				Status:     domain.StrategyStatusActive,
				IsPaper:    true,
			},
		}},
	})
	orch.Register("strategy_resweep", "test", schedulerSpecEveryMinute(), orch.strategyResweep)

	if err := orch.strategyResweep(context.Background()); err != nil {
		t.Fatalf("strategyResweep() error = %v", err)
	}
	summary := singleJobStatus(t, orch, "strategy_resweep").LastSummary
	if summary["strategies"] != 1 || summary["supported"] != 0 || summary["skipped"] != 1 {
		t.Fatalf("strategyResweep() summary = %#v, want one skipped unsupported strategy", summary)
	}
}

func TestStrategyTournamentSkipsKalshiBeforeOHLCVDownload(t *testing.T) {
	t.Parallel()

	orch := NewJobOrchestrator(OrchestratorDeps{
		StrategyRepo: &kalshiStrategyRepoStub{strategies: []domain.Strategy{
			{
				ID:         uuid.New(),
				Name:       "auto: kalshi KXMENWORLDCUP-26-US",
				Ticker:     "KXMENWORLDCUP-26-US",
				MarketType: domain.MarketTypeKalshi,
				Status:     domain.StrategyStatusActive,
				IsPaper:    true,
			},
		}},
	})
	orch.Register("strategy_tournament", "test", schedulerSpecEveryMinute(), orch.strategyTournament)

	if err := orch.strategyTournament(context.Background()); err != nil {
		t.Fatalf("strategyTournament() error = %v", err)
	}
	summary := singleJobStatus(t, orch, "strategy_tournament").LastSummary
	if summary["scanned"] != 1 || summary["supported"] != 0 || summary["skipped"] != 1 {
		t.Fatalf("strategyTournament() summary = %#v, want one skipped unsupported strategy", summary)
	}
}

func TestStrategyCoverageJobsRequireDataServiceForSupportedStrategies(t *testing.T) {
	t.Parallel()

	strategies := []domain.Strategy{{
		ID:         uuid.New(),
		Name:       "invalid config must not be parsed",
		Ticker:     "AAPL",
		MarketType: domain.MarketTypeStock,
		Status:     domain.StrategyStatusActive,
		Config:     json.RawMessage(`not JSON`),
	}}
	for _, tc := range []struct {
		name string
		run  func(*JobOrchestrator) error
	}{
		{name: "resweep", run: func(o *JobOrchestrator) error { return o.strategyResweep(context.Background()) }},
		{name: "tournament", run: func(o *JobOrchestrator) error { return o.strategyTournament(context.Background()) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orch := NewJobOrchestrator(OrchestratorDeps{StrategyRepo: &kalshiStrategyRepoStub{strategies: strategies}})
			err := tc.run(orch)
			if err == nil || IsDegraded(err) || !strings.Contains(err.Error(), "data service is required for supported strategies") {
				t.Fatalf("job error = %v, want true data service preflight error", err)
			}
		})
	}
}

func TestSupportsOHLCVResweep(t *testing.T) {
	t.Parallel()

	for _, marketType := range []domain.MarketType{domain.MarketTypeStock, domain.MarketTypeCrypto} {
		if !eventmarkets.SupportsOHLCVResweep(marketType) {
			t.Fatalf("SupportsOHLCVResweep(%q) = false, want true", marketType)
		}
	}
	for _, marketType := range []domain.MarketType{domain.MarketTypeKalshi, domain.MarketTypePolymarket, domain.MarketTypeOptions} {
		if eventmarkets.SupportsOHLCVResweep(marketType) {
			t.Fatalf("SupportsOHLCVResweep(%q) = true, want false", marketType)
		}
	}
}

func TestJobOrchestratorAlpacaReconcileRecordsMetricsAndSummary(t *testing.T) {
	t.Parallel()

	metrics := &stubAutomationMetrics{}
	orch := NewJobOrchestrator(OrchestratorDeps{Logger: slog.Default()})
	orch.WithJobMetrics(metrics)
	orch.Register("alpaca_reconcile", "test job", schedulerSpecEveryMinute(), func(context.Context) error {
		orch.SetLastSummary("alpaca_reconcile", map[string]int{"orders_created": 1})
		metrics.RecordAlpacaReconcileRun("success")
		return nil
	})

	if err := orch.RunJob(context.Background(), "alpaca_reconcile"); err != nil {
		t.Fatalf("RunJob() error = %v", err)
	}
	waitForJobRuns(t, orch, "alpaca_reconcile", 1)

	status := singleJobStatus(t, orch, "alpaca_reconcile")
	if status.LastSummary == nil || status.LastSummary["orders_created"] != 1 {
		t.Fatalf("LastSummary = %#v, want orders_created=1", status.LastSummary)
	}
	if metrics.alpacaRuns["success"] != 1 {
		t.Fatalf("alpaca success runs = %d, want 1", metrics.alpacaRuns["success"])
	}
}

func waitForJobRuns(t *testing.T, orch *JobOrchestrator, jobName string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status := singleJobStatus(t, orch, jobName)
		if status.RunCount >= want && !status.Running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach run_count=%d", jobName, want)
}

func singleJobStatus(t *testing.T, orch *JobOrchestrator, jobName string) JobStatus {
	t.Helper()
	for _, status := range orch.Status() {
		if status.Name == jobName {
			return status
		}
	}
	t.Fatalf("job status %q not found", jobName)
	return JobStatus{}
}

func schedulerSpecEveryMinute() scheduler.ScheduleSpec {
	return scheduler.ScheduleSpec{Cron: "* * * * *", Type: scheduler.ScheduleTypeCron}
}

type kalshiCatalogStub struct{}

func (kalshiCatalogStub) ListMarkets(context.Context, kalshidiscovery.ListOptions) ([]kalshidiscovery.MarketCandidate, string, error) {
	return nil, "", nil
}

func (kalshiCatalogStub) GetMarket(context.Context, string) (*kalshidiscovery.MarketCandidate, error) {
	return nil, nil
}

type kalshiStrategyRepoStub struct {
	strategies []domain.Strategy
}

func (s *kalshiStrategyRepoStub) Create(context.Context, *domain.Strategy) error { return nil }
func (s *kalshiStrategyRepoStub) Get(context.Context, uuid.UUID) (*domain.Strategy, error) {
	return nil, repository.ErrNotFound
}

func (s *kalshiStrategyRepoStub) List(context.Context, repository.StrategyFilter, int, int) ([]domain.Strategy, error) {
	return append([]domain.Strategy(nil), s.strategies...), nil
}

func (s *kalshiStrategyRepoStub) Count(context.Context, repository.StrategyFilter) (int, error) {
	return len(s.strategies), nil
}
func (s *kalshiStrategyRepoStub) Update(context.Context, *domain.Strategy) error { return nil }
func (s *kalshiStrategyRepoStub) Delete(context.Context, uuid.UUID) error        { return nil }
func (s *kalshiStrategyRepoStub) UpdateThesis(context.Context, uuid.UUID, json.RawMessage) error {
	return nil
}

func (s *kalshiStrategyRepoStub) GetThesisRaw(context.Context, uuid.UUID) (json.RawMessage, error) {
	return nil, nil
}

type kalshiWatchedRepoStub struct{}

func (s *kalshiWatchedRepoStub) Upsert(context.Context, *domain.KalshiWatchedMarket) error {
	return nil
}
func (s *kalshiWatchedRepoStub) SetEnabled(context.Context, string, bool) error { return nil }
func (s *kalshiWatchedRepoStub) ListEnabled(context.Context) ([]domain.KalshiWatchedMarket, error) {
	return nil, nil
}

type kalshiSnapshotsRepoStub struct{}

func (s *kalshiSnapshotsRepoStub) Create(context.Context, *domain.KalshiMarketSnapshot) error {
	return nil
}

func (s *kalshiSnapshotsRepoStub) ListLatestByTicker(context.Context, string, int) ([]domain.KalshiMarketSnapshot, error) {
	return nil, nil
}

func (s *kalshiSnapshotsRepoStub) ListRecent(context.Context, int) ([]domain.KalshiMarketSnapshot, error) {
	return nil, nil
}

type kalshiDiscoveryRunsRepoStub struct{}

func (s *kalshiDiscoveryRunsRepoStub) Create(context.Context, *domain.KalshiDiscoveryRun) error {
	return nil
}

func (s *kalshiDiscoveryRunsRepoStub) GetActive(context.Context) (*domain.KalshiDiscoveryRun, error) {
	return nil, repository.ErrNotFound
}

func (s *kalshiDiscoveryRunsRepoStub) Finish(context.Context, *domain.KalshiDiscoveryRun) error {
	return nil
}

func (s *kalshiDiscoveryRunsRepoStub) ListLatest(context.Context, int) ([]domain.KalshiDiscoveryRun, error) {
	return nil, nil
}

type polymarketBrokerStub struct {
	positions []domain.Position
}

func (s *polymarketBrokerStub) SubmitOrder(context.Context, *domain.Order) (string, error) {
	return "", nil
}

func (s *polymarketBrokerStub) CancelOrder(context.Context, string) error { return nil }

func (s *polymarketBrokerStub) GetOrderStatus(context.Context, string) (domain.OrderStatus, error) {
	return "", nil
}

func (s *polymarketBrokerStub) GetPositions(context.Context) ([]domain.Position, error) {
	return append([]domain.Position(nil), s.positions...), nil
}

func (s *polymarketBrokerStub) GetAccountBalance(context.Context) (execution.Balance, error) {
	return execution.Balance{}, nil
}

type polymarketPositionRepoStub struct {
	positions []domain.Position
}

func (s *polymarketPositionRepoStub) Create(context.Context, *domain.Position) error { return nil }
func (s *polymarketPositionRepoStub) CreateAlpacaOwned(context.Context, *domain.Position) error {
	return nil
}

func (s *polymarketPositionRepoStub) Get(context.Context, uuid.UUID) (*domain.Position, error) {
	return nil, repository.ErrNotFound
}

func (s *polymarketPositionRepoStub) List(context.Context, repository.PositionFilter, int, int) ([]domain.Position, error) {
	return nil, nil
}

func (s *polymarketPositionRepoStub) Count(context.Context, repository.PositionFilter) (int, error) {
	return 0, nil
}
func (s *polymarketPositionRepoStub) Update(context.Context, *domain.Position) error { return nil }
func (s *polymarketPositionRepoStub) Delete(context.Context, uuid.UUID) error        { return nil }
func (s *polymarketPositionRepoStub) GetOpen(context.Context, repository.PositionFilter, int, int) ([]domain.Position, error) {
	return append([]domain.Position(nil), s.positions...), nil
}

func (s *polymarketPositionRepoStub) ListOpenAlpacaOwned(context.Context, int, int) ([]domain.Position, error) {
	return nil, nil
}

func (s *polymarketPositionRepoStub) CountOpen(context.Context, repository.PositionFilter) (int, error) {
	return len(s.positions), nil
}

func (s *polymarketPositionRepoStub) GetByStrategy(context.Context, uuid.UUID, repository.PositionFilter, int, int) ([]domain.Position, error) {
	return nil, nil
}

type polymarketAuditRepoStub struct{}

func (s *polymarketAuditRepoStub) Create(context.Context, *domain.AuditLogEntry) error { return nil }

func (s *polymarketAuditRepoStub) Query(context.Context, repository.AuditLogFilter, int, int) ([]domain.AuditLogEntry, error) {
	return nil, nil
}

func (s *polymarketAuditRepoStub) Count(context.Context, repository.AuditLogFilter) (int, error) {
	return 0, nil
}

type polymarketReconcilerMetricsStub struct{}

func (s *polymarketReconcilerMetricsStub) IncDrift(string) {}
