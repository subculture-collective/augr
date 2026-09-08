package automation

import (
	"context"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/promotion"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	pgrepo "github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
	"github.com/google/uuid"
)

type promotionEvaluatorStub struct{}

func (promotionEvaluatorStub) EvaluateEligiblePromotions(context.Context, uuid.UUID, uuid.UUID, promotion.Readiness) (pgrepo.PromotionEvaluationBatch, error) {
	return pgrepo.PromotionEvaluationBatch{}, nil
}

type promotionAccountStub struct{}

func (promotionAccountStub) GetByID(context.Context, uuid.UUID) (*domain.Account, error) {
	return nil, repository.ErrNotFound
}

type promotionProjectionStub struct{}

func (promotionProjectionStub) GetLatestPortfolioProjection(context.Context, uuid.UUID, time.Time) (*repository.ProjectionSnapshot, error) {
	return nil, repository.ErrNotFound
}

type promotionEvidenceStub struct{}

func (promotionEvidenceStub) GetCutoverEvidenceInventoryForScope(context.Context, uuid.UUID, uuid.UUID) (*repository.CutoverEvidenceInventory, error) {
	return nil, repository.ErrNotFound
}

func TestPromotionEvaluationRegistrationDoesNotRequireAutoActivation(t *testing.T) {
	accountID, scopeID := uuid.New(), uuid.New()
	orchestrator := NewJobOrchestrator(OrchestratorDeps{
		CanonicalAccountID: accountID, DiscoveryScopeID: scopeID,
		PromotionEvaluation: promotionEvaluatorStub{}, PromotionAccountSource: promotionAccountStub{},
		PromotionProjectionSource: promotionProjectionStub{}, PromotionEvidenceSource: promotionEvidenceStub{},
		AutomaticShadowPromotion: false,
	})
	orchestrator.registerPromotionEvaluationJob()
	if _, ok := orchestrator.jobs["promotion_evaluation"]; !ok {
		t.Fatal("promotion evaluation was not registered for an exact configured scope")
	}
	if _, ok := orchestrator.jobs["promotion_activation"]; ok {
		t.Fatal("promotion activation registered without explicit enable")
	}
}

func TestPromotionEvaluationIsInertWithoutConfiguredScope(t *testing.T) {
	orchestrator := NewJobOrchestrator(OrchestratorDeps{})
	orchestrator.registerPromotionEvaluationJob()
	if _, ok := orchestrator.jobs["promotion_evaluation"]; ok {
		t.Fatal("promotion evaluation registered without exact scope")
	}
	for _, unavailable := range orchestrator.unavailableJobs {
		if unavailable.Name == "promotion_evaluation" {
			t.Fatal("inert deployment reported promotion evaluation unavailable")
		}
	}
}
