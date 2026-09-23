package operations

import (
	"context"
	"sort"
	"time"
)

type Capability struct {
	Name     string   `json:"name"`
	Mode     string   `json:"mode"`
	Ready    bool     `json:"ready"`
	Required bool     `json:"required"`
	Blockers []string `json:"blockers,omitempty"`
}

type ReadinessReport struct {
	ReleaseReady       bool         `json:"release_ready"`
	LiveTradingEnabled bool         `json:"live_trading_enabled"`
	Capabilities       []Capability `json:"capabilities"`
	GeneratedAt        time.Time    `json:"generated_at"`
}

type Source interface {
	Readiness(context.Context) (ReadinessReport, error)
}
type SourceFunc func(context.Context) (ReadinessReport, error)

func (f SourceFunc) Readiness(ctx context.Context) (ReadinessReport, error) { return f(ctx) }

type BuildInput struct {
	Database, Schema, DecisionJournal    bool
	Scheduler, OptionsData               bool
	PolymarketData, PolymarketSettlement bool
	KalshiData, KalshiSettlement         bool
	LiveTradingEnabled                   bool
	RecoveryDrillsPassed                 bool
	GeneratedAt                          time.Time
	// Live execution inputs. They only explain why live_execution is blocked;
	// live_execution is never reported ready by this report.
	LiveTradingAllowedStrategies []string
	LiveTradingAllowedBrokers    []string
	AccountEnvironment           string
}

// liveExecutionActivationBlocker is always present: live execution requires
// an explicit operator activation outside this readiness report.
const liveExecutionActivationBlocker = "incremental operator activation required"

// LiveExecutionBlockers lists the concrete configuration gaps that keep
// live_execution blocked, so an operator can see which switch is off rather
// than only the generic activation requirement.
func LiveExecutionBlockers(in BuildInput) []string {
	blockers := []string{liveExecutionActivationBlocker}
	if !in.LiveTradingEnabled {
		blockers = append(blockers, "ENABLE_LIVE_TRADING=false")
	}
	if len(in.LiveTradingAllowedStrategies) == 0 {
		blockers = append(blockers, "LIVE_TRADING_ALLOWED_STRATEGIES is empty")
	}
	if len(in.LiveTradingAllowedBrokers) == 0 {
		blockers = append(blockers, "LIVE_TRADING_ALLOWED_BROKERS is empty")
	}
	switch env := in.AccountEnvironment; env {
	case "":
		blockers = append(blockers, "account environment unknown")
	case "live":
		// A live account binding is the only environment that would not block.
	default:
		blockers = append(blockers, "account environment is "+env+" (runtime supports paper accounts only)")
	}
	return blockers
}

func BuildReadiness(in BuildInput) ReadinessReport {
	capability := func(name string, required bool, checks map[string]bool) Capability {
		c := Capability{Name: name, Mode: "paper", Required: required, Ready: true}
		for label, ok := range checks {
			if !ok {
				c.Ready = false
				c.Blockers = append(c.Blockers, label)
			}
		}
		sort.Strings(c.Blockers)
		return c
	}
	base := map[string]bool{"database unavailable": in.Database, "schema mismatch": in.Schema, "decision journal unavailable": in.DecisionJournal}
	capabilities := []Capability{
		capability("stocks", true, merge(base, map[string]bool{"scheduler unavailable": in.Scheduler})),
		capability("options", true, merge(base, map[string]bool{"scheduler unavailable": in.Scheduler, "options data unavailable": in.OptionsData})),
		// Polymarket remains visible for historical research, but it is not a
		// release requirement in deployments where venue access is retired.
		capability("polymarket", false, merge(base, map[string]bool{"polymarket data unavailable": in.PolymarketData, "settlement job unavailable": in.PolymarketSettlement})),
		capability("kalshi", true, merge(base, map[string]bool{"kalshi data unavailable": in.KalshiData, "settlement job unavailable": in.KalshiSettlement})),
		capability("recovery_drills", true, map[string]bool{"required recovery drills not verified": in.RecoveryDrillsPassed}),
		{Name: "live_execution", Mode: "live", Ready: false, Required: false, Blockers: LiveExecutionBlockers(in)},
	}
	ready := true
	for _, c := range capabilities {
		if c.Required && !c.Ready {
			ready = false
		}
	}
	generated := in.GeneratedAt
	if generated.IsZero() {
		generated = time.Now().UTC()
	}
	return ReadinessReport{ReleaseReady: ready, LiveTradingEnabled: in.LiveTradingEnabled, Capabilities: capabilities, GeneratedAt: generated}
}

func merge(left, right map[string]bool) map[string]bool {
	out := make(map[string]bool, len(left)+len(right))
	for k, v := range left {
		out[k] = v
	}
	for k, v := range right {
		out[k] = v
	}
	return out
}
