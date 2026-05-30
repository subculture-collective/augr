package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
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
