package main

import (
	"strings"
	"testing"
)

func TestReadmeDocumentsCLICommands(t *testing.T) {
	body := readRepoFile(t, "README.md")

	for _, snippet := range []string{
		"## CLI",
		"tradingagent serve",
		"tradingagent run",
		"tradingagent strategies",
		"tradingagent portfolio",
		"tradingagent risk",
		"tradingagent memories",
		"tradingagent dashboard",
		"Run `tradingagent --help` for full usage details.",
	} {
		if !strings.Contains(body, snippet) {
			t.Errorf("README.md missing required CLI snippet %q", snippet)
		}
	}
}

func TestDevelopmentSetupPointsToCurrentCLISource(t *testing.T) {
	body := readRepoFile(t, "docs/development-setup.md")

	for _, snippet := range []string{
		"go run ./cmd/tradingagent serve",
		"./bin/tradingagent serve",
		"./bin/tradingagent run AAPL",
		"run `./bin/tradingagent --help` after building",
	} {
		if !strings.Contains(body, snippet) {
			t.Errorf("development setup missing required CLI snippet %q", snippet)
		}
	}
}
