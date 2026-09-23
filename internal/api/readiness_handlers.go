package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/PatrickFanella/get-rich-quick/internal/automation"
	"github.com/PatrickFanella/get-rich-quick/internal/risk"
)

// KillSwitchStateReader is implemented by risk engines that can report the
// effective kill-switch state including restore diagnostics.
type KillSwitchStateReader interface {
	KillSwitchState() risk.KillSwitchState
}

// ReadinessDeps lists the runtime facts GET /readyz evaluates beyond the
// database probe. Nil funcs and false "required" flags skip a check.
type ReadinessDeps struct {
	// SchemaCheck verifies the applied schema still matches the runtime.
	SchemaCheck HealthCheck
	// KillSwitch reports the effective kill-switch state; an active switch
	// makes the service not ready.
	KillSwitch KillSwitchStateReader
	// SchedulerRequired is ENABLE_SCHEDULER; SchedulerPresent reports whether a
	// scheduler was constructed.
	SchedulerRequired bool
	SchedulerPresent  bool
	// Automation returns the orchestrator readiness snapshot. Nil means the
	// orchestrator was not constructed.
	Automation func() AutomationReadiness
	// LLMConfigured reports that a default LLM provider was built.
	LLMConfigured bool
}

// AutomationReadiness summarises orchestrator state for /readyz and
// /api/v1/automation/health.
type AutomationReadiness struct {
	Present         bool                          `json:"present"`
	Degraded        bool                          `json:"degraded"`
	Reason          string                        `json:"reason,omitempty"`
	Health          automation.OrchestratorHealth `json:"health"`
	TotalJobs       int                           `json:"total_jobs"`
	DisabledJobs    []string                      `json:"disabled_jobs"`
	UnavailableJobs []automation.UnavailableJob   `json:"unavailable_jobs"`
}

// AutomationReadinessOf derives readiness from the orchestrator's Health()
// flag plus its job statuses. All jobs disabled is treated as degraded even
// when Health() is clear, because operator controls can reach the same state.
func AutomationReadinessOf(o *automation.JobOrchestrator) AutomationReadiness {
	if o == nil {
		return AutomationReadiness{DisabledJobs: []string{}, UnavailableJobs: []automation.UnavailableJob{}}
	}
	statuses := o.Status()
	snapshot := AutomationReadiness{
		Present:         true,
		TotalJobs:       len(statuses),
		DisabledJobs:    make([]string, 0),
		UnavailableJobs: o.UnavailableJobs(),
	}
	if snapshot.UnavailableJobs == nil {
		snapshot.UnavailableJobs = []automation.UnavailableJob{}
	}
	for _, st := range statuses {
		if !st.Enabled {
			snapshot.DisabledJobs = append(snapshot.DisabledJobs, st.Name)
		}
	}
	sort.Strings(snapshot.DisabledJobs)
	health := o.Health()
	snapshot.Health = health
	switch {
	case health.Degraded:
		snapshot.Degraded = true
		snapshot.Reason = health.Reason
		if snapshot.Reason == "" {
			snapshot.Reason = "orchestrator reported degraded"
		}
	case len(statuses) == 0:
		snapshot.Degraded = true
		snapshot.Reason = "no automation jobs registered"
	case len(snapshot.DisabledJobs) == len(statuses):
		snapshot.Degraded = true
		snapshot.Reason = "all automation jobs are disabled (startup recovery failure or operator controls)"
	}
	return snapshot
}

type readinessResponse struct {
	Status  string            `json:"status"`
	Checks  map[string]string `json:"checks"`
	Failing []string          `json:"failing,omitempty"`
}

// handleReady serves GET /readyz. It returns 200 only when every configured
// dependency is usable for trading; a 503 body lists the failing checks so an
// operator can act without reading logs.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	checkCtx, cancel := context.WithTimeout(r.Context(), healthCheckTimeout)
	defer cancel()

	resp := readinessResponse{Status: "ready", Checks: map[string]string{}}
	fail := func(name, detail string) {
		resp.Checks[name] = detail
		resp.Failing = append(resp.Failing, name+": "+detail)
	}

	if err := s.runHealthCheck(checkCtx, s.dbHealth); err != nil {
		fail("db", err.Error())
	} else {
		resp.Checks["db"] = "ok"
	}

	if s.readiness.SchemaCheck != nil {
		if err := s.readiness.SchemaCheck.Check(checkCtx); err != nil {
			fail("schema", err.Error())
		} else {
			resp.Checks["schema"] = "ok"
		}
	}

	if s.readiness.KillSwitch != nil {
		state := s.readiness.KillSwitch.KillSwitchState()
		switch {
		case state.Active:
			detail := "active"
			if reason := strings.TrimSpace(state.Reason); reason != "" {
				detail += ": " + reason
			}
			if state.RestoreFailed {
				detail += " (restore_failed=true)"
			}
			fail("kill_switch", detail)
		case state.RestoreFailed:
			fail("kill_switch", "restore_failed=true: "+state.RestoreError)
		default:
			resp.Checks["kill_switch"] = "ok"
		}
	}

	if s.readiness.SchedulerRequired {
		if s.readiness.SchedulerPresent {
			resp.Checks["scheduler"] = "ok"
		} else {
			fail("scheduler", "ENABLE_SCHEDULER=true but no scheduler was constructed")
		}
		var auto AutomationReadiness
		if s.readiness.Automation != nil {
			auto = s.readiness.Automation()
		}
		switch {
		case !auto.Present:
			fail("automation", "orchestrator not constructed")
		case auto.Degraded:
			fail("automation", fmt.Sprintf("%s; disabled_jobs=%s", auto.Reason, strings.Join(auto.DisabledJobs, ",")))
		default:
			resp.Checks["automation"] = "ok"
		}
	}

	if s.readiness.LLMConfigured {
		resp.Checks["llm"] = "ok"
	} else {
		fail("llm", "no default LLM provider configured")
	}

	status := http.StatusOK
	if len(resp.Failing) > 0 {
		resp.Status = "not_ready"
		status = http.StatusServiceUnavailable
		s.logger.Info("readiness check failed", "failing", resp.Failing)
	}
	respondJSON(w, status, resp)
}
