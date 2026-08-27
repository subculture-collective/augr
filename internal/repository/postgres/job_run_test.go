package postgres

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fakeJobRunRows struct {
	rows [][]any
	idx  int
	err  error
}

func (f *fakeJobRunRows) Next() bool {
	if f.idx >= len(f.rows) {
		return false
	}
	f.idx++
	return true
}

func (f *fakeJobRunRows) Scan(dest ...any) error {
	row := f.rows[f.idx-1]
	for i := range dest {
		switch d := dest[i].(type) {
		case *uuid.UUID:
			*d = row[i].(uuid.UUID)
		case *string:
			*d = row[i].(string)
		case **time.Time:
			if row[i] == nil {
				*d = nil
				continue
			}
			v := row[i].(time.Time)
			*d = &v
		case *int64:
			*d = row[i].(int64)
		case *int:
			*d = row[i].(int)
		case *time.Time:
			*d = row[i].(time.Time)
		case *[]byte:
			if row[i] == nil {
				*d = nil
				continue
			}
			*d = row[i].([]byte)
		case **string:
			if row[i] == nil {
				*d = nil
				continue
			}
			v := row[i].(string)
			*d = &v
		default:
			panic("unexpected scan destination")
		}
	}
	return nil
}

func (f *fakeJobRunRows) Err() error { return f.err }

func TestScanJobRunsIncludesResult(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	completed := started.Add(2 * time.Minute)
	lastErrAt := started.Add(90 * time.Second)
	created := started.Add(3 * time.Minute)
	rows := &fakeJobRunRows{rows: [][]any{{
		uuid.New(),
		"options_discovery",
		"ok",
		started,
		completed,
		int64(12345),
		[]byte(`{"candidates":12,"winners":3}`),
		nil,
		lastErrAt,
		7,
		created,
	}}}

	runs, err := scanJobRuns(rows)
	if err != nil {
		t.Fatalf("scanJobRuns error = %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("len(runs) = %d, want 1", len(runs))
	}
	if runs[0].Result["candidates"] != 12 || runs[0].Result["winners"] != 3 {
		t.Fatalf("Result = %#v, want counts preserved", runs[0].Result)
	}
	if runs[0].CompletedAt == nil || !runs[0].CompletedAt.Equal(completed) {
		t.Fatalf("CompletedAt = %v, want %v", runs[0].CompletedAt, completed)
	}
	if runs[0].LastErrorAt == nil || !runs[0].LastErrorAt.Equal(lastErrAt) {
		t.Fatalf("LastErrorAt = %v, want %v", runs[0].LastErrorAt, lastErrAt)
	}
}

func TestJobRunResultJSONBackwardCompatibility(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		raw         string
		wantCount   int
		wantTickers []string
		wantDetail  string
	}{
		{name: "legacy counts", raw: `{"processed":7}`, wantCount: 7},
		{name: "counts and tickers", raw: `{"processed":7,"_tickers":["AAPL","MSFT"]}`, wantCount: 7, wantTickers: []string{"AAPL", "MSFT"}},
		{name: "counts tickers and detail", raw: `{"processed":7,"_tickers":["AAPL","MSFT"],"_detail":"partial provider response"}`, wantCount: 7, wantTickers: []string{"AAPL", "MSFT"}, wantDetail: "partial provider response"},
	} {
		t.Run(test.name, func(t *testing.T) {
			counts, tickers, detail, err := decodeJobRunResult([]byte(test.raw))
			if err != nil {
				t.Fatal(err)
			}
			if counts["processed"] != test.wantCount || strings.Join(tickers, ",") != strings.Join(test.wantTickers, ",") || detail != test.wantDetail {
				t.Fatalf("decoded counts=%v tickers=%v detail=%q", counts, tickers, detail)
			}
		})
	}

	encoded, err := encodeJobRunResult(map[string]int{"processed": 7}, []string{"AAPL", "MSFT"}, "partial provider response")
	if err != nil {
		t.Fatal(err)
	}
	counts, tickers, detail, err := decodeJobRunResult(encoded)
	if err != nil || counts["processed"] != 7 || strings.Join(tickers, ",") != "AAPL,MSFT" || detail != "partial provider response" {
		t.Fatalf("round trip counts=%v tickers=%v detail=%q err=%v", counts, tickers, detail, err)
	}
}

func TestJobRunResultJSONRejectsMalformedPayloads(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		`{"_tickers":"AAPL"}`,
		`{"_tickers":["AAPL",2]}`,
		`{"_tickers":null}`,
		`{"_detail":7}`,
		`{"_detail":null}`,
		`{"_detail":"first","_detail":"second"}`,
		`{"_tickers":[],"_tickers":[]}`,
		`{"processed":1,"processed":2}`,
		`{"processed":1.5}`,
		`{"processed":1} {"other":2}`,
		`[]`,
		`null`,
	} {
		if _, _, _, err := decodeJobRunResult([]byte(raw)); err == nil {
			t.Errorf("decodeJobRunResult(%s) error = nil", raw)
		}
	}
	for _, key := range []string{jobRunTickersKey, jobRunDetailKey} {
		if _, err := encodeJobRunResult(map[string]int{key: 1}, nil, ""); err == nil {
			t.Errorf("reserved count key %q encoded without error", key)
		}
	}
}

func TestJobRunJSONExposesDegradedDetailNotError(t *testing.T) {
	t.Parallel()
	payload, err := json.Marshal(JobRun{Status: "degraded", Detail: "partial provider response"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["detail"] != "partial provider response" {
		t.Fatalf("detail = %#v", decoded["detail"])
	}
	if _, exists := decoded["error"]; exists {
		t.Fatalf("degraded API payload exposes error: %s", payload)
	}
}

func TestJobRunJSONExposesDependencySkipDetailNotError(t *testing.T) {
	t.Parallel()
	payload, err := json.Marshal(JobRun{
		Status: "skipped",
		Result: map[string]int{"dependency_blocked": 1},
		Detail: "dependency current_data_refresh still running",
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["detail"] != "dependency current_data_refresh still running" {
		t.Fatalf("detail = %#v", decoded["detail"])
	}
	if _, exists := decoded["error"]; exists {
		t.Fatalf("dependency skip API payload exposes error: %s", payload)
	}
}

func TestJobRunLifecycleIntegration(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newJobRunIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewJobRunRepo(pool)
	started := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	run := &JobRun{JobName: "integration_job", Status: "running", StartedAt: started}
	if err := repo.Create(ctx, run); err != nil {
		t.Fatalf("Create(running) error = %v", err)
	}
	var status string
	var completedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT status, completed_at FROM automation_job_runs WHERE id=$1`, run.ID).Scan(&status, &completedAt); err != nil {
		t.Fatal(err)
	}
	if status != "running" || completedAt != nil {
		t.Fatalf("admission row status=%q completed_at=%v", status, completedAt)
	}

	completed := started.Add(10 * time.Second)
	run.Status = "ok"
	run.CompletedAt = &completed
	run.DurationNs = int64(10 * time.Second)
	run.Result = map[string]int{"items": 3}
	run.Tickers = []string{"AAPL", "MSFT"}
	if err := repo.Complete(ctx, run); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	stored, err := repo.ListByJob(ctx, run.JobName, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].ID != run.ID || stored[0].Status != "ok" || stored[0].CompletedAt == nil || stored[0].Result["items"] != 3 || strings.Join(stored[0].Tickers, ",") != "AAPL,MSFT" {
		t.Fatalf("completed row = %+v", stored)
	}
	summaries, err := repo.Summaries(ctx)
	if err != nil {
		t.Fatalf("Summaries() error = %v", err)
	}
	if len(summaries) != 1 || summaries[0].LastRun == nil || !summaries[0].LastRun.Equal(completed) || summaries[0].LastResult != "ok" || summaries[0].LastSummary["items"] != 3 || strings.Join(summaries[0].LastTickers, ",") != "AAPL,MSFT" {
		t.Fatalf("completed summary = %+v, want terminal time %v", summaries, completed)
	}

	degraded := &JobRun{JobName: run.JobName, Status: "running", StartedAt: completed.Add(time.Second)}
	if err := repo.Create(ctx, degraded); err != nil {
		t.Fatal(err)
	}
	degradedAt := degraded.StartedAt.Add(5 * time.Second)
	degraded.Status = "degraded"
	degraded.CompletedAt = &degradedAt
	degraded.DurationNs = int64(5 * time.Second)
	degraded.Result = map[string]int{"items": 2}
	degraded.Tickers = []string{"AAPL"}
	degraded.Detail = "partial provider response"
	degraded.Error = ""
	degraded.LastErrorAt = nil
	degraded.ConsecutiveFailures = 0
	if err := repo.Complete(ctx, degraded); err != nil {
		t.Fatal(err)
	}
	var rawResult []byte
	var rawError *string
	var rawLastErrorAt *time.Time
	var rawStreak int
	if err := pool.QueryRow(ctx, `SELECT result, error, last_error_at, consecutive_failures FROM automation_job_runs WHERE id=$1`, degraded.ID).Scan(&rawResult, &rawError, &rawLastErrorAt, &rawStreak); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawResult), `"_detail": "partial provider response"`) || rawError != nil || rawLastErrorAt != nil || rawStreak != 0 {
		t.Fatalf("raw degraded row result=%s error=%v last_error_at=%v streak=%d", rawResult, rawError, rawLastErrorAt, rawStreak)
	}
	degradedRuns, err := repo.ListByJob(ctx, run.JobName, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(degradedRuns) != 1 || degradedRuns[0].Status != "degraded" || degradedRuns[0].Error != "" || degradedRuns[0].Detail != "partial provider response" {
		t.Fatalf("degraded API model = %+v", degradedRuns)
	}
	summaries, err = repo.Summaries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].LastResult != "degraded" || summaries[0].LastError != "" || summaries[0].LastDetail != "partial provider response" {
		t.Fatalf("degraded summary = %+v", summaries)
	}

	skippedAt := degradedAt.Add(time.Second)
	skipped := &JobRun{
		JobName:             run.JobName,
		Status:              "skipped",
		StartedAt:           skippedAt,
		CompletedAt:         &skippedAt,
		Result:              map[string]int{"dependency_blocked": 1},
		Detail:              "dependency current_data_refresh still running",
		ConsecutiveFailures: 5,
	}
	if err := repo.Create(ctx, skipped); err != nil {
		t.Fatal(err)
	}
	var skippedStatus string
	if err := pool.QueryRow(ctx, `SELECT status, result, error, consecutive_failures FROM automation_job_runs WHERE id=$1`, skipped.ID).Scan(&skippedStatus, &rawResult, &rawError, &rawStreak); err != nil {
		t.Fatal(err)
	}
	if skippedStatus != "skipped" || !strings.Contains(string(rawResult), `"dependency_blocked": 1`) || !strings.Contains(string(rawResult), `"_detail": "dependency current_data_refresh still running"`) || rawError != nil || rawStreak != 5 {
		t.Fatalf("raw dependency skip status=%q result=%s error=%v streak=%d", skippedStatus, rawResult, rawError, rawStreak)
	}
	skippedRuns, err := repo.ListByJob(ctx, run.JobName, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(skippedRuns) != 1 || skippedRuns[0].Status != "skipped" || skippedRuns[0].Error != "" || skippedRuns[0].Detail != skipped.Detail || skippedRuns[0].Result["dependency_blocked"] != 1 {
		t.Fatalf("dependency skip API model = %+v", skippedRuns)
	}
	summaries, err = repo.Summaries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].LastResult != "skipped" || summaries[0].LastError != "" || summaries[0].LastDetail != skipped.Detail || summaries[0].ConsecutiveFailures != 5 {
		t.Fatalf("dependency skip summary = %+v", summaries)
	}

	legacyID := uuid.New()
	legacyAt := skippedAt.Add(time.Second)
	if _, err := pool.Exec(ctx, `INSERT INTO automation_job_runs (id, job_name, status, started_at, completed_at, result, error, consecutive_failures) VALUES ($1, $2, 'degraded', $3, $3, '{"items":1}', $4, 0)`, legacyID, "legacy_degraded_job", legacyAt, "legacy partial result"); err != nil {
		t.Fatal(err)
	}
	legacyRuns, err := repo.ListByJob(ctx, "legacy_degraded_job", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(legacyRuns) != 1 || legacyRuns[0].Error != "legacy partial result" || legacyRuns[0].Detail != "" {
		t.Fatalf("legacy raw history = %+v", legacyRuns)
	}
	summaries, err = repo.Summaries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	legacySummaryFound := false
	for _, summary := range summaries {
		if summary.JobName == "legacy_degraded_job" {
			legacySummaryFound = true
			if summary.LastError != "" || summary.LastDetail != "legacy partial result" {
				t.Fatalf("legacy degraded summary = %+v", summary)
			}
		}
	}
	if !legacySummaryFound {
		t.Fatalf("summaries = %+v, missing legacy degraded job", summaries)
	}

	orphan := &JobRun{JobName: "orphaned_job", Status: "running", StartedAt: started}
	if err := repo.Create(ctx, orphan); err != nil {
		t.Fatal(err)
	}
	recoveredAt := completed.Add(time.Minute)
	recovered, err := repo.FailIncomplete(ctx, recoveredAt, "process restarted")
	if err != nil {
		t.Fatalf("FailIncomplete() error = %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered = %d, want 1", recovered)
	}
	orphans, err := repo.ListByJob(ctx, orphan.JobName, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 || orphans[0].Status != "error" || orphans[0].CompletedAt == nil || orphans[0].Error != "process restarted" || orphans[0].ConsecutiveFailures != 1 {
		t.Fatalf("recovered row = %+v", orphans)
	}
	summaries, err = repo.Summaries(ctx)
	if err != nil {
		t.Fatalf("Summaries() after recovery error = %v", err)
	}
	foundOrphan := false
	for _, summary := range summaries {
		if summary.JobName == orphan.JobName {
			foundOrphan = true
			if summary.LastRun == nil || !summary.LastRun.Equal(recoveredAt) || summary.LastResult != "error" {
				t.Fatalf("recovered summary = %+v, want terminal time %v", summary, recoveredAt)
			}
		}
	}
	if !foundOrphan {
		t.Fatalf("Summaries() = %+v, missing %q", summaries, orphan.JobName)
	}
}

func newJobRunIntegrationPool(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
	t.Helper()
	connString := os.Getenv("DB_URL")
	if connString == "" {
		connString = os.Getenv("DATABASE_URL")
	}
	if connString == "" {
		t.Skip("skipping integration test: DB_URL or DATABASE_URL is not set")
	}
	admin, err := pgxpool.New(ctx, connString)
	if err != nil {
		t.Fatalf("connect integration database: %v", err)
	}
	schema := "job_run_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatalf("create integration schema: %v", err)
	}
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `CREATE TABLE automation_job_runs (
		id uuid PRIMARY KEY,
		job_name text NOT NULL,
		status text NOT NULL,
		started_at timestamptz NOT NULL,
		completed_at timestamptz,
		duration_ns bigint,
		result jsonb,
		error text,
		last_error_at timestamptz,
		consecutive_failures integer NOT NULL DEFAULT 0,
		created_at timestamptz NOT NULL DEFAULT now()
	)`)
	if err != nil {
		pool.Close()
		_, _ = admin.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
		t.Fatalf("create job run table: %v", err)
	}
	return pool, func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	}
}
