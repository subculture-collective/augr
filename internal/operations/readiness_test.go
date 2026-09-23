package operations

import "testing"

func TestBuildReadinessIsCapabilityScopedAndLiveFailClosed(t *testing.T) {
	report := BuildReadiness(BuildInput{Database: true, Schema: true, DecisionJournal: true, Scheduler: true, OptionsData: true, PolymarketData: true, PolymarketSettlement: true, KalshiData: false, KalshiSettlement: true, RecoveryDrillsPassed: true})
	if report.ReleaseReady {
		t.Fatal("release ready despite required Kalshi blocker")
	}
	if len(report.Capabilities) != 6 {
		t.Fatalf("capabilities = %d", len(report.Capabilities))
	}
	if report.Capabilities[3].Ready || len(report.Capabilities[3].Blockers) != 1 {
		t.Fatalf("kalshi = %+v", report.Capabilities[3])
	}
	if report.Capabilities[5].Ready || report.Capabilities[5].Required {
		t.Fatalf("live capability = %+v", report.Capabilities[5])
	}
}

func TestLiveExecutionBlockersNameConfigurationGaps(t *testing.T) {
	blockers := LiveExecutionBlockers(BuildInput{AccountEnvironment: "paper_scored"})
	want := []string{
		"incremental operator activation required",
		"ENABLE_LIVE_TRADING=false",
		"LIVE_TRADING_ALLOWED_STRATEGIES is empty",
		"LIVE_TRADING_ALLOWED_BROKERS is empty",
		"account environment is paper_scored (runtime supports paper accounts only)",
	}
	if len(blockers) != len(want) {
		t.Fatalf("blockers = %v, want %v", blockers, want)
	}
	for i := range want {
		if blockers[i] != want[i] {
			t.Fatalf("blockers[%d] = %q, want %q", i, blockers[i], want[i])
		}
	}

	full := LiveExecutionBlockers(BuildInput{LiveTradingEnabled: true, LiveTradingAllowedStrategies: []string{"s"}, LiveTradingAllowedBrokers: []string{"alpaca"}, AccountEnvironment: "live"})
	if len(full) != 1 || full[0] != "incremental operator activation required" {
		t.Fatalf("fully configured blockers = %v, want only the activation requirement", full)
	}
	report := BuildReadiness(BuildInput{LiveTradingEnabled: true, LiveTradingAllowedStrategies: []string{"s"}, LiveTradingAllowedBrokers: []string{"alpaca"}, AccountEnvironment: "live"})
	if report.Capabilities[5].Ready {
		t.Fatal("live_execution must stay blocked even when configuration is complete")
	}
}

func TestBuildReadinessDoesNotRequireRetiredPolymarketCapability(t *testing.T) {
	report := BuildReadiness(BuildInput{
		Database:             true,
		Schema:               true,
		DecisionJournal:      true,
		Scheduler:            true,
		OptionsData:          true,
		KalshiData:           true,
		KalshiSettlement:     true,
		RecoveryDrillsPassed: true,
		PolymarketData:       false,
		PolymarketSettlement: false,
	})
	if !report.ReleaseReady {
		t.Fatalf("release not ready with only optional Polymarket blockers: %+v", report)
	}
	polymarket := report.Capabilities[2]
	if polymarket.Required || polymarket.Ready || len(polymarket.Blockers) != 2 {
		t.Fatalf("polymarket = %+v, want optional and visibly blocked", polymarket)
	}
}
