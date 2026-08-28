package execution

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/economicid"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
)

var (
	testScopeAccountID = uuid.MustParse("10000000-0000-4000-8000-000000000001")
	testStrategyID     = uuid.MustParse("20000000-0000-4000-8000-000000000002")
	testRunID          = uuid.MustParse("30000000-0000-4000-8000-000000000003")
	testSubscriptionID = uuid.MustParse("40000000-0000-4000-8000-000000000004")
	testCopyRunID      = uuid.MustParse("50000000-0000-4000-8000-000000000005")
	testTradeDate      = time.Date(2026, time.August, 27, 0, 0, 0, 0, time.UTC)
)

func TestNewStrategyExecutionScope(t *testing.T) {
	run := domain.PipelineRunRef{ID: testRunID, TradeDate: testTradeDate}
	scope, err := NewStrategyExecutionScope(testScopeAccountID, domain.AccountEnvironmentPaperScored, testStrategyID, run)
	if err != nil {
		t.Fatalf("NewStrategyExecutionScope() error = %v", err)
	}

	assertScopeIdentity(t, scope, ledger.ExecutionOriginStrategyVersion, testStrategyID.String())
	if scope.executionAccount.AccountID() != testScopeAccountID || scope.executionAccount.Environment() != domain.AccountEnvironmentPaperScored {
		t.Fatalf("retained binding = %s/%q", scope.executionAccount.AccountID(), scope.executionAccount.Environment())
	}
	gotRun, ok := scope.PipelineRun()
	if !ok || gotRun.ID != run.ID || !gotRun.TradeDate.Equal(run.TradeDate) {
		t.Fatalf("PipelineRun() = %+v, %t; want %+v, true", gotRun, ok, run)
	}
	if scope.CopyOriginRunID() != uuid.Nil {
		t.Fatalf("CopyOriginRunID() = %s, want nil UUID", scope.CopyOriginRunID())
	}
}

func TestNewStrategyExecutionScopeRejectsIncompleteIdentity(t *testing.T) {
	tests := []struct {
		name        string
		accountID   uuid.UUID
		environment domain.AccountEnvironment
		strategyID  uuid.UUID
		run         domain.PipelineRunRef
	}{
		{name: "account", environment: domain.AccountEnvironmentPaperScored, strategyID: testStrategyID, run: domain.PipelineRunRef{ID: testRunID, TradeDate: testTradeDate}},
		{name: "environment", accountID: testScopeAccountID, strategyID: testStrategyID, run: domain.PipelineRunRef{ID: testRunID, TradeDate: testTradeDate}},
		{name: "strategy version", accountID: testScopeAccountID, environment: domain.AccountEnvironmentPaperScored, run: domain.PipelineRunRef{ID: testRunID, TradeDate: testTradeDate}},
		{name: "run ID", accountID: testScopeAccountID, environment: domain.AccountEnvironmentPaperScored, strategyID: testStrategyID, run: domain.PipelineRunRef{TradeDate: testTradeDate}},
		{name: "trade date", accountID: testScopeAccountID, environment: domain.AccountEnvironmentPaperScored, strategyID: testStrategyID, run: domain.PipelineRunRef{ID: testRunID}},
		{name: "non-midnight trade date", accountID: testScopeAccountID, environment: domain.AccountEnvironmentPaperScored, strategyID: testStrategyID, run: domain.PipelineRunRef{ID: testRunID, TradeDate: testTradeDate.Add(time.Hour)}},
		{name: "non-UTC trade date", accountID: testScopeAccountID, environment: domain.AccountEnvironmentPaperScored, strategyID: testStrategyID, run: domain.PipelineRunRef{ID: testRunID, TradeDate: time.Date(2026, time.August, 27, 0, 0, 0, 0, time.FixedZone("EDT", -4*60*60))}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewStrategyExecutionScope(tt.accountID, tt.environment, tt.strategyID, tt.run); err == nil {
				t.Fatal("NewStrategyExecutionScope() error = nil")
			}
		})
	}
}

func TestNewCopyExecutionScope(t *testing.T) {
	scope, err := NewCopyExecutionScope(testScopeAccountID, domain.AccountEnvironmentPaperStress, testSubscriptionID, testCopyRunID)
	if err != nil {
		t.Fatalf("NewCopyExecutionScope() error = %v", err)
	}

	assertScopeIdentity(t, scope, ledger.ExecutionOriginCopySubscription, testSubscriptionID.String())
	if run, ok := scope.PipelineRun(); ok || run != (domain.PipelineRunRef{}) {
		t.Fatalf("PipelineRun() = %+v, %t; want zero, false", run, ok)
	}
	if scope.CopyOriginRunID() != testCopyRunID {
		t.Fatalf("CopyOriginRunID() = %s, want %s", scope.CopyOriginRunID(), testCopyRunID)
	}
}

func TestNewCopyExecutionScopeRejectsIncompleteIdentity(t *testing.T) {
	if _, err := NewCopyExecutionScope(testScopeAccountID, domain.AccountEnvironmentPaperScored, uuid.Nil, testCopyRunID); err == nil {
		t.Fatal("NewCopyExecutionScope() accepted nil subscription")
	}
	if _, err := NewCopyExecutionScope(testScopeAccountID, domain.AccountEnvironmentPaperScored, testSubscriptionID, uuid.Nil); err == nil {
		t.Fatal("NewCopyExecutionScope() accepted nil copy origin run")
	}
}

func TestNewNonRunExecutionScope(t *testing.T) {
	for _, originType := range []ledger.ExecutionOriginType{
		ledger.ExecutionOriginPortfolioRebalance,
		ledger.ExecutionOriginRiskReduction,
		ledger.ExecutionOriginOperator,
		ledger.ExecutionOriginSettlement,
		ledger.ExecutionOriginReconciliation,
	} {
		t.Run(string(originType), func(t *testing.T) {
			first, err := NewNonRunExecutionScope(testScopeAccountID, domain.AccountEnvironmentPaperScored, originType, " authoritative-origin ")
			if err != nil {
				t.Fatalf("NewNonRunExecutionScope() error = %v", err)
			}
			second, err := NewNonRunExecutionScope(testScopeAccountID, domain.AccountEnvironmentPaperScored, originType, " authoritative-origin ")
			if err != nil {
				t.Fatalf("retry NewNonRunExecutionScope() error = %v", err)
			}
			firstType, firstID := first.Origin()
			secondType, secondID := second.Origin()
			if firstType != originType || secondType != originType || firstID != "authoritative-origin" || secondID != firstID {
				t.Fatalf("Origin() = %q/%q then %q/%q, want preserved normalized %q origin", firstType, firstID, secondType, secondID, originType)
			}
			if _, ok := first.PipelineRun(); ok || first.CopyOriginRunID() != uuid.Nil {
				t.Fatal("non-run scope contains run identity")
			}
		})
	}
}

func TestNewScheduledNonRunExecutionScopeDerivesDeterministicOriginID(t *testing.T) {
	for _, originType := range []ledger.ExecutionOriginType{ledger.ExecutionOriginOperator, ledger.ExecutionOriginSettlement, ledger.ExecutionOriginReconciliation} {
		t.Run(string(originType), func(t *testing.T) {
			scope, err := NewScheduledNonRunExecutionScope(testScopeAccountID, domain.AccountEnvironmentPaperScored, originType, " occurrence-1 ")
			if err != nil {
				t.Fatalf("NewScheduledNonRunExecutionScope() error = %v", err)
			}
			_, got := scope.Origin()
			want := economicid.DeterministicUUID(
				nonRunOriginIDDomain+":"+string(originType),
				testScopeAccountID.String(),
				string(domain.AccountEnvironmentPaperScored),
				"occurrence-1",
			).String()
			if got != want {
				t.Fatalf("Origin() ID = %q, want %q", got, want)
			}
		})
	}
	if _, err := NewScheduledNonRunExecutionScope(testScopeAccountID, domain.AccountEnvironmentPaperScored, ledger.ExecutionOriginRiskReduction, "occurrence-1"); err == nil {
		t.Fatal("NewScheduledNonRunExecutionScope() accepted non-scheduled origin type")
	}
}

func TestScopeOriginIDsAcceptsStableStringForNonUUIDOrigin(t *testing.T) {
	scope, err := NewNonRunExecutionScope(testScopeAccountID, domain.AccountEnvironmentPaperScored, ledger.ExecutionOriginPortfolioRebalance, "portfolio/daily/core")
	if err != nil {
		t.Fatal(err)
	}
	originID, runID, hasRun, err := scopeOriginIDs(scope)
	if err != nil || originID != uuid.Nil || runID != uuid.Nil || hasRun {
		t.Fatalf("scope IDs = %s %s %t, %v", originID, runID, hasRun, err)
	}
}

func TestNewNonRunExecutionScopeRejectsRunOrigins(t *testing.T) {
	for _, originType := range []ledger.ExecutionOriginType{ledger.ExecutionOriginStrategyVersion, ledger.ExecutionOriginCopySubscription, "unknown"} {
		if _, err := NewNonRunExecutionScope(testScopeAccountID, domain.AccountEnvironmentPaperScored, originType, "job"); err == nil {
			t.Fatalf("NewNonRunExecutionScope() accepted %q", originType)
		}
	}
	if _, err := NewNonRunExecutionScope(testScopeAccountID, domain.AccountEnvironmentPaperScored, ledger.ExecutionOriginOperator, " "); err == nil {
		t.Fatal("NewNonRunExecutionScope() accepted empty origin ID")
	}
}

func assertScopeIdentity(t *testing.T, scope ExecutionScope, wantType ledger.ExecutionOriginType, wantID string) {
	t.Helper()
	if scope.AccountID() != testScopeAccountID || scope.Environment() == "" {
		t.Fatalf("scope account/environment = %s/%q", scope.AccountID(), scope.Environment())
	}
	gotType, gotID := scope.Origin()
	if gotType != wantType || gotID != wantID {
		t.Fatalf("Origin() = %q/%q, want %q/%q", gotType, gotID, wantType, wantID)
	}
}
