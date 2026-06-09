package main

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestCanonicalDocsContainNoStalePythonReferences(t *testing.T) {
	forbidden := []string{
		"tradingagents",
		"langgraph",
		"typer",
		"rich.console",
		"rich.table",
		"rich.panel",
		"pyproject.toml",
		"pip install",
		"from tradingagents",
		"import tradingagents",
	}
	pythonSourcePathPattern := regexp.MustCompile(`\.py\b`)

	for _, rel := range canonicalDocPaths() {
		body := strings.ToLower(readRepoFile(t, rel))
		for _, unwanted := range forbidden {
			if strings.Contains(body, unwanted) {
				t.Fatalf("%s unexpectedly contains stale Python-era reference %q", rel, unwanted)
			}
		}
		if pythonSourcePathPattern.MatchString(body) {
			t.Fatalf("%s unexpectedly contains stale Python-era reference matching %q", rel, pythonSourcePathPattern.String())
		}
	}
}

func TestCanonicalDocsDoNotLinkDeletedReferenceOrResearchDocs(t *testing.T) {
	forbiddenLinks := []string{
		"docs/reference",
		"reference/README.md",
		"reference/api.md",
		"reference/web-ui.md",
		"reference/configuration.md",
		"docs/research",
		"research/index.md",
	}

	for _, rel := range canonicalDocPaths() {
		body := readRepoFile(t, rel)
		for _, forbidden := range forbiddenLinks {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s links deleted documentation path %q", rel, forbidden)
			}
		}
	}
}

func canonicalDocPaths() []string {
	return []string{
		"README.md",
		"docs/README.md",
		"docs/getting-started.md",
		"docs/development-setup.md",
		"docs/known-issues.md",
		"docs/roadmap.md",
		"docs/AUGR_ARCHITECTURE_AUDIT.md",
	}
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()

	path := filepath.Join(repoRootPath(t), rel)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return string(content)
}

func repoRootPath(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to determine test file path")
	}

	return filepath.Join(filepath.Dir(filename), "..", "..")
}
