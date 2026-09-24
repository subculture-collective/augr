package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// JobRun represents a single execution of an automation job.
type JobRun struct {
	ID                  uuid.UUID      `json:"id"`
	JobName             string         `json:"job_name"`
	Status              string         `json:"status"`
	StartedAt           time.Time      `json:"started_at"`
	CompletedAt         *time.Time     `json:"completed_at,omitempty"`
	DurationNs          int64          `json:"duration_ns,omitempty"`
	Result              map[string]int `json:"result,omitempty"`
	Tickers             []string       `json:"tickers,omitempty"`
	Error               string         `json:"error,omitempty"`
	Detail              string         `json:"detail,omitempty"`
	LastErrorAt         *time.Time     `json:"last_error_at,omitempty"`
	ConsecutiveFailures int            `json:"consecutive_failures"`
	CreatedAt           time.Time      `json:"created_at"`
}

// JobRunSummary holds aggregate stats for a single job name.
type JobRunSummary struct {
	JobName             string         `json:"job_name"`
	LastRun             *time.Time     `json:"last_run,omitempty"`
	LastResult          string         `json:"last_result"`
	LastSummary         map[string]int `json:"last_summary,omitempty"`
	LastError           string         `json:"last_error,omitempty"`
	LastDetail          string         `json:"last_detail,omitempty"`
	LastTickers         []string       `json:"last_tickers,omitempty"`
	LastErrorAt         *time.Time     `json:"last_error_at,omitempty"`
	RunCount            int            `json:"run_count"`
	ErrorCount          int            `json:"error_count"`
	ConsecutiveFailures int            `json:"consecutive_failures"`
}

const (
	jobRunTickersKey = "_tickers"
	jobRunDetailKey  = "_detail"
)

func encodeJobRunResult(result map[string]int, tickers []string, detail string) ([]byte, error) {
	if result == nil && tickers == nil && detail == "" {
		return nil, nil
	}
	payload := make(map[string]any, len(result)+2)
	for key, value := range result {
		if key == jobRunTickersKey || key == jobRunDetailKey {
			return nil, fmt.Errorf("reserved result key %q", key)
		}
		payload[key] = value
	}
	if tickers != nil {
		payload[jobRunTickersKey] = tickers
	}
	if detail != "" {
		payload[jobRunDetailKey] = detail
	}
	return json.Marshal(payload)
}

func decodeJobRunResult(raw []byte) (map[string]int, []string, string, error) {
	if len(raw) == 0 {
		return nil, nil, "", nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil {
		return nil, nil, "", err
	}
	if opening != json.Delim('{') {
		return nil, nil, "", fmt.Errorf("result must be a JSON object")
	}
	counts := make(map[string]int)
	seen := make(map[string]struct{})
	var tickers []string
	var detail string
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, nil, "", err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, nil, "", fmt.Errorf("result key must be a string")
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, nil, "", fmt.Errorf("duplicate result key %q", key)
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, nil, "", err
		}
		switch key {
		case jobRunTickersKey:
			if err := json.Unmarshal(value, &tickers); err != nil || tickers == nil {
				return nil, nil, "", fmt.Errorf("%s must be a string array", jobRunTickersKey)
			}
		case jobRunDetailKey:
			var decoded any
			if err := json.Unmarshal(value, &decoded); err != nil {
				return nil, nil, "", fmt.Errorf("%s must be a string", jobRunDetailKey)
			}
			var ok bool
			detail, ok = decoded.(string)
			if !ok {
				return nil, nil, "", fmt.Errorf("%s must be a string", jobRunDetailKey)
			}
		default:
			// Counts are integers. Non-integer values (floats, strings,
			// objects) written by older or foreign writers are skipped so one
			// odd key cannot block orchestrator hydration.
			var count int
			if err := json.Unmarshal(value, &count); err != nil {
				continue
			}
			counts[key] = count
		}
	}
	if _, err := decoder.Token(); err != nil {
		return nil, nil, "", err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("unexpected trailing JSON value")
		}
		return nil, nil, "", err
	}
	if len(counts) == 0 {
		counts = nil
	}
	return counts, tickers, detail, nil
}

// JobRunRepo persists automation job runs to PostgreSQL.
type JobRunRepo struct {
	pool *pgxpool.Pool
}

// NewJobRunRepo returns a new JobRunRepo.
func NewJobRunRepo(pool *pgxpool.Pool) *JobRunRepo {
	return &JobRunRepo{pool: pool}
}

// Create inserts a new job run record.
func (r *JobRunRepo) Create(ctx context.Context, run *JobRun) error {
	run.ID = uuid.New()
	resultJSON, err := encodeJobRunResult(run.Result, run.Tickers, run.Detail)
	if err != nil {
		return fmt.Errorf("postgres: marshal job run result: %w", err)
	}
	row := r.pool.QueryRow(ctx,
		`INSERT INTO automation_job_runs (id, job_name, status, started_at, completed_at, duration_ns, result, error, last_error_at, consecutive_failures)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 RETURNING created_at`,
		run.ID, run.JobName, run.Status, run.StartedAt, run.CompletedAt, run.DurationNs, resultJSON, nullString(run.Error), run.LastErrorAt, run.ConsecutiveFailures,
	)
	return row.Scan(&run.CreatedAt)
}

// Complete updates an admitted running row with its terminal outcome.
func (r *JobRunRepo) Complete(ctx context.Context, run *JobRun) error {
	resultJSON, err := encodeJobRunResult(run.Result, run.Tickers, run.Detail)
	if err != nil {
		return fmt.Errorf("postgres: marshal completed job run result: %w", err)
	}
	commandTag, err := r.pool.Exec(ctx,
		`UPDATE automation_job_runs
		 SET status = $2, completed_at = $3, duration_ns = $4, result = $5,
		     error = $6, last_error_at = $7, consecutive_failures = $8
		 WHERE id = $1 AND completed_at IS NULL`,
		run.ID, run.Status, run.CompletedAt, run.DurationNs, resultJSON,
		nullString(run.Error), run.LastErrorAt, run.ConsecutiveFailures,
	)
	if err != nil {
		return fmt.Errorf("postgres: complete job run: %w", err)
	}
	if commandTag.RowsAffected() != 1 {
		return fmt.Errorf("postgres: complete job run %s: expected one running row, updated %d", run.ID, commandTag.RowsAffected())
	}
	return nil
}

// FailIncomplete marks rows left running by a prior app process as terminal
// errors before scheduler state is hydrated. Only rows started before
// completedAt are touched, so a run admitted by a concurrently starting
// process is not clobbered. A process restart is not a job failure: the
// consecutive_failures counter is preserved, not incremented. The table has
// no owner column; adding one (process instance id) would allow exact
// per-process scoping.
func (r *JobRunRepo) FailIncomplete(ctx context.Context, completedAt time.Time, reason string) (int, error) {
	commandTag, err := r.pool.Exec(ctx,
		`UPDATE automation_job_runs
		 SET status = 'error',
		     completed_at = $1,
		     duration_ns = GREATEST(0, (EXTRACT(EPOCH FROM ($1 - started_at)) * 1000000000)::bigint),
		     error = $2,
		     last_error_at = $1
		 WHERE completed_at IS NULL AND started_at < $1`,
		completedAt, reason,
	)
	if err != nil {
		return 0, fmt.Errorf("postgres: fail incomplete job runs: %w", err)
	}
	return int(commandTag.RowsAffected()), nil
}

// MarkStuck flags a still-running row whose job exceeded its timeout without
// returning. completed_at stays NULL so the eventual completion (or the next
// FailIncomplete recovery) still terminates the row.
func (r *JobRunRepo) MarkStuck(ctx context.Context, id uuid.UUID, at time.Time, reason string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE automation_job_runs
		 SET status = 'stuck', error = $2, last_error_at = $3
		 WHERE id = $1 AND completed_at IS NULL`,
		id, reason, at,
	)
	if err != nil {
		return fmt.Errorf("postgres: mark job run stuck: %w", err)
	}
	return nil
}

// ListByJob returns recent runs for a specific job, newest first.
func (r *JobRunRepo) ListByJob(ctx context.Context, jobName string, limit int) ([]JobRun, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, job_name, status, started_at, completed_at, COALESCE(duration_ns, 0), result, error, last_error_at, consecutive_failures, created_at
		 FROM automation_job_runs
		 WHERE job_name = $1
		 ORDER BY started_at DESC
		 LIMIT $2`,
		jobName, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: list job runs: %w", err)
	}
	defer rows.Close()
	return scanJobRuns(rows)
}

// List returns recent automation job runs, newest first.
func (r *JobRunRepo) List(ctx context.Context, limit, offset int) ([]JobRun, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, job_name, status, started_at, completed_at, COALESCE(duration_ns, 0), result, error, last_error_at, consecutive_failures, created_at
		 FROM automation_job_runs
		 ORDER BY started_at DESC
		 LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: list automation job runs: %w", err)
	}
	defer rows.Close()
	return scanJobRuns(rows)
}

// Count returns the total number of automation job run records.
func (r *JobRunRepo) Count(ctx context.Context) (int, error) {
	var count int
	if err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM automation_job_runs`).Scan(&count); err != nil {
		return 0, fmt.Errorf("postgres: count automation job runs: %w", err)
	}
	return count, nil
}

type jobRunRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

func scanJobRuns(rows jobRunRows) ([]JobRun, error) {
	var runs []JobRun
	for rows.Next() {
		var (
			run       JobRun
			resultRaw []byte
			errStr    *string
			completed *time.Time
			lastErrAt *time.Time
		)
		if err := rows.Scan(&run.ID, &run.JobName, &run.Status, &run.StartedAt, &completed, &run.DurationNs, &resultRaw, &errStr, &lastErrAt, &run.ConsecutiveFailures, &run.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan job run: %w", err)
		}
		if completed != nil {
			run.CompletedAt = completed
		}
		if len(resultRaw) > 0 {
			var err error
			run.Result, run.Tickers, run.Detail, err = decodeJobRunResult(resultRaw)
			if err != nil {
				return nil, fmt.Errorf("postgres: unmarshal job run result: %w", err)
			}
		}
		if errStr != nil {
			run.Error = *errStr
		}
		if lastErrAt != nil {
			run.LastErrorAt = lastErrAt
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// Summaries returns aggregate stats per job name, used to hydrate the
// orchestrator on startup. The latest row per job is selected with DISTINCT ON
// ordered by started_at DESC so the (job_name, started_at DESC) index serves
// the lookup in a single query.
func (r *JobRunRepo) Summaries(ctx context.Context) ([]JobRunSummary, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT
			agg.job_name,
			agg.last_run,
			agg.run_count,
			agg.error_count,
			latest.status,
			latest.error,
			latest.last_error_at,
			COALESCE(latest.consecutive_failures, 0),
			latest.result
		 FROM (
			SELECT job_name,
			       MAX(COALESCE(completed_at, started_at)) AS last_run,
			       COUNT(*) AS run_count,
			       COUNT(*) FILTER (WHERE status = 'error') AS error_count
			FROM automation_job_runs
			GROUP BY job_name
		 ) agg
		 JOIN (
			SELECT DISTINCT ON (job_name)
			       job_name, status, error, last_error_at, consecutive_failures, result
			FROM automation_job_runs
			ORDER BY job_name, started_at DESC
		 ) latest USING (job_name)
		 ORDER BY agg.job_name`,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: job run summaries: %w", err)
	}
	defer rows.Close()

	var summaries []JobRunSummary
	for rows.Next() {
		var (
			s         JobRunSummary
			status    string
			errStr    *string
			resultRaw []byte
		)
		if err := rows.Scan(&s.JobName, &s.LastRun, &s.RunCount, &s.ErrorCount, &status, &errStr, &s.LastErrorAt, &s.ConsecutiveFailures, &resultRaw); err != nil {
			return nil, fmt.Errorf("postgres: scan job run summary: %w", err)
		}
		s.LastResult = status
		if errStr != nil {
			if status == "degraded" {
				s.LastDetail = *errStr
			} else {
				s.LastError = *errStr
			}
		}
		counts, tickers, detail, decodeErr := decodeJobRunResult(resultRaw)
		if decodeErr != nil {
			return nil, fmt.Errorf("postgres: decode latest job run result for %q: %w", s.JobName, decodeErr)
		}
		s.LastTickers = tickers
		s.LastSummary = counts
		s.LastDetail = detail
		if status == "degraded" && detail == "" && errStr != nil {
			s.LastDetail = *errStr
		}
		summaries = append(summaries, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return summaries, nil
}
