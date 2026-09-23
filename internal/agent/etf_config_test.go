package agent

import (
	"encoding/json"
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
)

func TestETFContractRequiresExplicitRolesAndSurvivesSnapshot(t *testing.T) {
	cfg := StrategyConfig{FundamentalsContract: data.SPYETFContractV1, RequiredAnalystRoles: []AgentRole{AgentRoleMarketAnalyst, AgentRoleFundamentalsAnalyst, AgentRoleNewsAnalyst}}
	if err := ValidateStrategyConfig(cfg); err != nil {
		t.Fatal(err)
	}
	resolved := ResolveConfig(&cfg, GlobalSettings{})
	if err := ValidateResolvedConfig(resolved); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(resolved)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot ResolvedConfig
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.FundamentalsContract != data.SPYETFContractV1 {
		t.Fatal("contract lost from snapshot")
	}
	for _, roles := range [][]AgentRole{nil, {}, {AgentRoleMarketAnalyst, AgentRoleNewsAnalyst}} {
		cfg.RequiredAnalystRoles = roles
		if err := ValidateStrategyConfig(cfg); err == nil {
			t.Fatal("weakened required roles accepted")
		}
	}
	cfg.FundamentalsContract = "unknown"
	if err := ValidateStrategyConfig(cfg); err == nil {
		t.Fatal("unknown contract accepted")
	}
	if ResolveConfig(nil, GlobalSettings{}).FundamentalsContract != "" {
		t.Fatal("default changed to ETF")
	}
}
