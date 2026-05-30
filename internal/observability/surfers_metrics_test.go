package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestSurfersMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewSurfersMetrics(reg)
	if got := testutil.CollectAndCount(m.wsConnections); got != 0 {
		t.Fatalf("wsConnections count=%d", got)
	}
	m.FeedMetrics().IncCounter("polymarket_ws_connections", map[string]string{"slug": "s1", "state": "active"})
	m.FeedMetrics().IncCounter("polymarket_ws_ticks_dropped_total", map[string]string{"slug": "s1", "reason": "backpressure"})
	m.RecorderMetrics().IncInserted("tick", 3)
	m.RecorderMetrics().ObserveLagSeconds("tick", 12)
	m.RecorderMetrics().IncDropped("book", 2)
	m.IncBreakerTripped("global", "dd")
	m.SetFillRate("strat", 0.7)
	m.IncGhostFill("strat")
	if got := testutil.ToFloat64(m.wsConnections.WithLabelValues("s1", "active")); got != 1 {
		t.Fatalf("wsConnections=%v", got)
	}
	if got := testutil.ToFloat64(m.wsTicksDropped.WithLabelValues("s1", "backpressure")); got != 1 {
		t.Fatalf("wsTicksDropped=%v", got)
	}
	if got := testutil.ToFloat64(m.recorderInserted.WithLabelValues("tick")); got != 3 {
		t.Fatalf("recorderInserted=%v", got)
	}
	if got := testutil.ToFloat64(m.recorderLag); got != 12 {
		t.Fatalf("recorderLag=%v", got)
	}
	if got := testutil.ToFloat64(m.breakerTripped.WithLabelValues("global", "dd")); got != 1 {
		t.Fatalf("breakerTripped=%v", got)
	}
	if got := testutil.ToFloat64(m.fillRate.WithLabelValues("strat")); got != 0.7 {
		t.Fatalf("fillRate=%v", got)
	}
	if got := testutil.ToFloat64(m.ghostFills.WithLabelValues("strat")); got != 1 {
		t.Fatalf("ghostFills=%v", got)
	}
}
