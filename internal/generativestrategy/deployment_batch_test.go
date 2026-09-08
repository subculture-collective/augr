package generativestrategy

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/portfolio"
	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

type deploymentSourceFixture struct{ items []EligibleDeploymentProposal }

func (source deploymentSourceFixture) ListEligibleGeneratedDeployments(context.Context, uuid.UUID, uuid.UUID, int) ([]EligibleDeploymentProposal, error) {
	return source.items, nil
}

type deploymentStoreFixture struct {
	registered, bound, proposed bool
}

func (store *deploymentStoreFixture) RegisterPortfolioRiskPolicy(context.Context, *portfolio.PortfolioRiskPolicy) error {
	store.registered = true
	return nil
}

func (store *deploymentStoreFixture) BindPortfolioRiskPolicy(context.Context, uuid.UUID, *portfolio.PortfolioRiskPolicy, time.Time) (uuid.UUID, error) {
	store.bound = true
	return uuid.New(), nil
}

func (store *deploymentStoreFixture) ProposeGeneratedDeployment(_ context.Context, value *strategycatalog.Deployment) (*strategycatalog.Deployment, error) {
	store.proposed = true
	return value, nil
}

func TestDeploymentBatchPersistsReviewedRiskBindingBeforeProposal(t *testing.T) {
	t.Parallel()
	accountID, scopeID, bindingID := uuid.New(), uuid.New(), uuid.New()
	policy, err := portfolio.ReviewedPortfolioRiskPolicyV1()
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := strategycatalog.NewDeployment(strategycatalog.DeploymentInput{
		VersionID: uuid.New(), AccountID: accountID, CapitalBindingID: bindingID, Budget: "2000",
		ScheduleCron: "5 10 * * 1-5", Timezone: "America/New_York", RiskPolicyVersion: policy.Reference(), Mode: strategycatalog.ExperimentPaperScored,
	})
	if err != nil {
		t.Fatal(err)
	}
	effectiveAt := time.Date(2026, 9, 3, 20, 0, 0, 0, time.UTC)
	store := &deploymentStoreFixture{}
	service, err := NewDeploymentBatchService(deploymentSourceFixture{items: []EligibleDeploymentProposal{{
		Key: "candidate", ScopeID: scopeID, RiskPolicy: policy, RiskEffectiveAt: effectiveAt, Deployment: deployment,
	}}}, store)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := service.RunEligible(context.Background(), accountID, scopeID, 1)
	if err != nil || summary.Eligible != 1 || summary.Completed != 1 || summary.Failed != 0 || !store.registered || !store.bound || !store.proposed {
		t.Fatalf("summary=%+v store=%+v error=%v", summary, store, err)
	}
}

func TestDeploymentBatchNoopsWithoutCompletedAssessment(t *testing.T) {
	t.Parallel()
	service, err := NewDeploymentBatchService(deploymentSourceFixture{}, &deploymentStoreFixture{})
	if err != nil {
		t.Fatal(err)
	}
	if summary, runErr := service.RunEligible(context.Background(), uuid.New(), uuid.New(), 1); runErr != nil || summary != (BatchSummary{}) {
		t.Fatalf("summary=%+v error=%v", summary, runErr)
	}
}
