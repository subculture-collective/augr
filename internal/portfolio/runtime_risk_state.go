package portfolio

// RuntimeRiskState is the database-derived portion of the allocator snapshot.
// Position exposure and Greeks are merged by the orchestrator from the same
// canonical account position read.
type RuntimeRiskState struct {
	DailyLossPct       float64
	DrawdownPct        float64
	NewOrdersToday     int
	CircuitBreakerOpen bool
	// OpenBreakerScopes lists every tripped, unreset breaker scope so the
	// allocator can reject only opportunities whose strategy scope is open.
	OpenBreakerScopes []string
	ReconciliationID  string
	UnderlyingRisk    map[string]float64
}
