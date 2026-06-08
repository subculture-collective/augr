package automation

import (
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

func TestPolymarketDiscoveryJobSpecIsHourly(t *testing.T) {
	want := scheduler.ScheduleSpec{Type: scheduler.ScheduleTypeCron, Cron: "0 * * * *"}
	if polymarketDiscoverySpec != want {
		t.Fatalf("polymarketDiscoverySpec = %+v, want %+v", polymarketDiscoverySpec, want)
	}
}
