package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data/polygon"
)

func TestSaveEvidence(t *testing.T) {
	body := []byte("{\"source\":true}\n")
	evidence := &polygon.TickerReferenceEvidence{RawResponse: body, ResponseSHA256: fmt.Sprintf("%x", sha256.Sum256(body)), ObservedAt: time.Now().UTC()}
	directory := filepath.Join(t.TempDir(), "receipt")
	if err := saveEvidence(directory, evidence); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		mode os.FileMode
	}{{"", 0o700}, {"response.json", 0o600}, {"manifest.json", 0o600}} {
		info, err := os.Stat(filepath.Join(directory, test.name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != test.mode {
			t.Fatalf("unsafe permissions for %s", test.name)
		}
	}
	got, err := os.ReadFile(filepath.Join(directory, "response.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatal("response changed")
	}
	manifestBytes, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Schema   string                          `json:"schema"`
		Status   string                          `json:"status"`
		Evidence polygon.TickerReferenceEvidence `json:"evidence"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != "augr-provider-reference-v1" || manifest.Status != "unqualified_reference" || manifest.Evidence.ResponseSHA256 != evidence.ResponseSHA256 || !manifest.Evidence.ObservedAt.Equal(evidence.ObservedAt) || manifest.Evidence.RawResponse != nil {
		t.Fatal("manifest does not reconstruct reference evidence")
	}
	if err := saveEvidence(directory, evidence); err == nil {
		t.Fatal("overwrote existing evidence")
	}
	evidence.ResponseSHA256 = "invalid"
	bad := filepath.Join(t.TempDir(), "bad")
	if err := saveEvidence(bad, evidence); err == nil {
		t.Fatal("accepted tampered bytes")
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatal("created invalid evidence directory")
	}
}

func TestRunInputContract(t *testing.T) {
	t.Setenv("POLYGON_API_KEY", "")
	for _, args := range [][]string{{"--help"}, {"--version"}} {
		var output bytes.Buffer
		if err := run(context.Background(), args, &output); err != nil || output.Len() == 0 {
			t.Fatalf("metadata command failed: %v", err)
		}
	}
	for _, args := range [][]string{nil, {"--unknown"}, {"--ticker", "SPY"}} {
		var output bytes.Buffer
		if err := run(context.Background(), args, &output); err == nil || output.Len() != 0 {
			t.Fatal("invalid arguments accepted or polluted stdout")
		}
	}
	args := []string{"--ticker", "SPY", "--date", "2026-09-08", "--output", filepath.Join(t.TempDir(), "new")}
	if err := run(context.Background(), args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "POLYGON_API_KEY") {
		t.Fatal("missing credential not rejected")
	}
	t.Setenv("POLYGON_API_KEY", "not-a-real-key")
	args[len(args)-1] = t.TempDir()
	if err := run(context.Background(), args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "already exist") {
		t.Fatal("existing directory not rejected before network")
	}
}
