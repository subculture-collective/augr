package postgres

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestReportArtifact_RoundTrip(t *testing.T) {
	// Unit-level: verify struct serialisation and field assignment.
	now := time.Now().UTC().Truncate(time.Second)
	report := json.RawMessage(`{"decision":"GO"}`)
	completed := now

	a := &ReportArtifact{
		ID:               uuid.New(),
		StrategyID:       uuid.New(),
		ReportType:       "paper_validation",
		TimeBucket:       now.Truncate(24 * time.Hour),
		Status:           "completed",
		ReportJSON:       report,
		Provider:         "openrouter",
		Model:            "meta-llama/llama-3.3-70b-instruct:free",
		PromptTokens:     100,
		CompletionTokens: 50,
		LatencyMs:        1200,
		CreatedAt:        now,
		CompletedAt:      &completed,
	}

	// Verify JSON round-trip.
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got ReportArtifact
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.StrategyID != a.StrategyID {
		t.Errorf("strategy_id = %s, want %s", got.StrategyID, a.StrategyID)
	}
	if got.ReportType != "paper_validation" {
		t.Errorf("report_type = %q, want paper_validation", got.ReportType)
	}
	if got.Status != "completed" {
		t.Errorf("status = %q, want completed", got.Status)
	}
	if got.PromptTokens != 100 {
		t.Errorf("prompt_tokens = %d, want 100", got.PromptTokens)
	}
	if got.CompletionTokens != 50 {
		t.Errorf("completion_tokens = %d, want 50", got.CompletionTokens)
	}
}

func TestReportArtifactFilter_Defaults(t *testing.T) {
	f := ReportArtifactFilter{}
	if f.StrategyID != nil {
		t.Error("expected nil StrategyID")
	}
	if f.ReportType != "" {
		t.Error("expected empty ReportType")
	}
	if f.Status != "" {
		t.Error("expected empty Status")
	}
}

func TestBuildReportArtifactListQueryRequiresExactCanonicalKey(t *testing.T) {
	accountID, scopeID, strategyID := uuid.New(), uuid.New(), uuid.New()
	query, args := buildReportArtifactListQuery(ReportArtifactFilter{
		AccountID: &accountID, ScopeID: &scopeID, StrategyID: &strategyID, ReportType: "paper_validation",
	}, 25, 5)
	for _, fragment := range []string{
		"LEFT JOIN paper_evaluation_scopes s ON s.id=a.scope_id",
		"a.strategy_id = $1", "a.scope_id = $2", "s.account_id = $3", "a.report_type = $4",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("query missing %q: %s", fragment, query)
		}
	}
	if len(args) != 6 || args[0] != strategyID || args[1] != scopeID || args[2] != accountID || args[3] != "paper_validation" {
		t.Fatalf("args=%v", args)
	}
}

func TestReportArtifactListRejectsIncompleteCanonicalKey(t *testing.T) {
	t.Parallel()
	if _, err := (&ReportArtifactRepo{}).List(t.Context(), uuid.Nil, uuid.New(), uuid.New(), "paper_validation", "", 10, 0); err == nil {
		t.Fatal("expected incomplete canonical report key to fail before database access")
	}
}
