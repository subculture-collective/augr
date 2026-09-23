package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/data/ssga"
)

const sourceFixture = `{"id":"7c1aea67-ca86-4645-97d3-23b7b732c260","execution_strategy_version_id":"0225cc3c-0348-a063-16a3-8a3b46b0bb61","ticker":"SPY","market_type":"stock","is_paper":true,"schedule_cron":"CRON_TZ=America/New_York 0 10 * * 1-5","config":{"canonical_signal_selection":{"keep":"exact"},"risk_config":{"min_confidence":0.9}}}`

func TestCandidatePreservesSourceAndCannotRun(t *testing.T) {
	candidate, err := candidateFromSource([]byte(sourceFixture))
	if err != nil {
		t.Fatal(err)
	}
	strategy := candidate["candidate_strategy"].(map[string]any)
	if strategy["status"] != "inactive" || strategy["schedule_cron"] != "" || candidate["runtime_execution_version_id"] != nil {
		t.Fatal("candidate is runnable")
	}
	cfg := strategy["config"].(map[string]json.RawMessage)
	if string(cfg["risk_config"]) != `{"min_confidence":0.9}` || string(cfg["canonical_signal_selection"]) != `{"keep":"exact"}` {
		t.Fatal("source policy changed")
	}
	if string(cfg["fundamentals_contract"]) != `"spy-ssga-etf-v1"` {
		t.Fatal("contract missing")
	}
	for _, raw := range []string{strings.Replace(sourceFixture, `"is_paper":true`, `"is_paper":false`, 1), strings.Replace(sourceFixture, "SPY", "QQQ", 1), strings.Replace(sourceFixture, "0225cc3c", "0225cc3d", 1), strings.Replace(sourceFixture, `"config":{`, `"config":{"fundamentals_contract":"existing",`, 1)} {
		if _, err := candidateFromSource([]byte(raw)); err == nil {
			t.Fatal("changed source accepted")
		}
	}
}

func TestPrepareCandidateWritesChecksumsAndNeverOverwrites(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "source.json")
	if err := os.WriteFile(src, []byte(sourceFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	fee := 0.000945
	profile := data.ETFSource{URL: ssga.ProfileURL, SHA256: strings.Repeat("a", 64), FetchedAt: now, AsOf: now.Add(-time.Hour)}
	holdings := profile
	holdings.URL = ssga.HoldingsURL
	fund := &data.ETFFundamentals{Contract: data.SPYETFContractV1, Ticker: "SPY", ISIN: "US78462F1030", Currency: "USD", NetAssetsUSD: 100, GrossExpenseRatio: &fee, Holdings: []data.ETFHolding{{Symbol: "AAA", Weight: 1}}, ProfileSource: profile, HoldingsSource: holdings}
	out := filepath.Join(parent, "candidate")
	if err := prepareCandidate(src, out, fund); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]string
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	for name, expected := range manifest {
		body, err := os.ReadFile(filepath.Join(out, name))
		if err != nil || hashBytes(body) != expected {
			t.Fatalf("checksum mismatch %s", name)
		}
	}
	receipt, err := os.ReadFile(filepath.Join(out, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(receipt), `"activation_allowed": false`) {
		t.Fatal("missing activation block")
	}
	if err := prepareCandidate(src, out, fund); err == nil {
		t.Fatal("overwrote existing evidence")
	}
	if err := prepareCandidate(src, "/var/lib/augr-etf-forbidden", fund); err == nil {
		t.Fatal("accepted production path")
	}
	link := filepath.Join(parent, "state-link")
	if err := os.Symlink("/var/lib", link); err != nil {
		t.Fatal(err)
	}
	if err := prepareCandidate(src, filepath.Join(link, "forbidden"), fund); err == nil {
		t.Fatal("symlink bypassed output guard")
	}
}
