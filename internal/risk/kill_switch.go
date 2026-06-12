package risk

import (
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

const (
	// defaultKillSwitchFilePath is the default file path checked for the kill switch file flag.
	defaultKillSwitchFilePath = "/tmp/tradingagent_kill"
	// killSwitchEnvVar is the environment variable checked for the kill switch.
	killSwitchEnvVar = "TRADING_AGENT_KILL"
)

type killSwitchPolicy struct {
	filePath   string
	fileExists func(string) bool
	getEnv     func(string) string
}

func (p killSwitchPolicy) mechanisms(apiKS KillSwitchStatus) []KillSwitchMechanism {
	mechanisms := make([]KillSwitchMechanism, 0, 3)
	if apiKS.Active {
		mechanisms = append(mechanisms, KillSwitchMechanismAPI)
	}
	if p.fileExists != nil && p.fileExists(p.filePath) {
		mechanisms = append(mechanisms, KillSwitchMechanismFile)
	}
	if p.getEnv != nil && p.getEnv(killSwitchEnvVar) == "true" {
		mechanisms = append(mechanisms, KillSwitchMechanismEnvVar)
	}
	return mechanisms
}

func (p killSwitchPolicy) activeAndMechanisms(apiKS KillSwitchStatus) (bool, []KillSwitchMechanism) {
	mechanisms := p.mechanisms(apiKS)
	return len(mechanisms) > 0, mechanisms
}

func (p killSwitchPolicy) status(apiKS KillSwitchStatus) KillSwitchStatus {
	active, mechanisms := p.activeAndMechanisms(apiKS)
	status := KillSwitchStatus{
		Active:      active,
		Reason:      apiKS.Reason,
		Mechanisms:  mechanisms,
		ActivatedAt: apiKS.ActivatedAt,
	}
	if status.Active && status.Reason == "" {
		status.Reason = "external mechanism"
	}
	return status
}

func (e *RiskEngineImpl) killSwitchPolicySnapshot() killSwitchPolicy {
	e.ksMu.RLock()
	defer e.ksMu.RUnlock()

	return killSwitchPolicy{
		filePath:   e.killSwitchFilePath,
		fileExists: e.fileExistsFunc,
		getEnv:     e.getEnvFunc,
	}
}

func (e *RiskEngineImpl) isKillSwitchActiveUnlocked(apiKS KillSwitchStatus) (bool, []KillSwitchMechanism) {
	return e.killSwitchPolicySnapshot().activeAndMechanisms(apiKS)
}

func (e *RiskEngineImpl) buildKillSwitchStatus(apiKS KillSwitchStatus) KillSwitchStatus {
	return e.killSwitchPolicySnapshot().status(apiKS)
}

func (e *RiskEngineImpl) activeMarketKillSwitchSnapshot(marketType domain.MarketType) (KillSwitchStatus, bool) {
	e.state.mu.RLock()
	defer e.state.mu.RUnlock()
	mks, ok := e.state.mks[marketType]
	return mks, ok && mks.Active
}

func (e *RiskEngineImpl) snapshotMarketKillSwitches() map[domain.MarketType]KillSwitchStatus {
	e.state.mu.RLock()
	defer e.state.mu.RUnlock()
	snapshot := make(map[domain.MarketType]KillSwitchStatus, len(e.state.mks))
	for mt, mks := range e.state.mks {
		snapshot[mt] = mks
	}
	return snapshot
}

func (e *RiskEngineImpl) activateKillSwitchLocked(reason string) KillSwitchStatus {
	now := e.currentTime()
	e.state.ks = KillSwitchStatus{
		Active:      true,
		Reason:      reason,
		Mechanisms:  []KillSwitchMechanism{KillSwitchMechanismAPI},
		ActivatedAt: &now,
	}
	return e.state.ks
}

func (e *RiskEngineImpl) deactivateKillSwitchLocked() {
	e.state.ks = KillSwitchStatus{Active: false}
}

func (e *RiskEngineImpl) activateMarketKillSwitchLocked(marketType domain.MarketType, reason string) KillSwitchStatus {
	now := e.currentTime()
	mks := KillSwitchStatus{
		Active:      true,
		Reason:      reason,
		Mechanisms:  []KillSwitchMechanism{KillSwitchMechanismAPI},
		ActivatedAt: &now,
	}
	e.state.mks[marketType] = mks
	return mks
}

func (e *RiskEngineImpl) deactivateMarketKillSwitchLocked(marketType domain.MarketType) {
	delete(e.state.mks, marketType)
}
