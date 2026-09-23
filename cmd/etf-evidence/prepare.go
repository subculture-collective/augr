package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/agent"
	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func candidateFromSource(raw []byte) (map[string]any, error) {
	var source domain.Strategy
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, err
	}
	if source.ID.String() != "7c1aea67-ca86-4645-97d3-23b7b732c260" || source.ExecutionStrategyVersionID == nil || source.ExecutionStrategyVersionID.String() != "0225cc3c-0348-a063-16a3-8a3b46b0bb61" || source.Ticker != "SPY" || source.MarketType != domain.MarketTypeStock || !source.IsPaper {
		return nil, fmt.Errorf("source strategy/version/environment mismatch")
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(source.Config, &config); err != nil || config == nil {
		return nil, fmt.Errorf("source config invalid")
	}
	if _, exists := config["fundamentals_contract"]; exists {
		return nil, fmt.Errorf("source already has an input contract")
	}
	config["fundamentals_contract"], _ = json.Marshal(data.SPYETFContractV1)
	var required []agent.AgentRole
	if rawRoles, exists := config["required_analyst_roles"]; exists {
		if err := json.Unmarshal(rawRoles, &required); err != nil {
			return nil, fmt.Errorf("source required roles invalid")
		}
	}
	for _, role := range []agent.AgentRole{agent.AgentRoleMarketAnalyst, agent.AgentRoleFundamentalsAnalyst, agent.AgentRoleNewsAnalyst} {
		found := false
		for _, existing := range required {
			if existing == role {
				found = true
			}
		}
		if !found {
			required = append(required, role)
		}
	}
	config["required_analyst_roles"], _ = json.Marshal(required)
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	var validated agent.StrategyConfig
	if err = json.Unmarshal(encoded, &validated); err != nil {
		return nil, err
	}
	if err = agent.ValidateStrategyConfig(validated); err != nil {
		return nil, err
	}
	return map[string]any{
		"schema": "spy-etf-candidate-v1", "source_strategy_id": source.ID,
		"source_execution_version_id":  source.ExecutionStrategyVersionID,
		"source_snapshot_sha256":       hashBytes(raw),
		"candidate_strategy":           map[string]any{"name": "spy-ssga-etf-v1", "ticker": "SPY", "market_type": "stock", "is_paper": true, "status": "inactive", "schedule_cron": "", "config": config},
		"proposed_schedule_for_review": source.ScheduleCron,
		"runtime_execution_version_id": nil,
	}, nil
}

func hashBytes(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

func prepareCandidate(sourcePath, outputPath string, fund *data.ETFFundamentals) error {
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	proposal, err := candidateFromSource(raw)
	if err != nil {
		return err
	}
	if err = data.ValidateSPYETFFundamentals(fund, time.Now().UTC()); err != nil {
		return err
	}
	// Only an existing parent is accepted, so symlink resolution cannot bypass this boundary.
	absolute, err := filepath.Abs(outputPath)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return err
	}
	for _, forbidden := range []string{"/var/lib", "/etc"} {
		if parent == forbidden || strings.HasPrefix(parent, forbidden+"/") {
			return fmt.Errorf("production state is not a preparation destination")
		}
	}
	destination := filepath.Join(parent, filepath.Base(absolute))
	if err = os.Mkdir(destination, 0o700); err != nil {
		return err
	}
	// Retain partial output on an I/O failure; never reuse or overwrite a candidate directory.
	write := func(name string, value any) (string, error) {
		bytes, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return "", err
		}
		bytes = append(bytes, '\n')
		file, err := os.OpenFile(filepath.Join(destination, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return "", err
		}
		_, writeErr := file.Write(bytes)
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil {
			return "", writeErr
		}
		if syncErr != nil {
			return "", syncErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		return hashBytes(bytes), nil
	}
	manifest := map[string]string{}
	candidateHash, err := write("candidate.json", proposal)
	if err != nil {
		return err
	}
	manifest["candidate.json"] = candidateHash
	evidenceHash, err := write("etf-evidence.json", fund)
	if err != nil {
		return err
	}
	manifest["etf-evidence.json"] = evidenceHash
	report := map[string]any{
		"schema": "spy-etf-preparation-receipt-v1", "prepared_at": time.Now().UTC(), "candidate_sha256": candidateHash, "evidence_sha256": evidenceHash, "activation_allowed": false,
		"blockers": []string{"pin_resolved_global_configuration_and_prompts", "pass_exact_commit_CI", "deploy_reviewed_app_candidate", "create_new_immutable_runtime_version", "review_point_in_time_backtest", "pass_new_prospective_cycle"},
	}
	receiptHash, err := write("receipt.json", report)
	if err != nil {
		return err
	}
	manifest["receipt.json"] = receiptHash
	_, err = write("manifest.json", manifest)
	return err
}
