package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/google/uuid"
)

func TestRunnerCompletionPreparationChronology(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			persister := newRunnerSpyPersister()
			now := time.Date(2026, 9, 9, 14, 0, 0, 0, time.UTC)
			runner := NewRunner(Definition{}, Dependencies{Persister: persister, Clock: func() time.Time { return now }})
			calls := 0
			prepared := PreparedRun{Strategy: domain.Strategy{ID: uuid.New(), Ticker: "TEST"}, Runtime: RuntimeConfig{SkipPhases: map[Phase]bool{PhaseAnalysis: true, PhaseResearchDebate: true, PhaseTrading: true, PhaseRiskDebate: true, PhaseExecutionGate: true}}}
			prepared.PrepareCompletion = func(_ context.Context, run domain.PipelineRun, _ domain.PipelineSignal) error {
				calls++
				if run.CompletedAt != nil || run.Status != domain.PipelineStatusRunning {
					t.Fatal("preparation occurred after terminal boundary")
				}
				now = now.Add(time.Second)
				if fail {
					return errors.New("capture rejected")
				}
				return nil
			}
			result, err := runner.Run(t.Context(), prepared)
			if calls != 1 || result == nil || result.Run.CompletedAt == nil || !result.Run.CompletedAt.Equal(now) || !result.TerminalApplied {
				t.Fatalf("completion chronology mismatch result=%+v err=%v calls=%d", result, err, calls)
			}
			want := domain.PipelineStatusCompleted
			if fail {
				want = domain.PipelineStatusFailed
			}
			if result.Run.Status != want || (err != nil) != fail {
				t.Fatalf("status=%v err=%v", result.Run.Status, err)
			}
		})
	}
}
