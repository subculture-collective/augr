package main

import (
	"strings"
	"testing"
)

func TestExactContractProviderConfiguration(t *testing.T) {
	for _, mode := range []string{"valid paper", "valid live reference", "missing origin", "missing key", "missing secret", "wrong provider", "data host", "external host", "embedded credential", "query", "http production"} {
		t.Run(mode, func(t *testing.T) {
			env := map[string]string{"ALPACA_REFERENCE_URL": "https://paper-api.alpaca.markets", "ALPACA_API_KEY": "synthetic-private-key", "ALPACA_API_SECRET": "synthetic-private-secret"}
			provider := "alpaca"
			switch mode {
			case "valid live reference":
				env["ALPACA_REFERENCE_URL"] = "https://api.alpaca.markets"
			case "missing origin":
				delete(env, "ALPACA_REFERENCE_URL")
			case "missing key":
				delete(env, "ALPACA_API_KEY")
			case "missing secret":
				delete(env, "ALPACA_API_SECRET")
			case "wrong provider":
				provider = "polygon"
			case "data host":
				env["ALPACA_REFERENCE_URL"] = "https://data.alpaca.markets"
			case "external host":
				env["ALPACA_REFERENCE_URL"] = "https://example.invalid"
			case "embedded credential":
				env["ALPACA_REFERENCE_URL"] = "https://synthetic-private-key@api.alpaca.markets"
			case "query":
				env["ALPACA_REFERENCE_URL"] += "?secret=synthetic-private-secret"
			case "http production":
				env["ALPACA_REFERENCE_URL"] = "http://api.alpaca.markets"
			}
			client, err := exactContractProviderFromEnv(provider, func(name string) string { return env[name] })
			valid := strings.HasPrefix(mode, "valid")
			if valid != (err == nil) || valid != (client != nil) {
				t.Fatalf("mode=%s configured=%t err=%v", mode, client != nil, err)
			}
			if err != nil && (strings.Contains(err.Error(), "synthetic-private") || strings.Contains(err.Error(), env["ALPACA_REFERENCE_URL"]) && env["ALPACA_REFERENCE_URL"] != "") {
				t.Fatal("configuration error exposed credential or origin value")
			}
		})
	}
}
