package automation

import (
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
	"github.com/robfig/cron/v3"
)

// The operational observer manifest must use the same zone as the orchestrator.
// Treating these crons as UTC arms the observer four hours too early in September.
func TestQualificationSeptemberBoundariesUseEasternCron(t *testing.T) {
	monday := time.Date(2026, 9, 21, 10, 0, 0, 0, easternTime)
	if !scheduler.IsNYSETradingDay(monday) {
		t.Fatal("qualification Monday must be a trading day in the pinned calendar")
	}
	for _, tc := range []struct {
		name string
		spec scheduler.ScheduleSpec
		want string
	}{
		{"options", optionsScanSpec, "2026-09-22T02:00:00Z"},
		{"history", historyRefreshSpec, "2026-09-22T04:00:00Z"},
		{"sweep", overnightSweepSpec, "2026-09-22T04:30:00Z"},
		{"generate", overnightGenerateSpec, "2026-09-22T10:00:00Z"},
		{"discovery", optionsDiscoverySpec, "2026-09-22T10:30:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cron.New(cron.WithLocation(easternTime))
			id, err := c.AddFunc(tc.spec.Cron, func() {})
			if err != nil {
				t.Fatal(err)
			}
			next := c.Entry(id).Schedule.Next(monday)
			if got := next.UTC().Format(time.RFC3339); got != tc.want {
				t.Fatalf("boundary = %s, want %s", got, tc.want)
			}
			if !tc.spec.ShouldFire(next) {
				t.Fatal("the runtime session/calendar gate must admit this boundary")
			}
		})
	}
}
