package agent_test

import (
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/agent"
)

func TestPositionSnapshotPromptText(t *testing.T) {
	t.Parallel()

	opened := time.Date(2026, 9, 1, 14, 0, 0, 0, time.UTC)
	pnl := 12.5
	for name, tc := range map[string]struct {
		snapshot *agent.PositionSnapshot
		want     []string
		reject   []string
	}{
		"not applicable": {snapshot: nil},
		"unknown": {
			snapshot: &agent.PositionSnapshot{Ticker: "SPY"},
			want:     []string{"UNKNOWN", "Do not assume the account already holds SPY"},
		},
		"flat": {
			snapshot: &agent.PositionSnapshot{Ticker: "SPY", Known: true},
			want:     []string{"FLAT", "holds no SPY", "SELL cannot execute", "HOLD to stay flat"},
			reject:   []string{"LONG"},
		},
		"long": {
			snapshot: &agent.PositionSnapshot{Ticker: "SPY", Known: true, Quantity: 10, AvgEntry: 750, OpenedAt: &opened, UnrealizedPnL: &pnl, AccountQuantity: 10},
			want:     []string{"LONG 10 at an average entry of 750.00", "opened 2026-09-01", "unrealized P&L 12.50", "up to 10"},
			reject:   []string{"FLAT", "other strategies"},
		},
		"flat here, held elsewhere": {
			snapshot: &agent.PositionSnapshot{Ticker: "SPY", Known: true, AccountQuantity: 4},
			want:     []string{"FLAT", "also holds 4 SPY through other strategies", "cannot sell those"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := tc.snapshot.PromptText()
			if tc.snapshot == nil {
				if got != "" {
					t.Fatalf("PromptText() = %q, want empty", got)
				}
				return
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("PromptText() = %q, missing %q", got, want)
				}
			}
			for _, reject := range tc.reject {
				if strings.Contains(got, reject) {
					t.Errorf("PromptText() = %q, must not contain %q", got, reject)
				}
			}
		})
	}
}

func TestWithPositionContextCopiesReports(t *testing.T) {
	t.Parallel()

	reports := map[agent.AgentRole]string{agent.AgentRoleMarketAnalyst: "bars"}
	got := agent.WithPositionContext(reports, &agent.PositionSnapshot{Ticker: "SPY", Known: true})
	if _, leaked := reports[agent.ContextKeyCurrentPosition]; leaked {
		t.Fatal("WithPositionContext modified the input map")
	}
	if !strings.Contains(got[agent.ContextKeyCurrentPosition], "FLAT") || got[agent.AgentRoleMarketAnalyst] != "bars" {
		t.Fatalf("WithPositionContext() = %#v", got)
	}
	if same := agent.WithPositionContext(reports, nil); len(same) != 1 {
		t.Fatalf("nil snapshot added context: %#v", same)
	}
}
