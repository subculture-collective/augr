package promotion

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/robustness"
	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

type promotionServiceStore struct {
	deployment *strategycatalog.Deployment
	assessment *robustness.Assessment
	recorded   *Decision
}

func (store *promotionServiceStore) GetDeployment(context.Context, uuid.UUID) (*strategycatalog.Deployment, error) {
	return store.deployment, nil
}

func (store *promotionServiceStore) GetAssessment(context.Context, uuid.UUID) (*robustness.Assessment, error) {
	return store.assessment, nil
}

func (store *promotionServiceStore) GetDecision(context.Context, uuid.UUID) (*Decision, error) {
	return store.recorded, nil
}

func (store *promotionServiceStore) RegisterPolicy(_ context.Context, value *Policy) (*Policy, error) {
	return value, nil
}

func (store *promotionServiceStore) RecordDecision(_ context.Context, value *Decision) (*Decision, error) {
	store.recorded = value
	return value, nil
}

func TestServiceReloadsExactParentsAndExposesNoVerdictInput(t *testing.T) {
	deployment, assessment := promotionParents(t, true)
	policy, _ := NewPolicy(PolicyInput{Version: "promotion-service-v1", RequiredGates: []string{"overall_robustness"}, FailureAction: ActionHold})
	store := &promotionServiceStore{deployment: deployment, assessment: assessment}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	input := trustedReadinessInput()
	input.ConfiguredAccountID, input.Account.ID, input.ScopeAccountID = deployment.AccountID(), deployment.AccountID(), deployment.AccountID()
	input.ScopeID = assessment.ScopeID()
	input.ReconciliationAccountID = deployment.AccountID()
	readiness := EvaluateReadiness(input)
	decision, err := service.Evaluate(context.Background(), Request{DeploymentID: deployment.ID(), AssessmentID: assessment.ID(), Policy: policy, Readiness: readiness})
	if err != nil || decision.Outcome() != OutcomeApproved || store.recorded == nil || store.recorded.ID() != decision.ID() {
		t.Fatalf("decision=%v recorded=%v err=%v", decision, store.recorded, err)
	}
}

func TestServiceRejectsReadinessForDifferentDeploymentAccountOrAssessmentScope(t *testing.T) {
	deployment, assessment := promotionParents(t, true)
	policy, _ := NewPolicy(PolicyInput{Version: "promotion-service-v1", RequiredGates: []string{"overall_robustness"}, FailureAction: ActionHold})
	service, _ := NewService(&promotionServiceStore{deployment: deployment, assessment: assessment})
	for name, mutate := range map[string]func(*ReadinessInput){
		"deployment account": func(input *ReadinessInput) { input.ScopeID = assessment.ScopeID() },
		"assessment scope": func(input *ReadinessInput) {
			input.ConfiguredAccountID, input.Account.ID, input.ScopeAccountID, input.ReconciliationAccountID = deployment.AccountID(), deployment.AccountID(), deployment.AccountID(), deployment.AccountID()
			input.ScopeID = uuid.New()
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := trustedReadinessInput()
			mutate(&input)
			decision, err := service.Evaluate(context.Background(), Request{DeploymentID: deployment.ID(), AssessmentID: assessment.ID(), Policy: policy, Readiness: EvaluateReadiness(input)})
			if err == nil || decision != nil {
				t.Fatalf("decision=%v err=%v", decision, err)
			}
		})
	}
}

func TestServiceFailsClosedWithoutEffectiveEvidenceReadiness(t *testing.T) {
	deployment, assessment := promotionParents(t, true)
	policy, _ := NewPolicy(PolicyInput{Version: "promotion-service-v1", RequiredGates: []string{"overall_robustness"}, FailureAction: ActionHold})
	store := &promotionServiceStore{deployment: deployment, assessment: assessment}
	service, _ := NewService(store)
	decision, err := service.Evaluate(context.Background(), Request{DeploymentID: deployment.ID(), AssessmentID: assessment.ID(), Policy: policy})
	if err == nil || decision != nil {
		t.Fatalf("decision=%v err=%v", decision, err)
	}
}
