package generativestrategy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/evaluation"
)

type evaluationBatchSource struct {
	items []EligibleEvaluation
	err   error
}

func (source evaluationBatchSource) ListEligibleGeneratedEvaluations(context.Context, uuid.UUID, uuid.UUID, int) ([]EligibleEvaluation, error) {
	return source.items, source.err
}

type evaluationBatchRunner struct {
	reports map[uuid.UUID]*evaluation.Report
	err     error
	calls   int
}

func (runner *evaluationBatchRunner) Evaluate(_ context.Context, request evaluation.Request) (*evaluation.Report, error) {
	runner.calls++
	if runner.err != nil {
		return nil, runner.err
	}
	return runner.reports[request.ResultID], nil
}

func TestEvaluationBatchRejectsCrossScopeAndDuplicateWorkBeforePersistence(t *testing.T) {
	accountID, scopeID, resultID := uuid.New(), uuid.New(), uuid.New()
	policy, _ := evaluation.ReviewedPolicyV1()
	valid := EligibleEvaluation{ScopeID: scopeID, AccountID: accountID, ResultID: resultID, ReportInput: evaluation.ReportInput{Policy: policy}}
	for name, items := range map[string][]EligibleEvaluation{
		"cross scope": {{ScopeID: uuid.New(), AccountID: accountID, ResultID: resultID, ReportInput: evaluation.ReportInput{Policy: policy}}},
		"duplicate":   {valid, valid},
	} {
		t.Run(name, func(t *testing.T) {
			runner := &evaluationBatchRunner{}
			service, _ := NewEvaluationBatchService(evaluationBatchSource{items: items}, runner)
			summary, err := service.RunEligible(context.Background(), accountID, scopeID, 10)
			if err == nil || summary.Failed != 1 || (name != "duplicate" && runner.calls != 0) {
				t.Fatalf("summary=%+v calls=%d err=%v", summary, runner.calls, err)
			}
		})
	}
}

func TestEvaluationBatchFailsClosedOnSourceAndEvaluationErrors(t *testing.T) {
	accountID, scopeID := uuid.New(), uuid.New()
	runner := &evaluationBatchRunner{}
	service, _ := NewEvaluationBatchService(evaluationBatchSource{err: errors.New("evidence unavailable")}, runner)
	if _, err := service.RunEligible(context.Background(), accountID, scopeID, 10); err == nil || !strings.Contains(err.Error(), "evidence unavailable") {
		t.Fatalf("source error=%v", err)
	}
	policy, _ := evaluation.ReviewedPolicyV1()
	service, _ = NewEvaluationBatchService(evaluationBatchSource{items: []EligibleEvaluation{{
		ScopeID: scopeID, AccountID: accountID, ResultID: uuid.New(), ReportInput: evaluation.ReportInput{Policy: policy},
	}}}, &evaluationBatchRunner{err: errors.New("persist failed")})
	if summary, err := service.RunEligible(context.Background(), accountID, scopeID, 10); err == nil || summary.Failed != 1 || summary.Completed != 0 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
}
