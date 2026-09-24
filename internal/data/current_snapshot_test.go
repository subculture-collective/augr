package data

import (
	"testing"
	"time"
)

func TestCurrentSnapshotAsOf(t *testing.T) {
	t.Parallel()

	to := time.Date(2026, 9, 24, 14, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		measured, to, want time.Time
	}{
		"seconds after the window end": {to.Add(3 * time.Second), to, to},
		"at the skew limit":            {to.Add(CurrentSnapshotWindowSkew), to, to},
		"historical window":            {to.Add(time.Hour), to, to.Add(time.Hour)},
		"inside the window":            {to.Add(-time.Minute), to, to.Add(-time.Minute)},
		"no window end":                {to, time.Time{}, to},
	} {
		if got := CurrentSnapshotAsOf(tc.measured, tc.to); !got.Equal(tc.want) {
			t.Errorf("%s: CurrentSnapshotAsOf() = %s, want %s", name, got, tc.want)
		}
	}
}
