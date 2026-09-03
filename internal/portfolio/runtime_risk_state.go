package portfolio

// RuntimeRiskState is the database-derived portion of the allocator snapshot.
// Position exposure and Greeks are merged by the orchestrator from the same
// canonical account position read.
type RuntimeRiskState struct {
	DailyLossPct       float64
	DrawdownPct        float64
	NewOrdersToday     int
	CircuitBreakerOpen bool
	ReconciliationID   string
	UnderlyingRisk     map[string]float64
}
