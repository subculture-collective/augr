package portfolio

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/google/uuid"
)

func TestPaperPreparationFailsClosed(t *testing.T) {
	sentinel := errors.New("canonical reference unavailable")
	for _, test := range []struct {
		name    string
		failure error
		message string
	}{
		{"provider error", sentinel, "canonical reference unavailable"},
		{"missing provider result", nil, "preparation is missing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			processor := NewPaperOrderManagerProcessor(PaperOrderManagerProcessorDeps{
				PrepareSignal: func(_ context.Context, request PaperOrderRequest) (execution.SignalOrderPreparation, error) {
					calls++
					if request.NotionalUSD != 100 {
						t.Fatal("request was changed")
					}
					return nil, test.failure
				},
			})
			result, err := processor.ProcessPaperOrder(context.Background(), PaperOrderRequest{NotionalUSD: 100, Plan: execution.TradingPlan{MarketType: domain.MarketTypeStock}, Signal: execution.FinalSignal{Signal: domain.PipelineSignalBuy}})
			if err == nil || !strings.Contains(err.Error(), test.message) || calls != 1 || result.Skipped {
				t.Fatalf("result=%+v err=%v calls=%d", result, err, calls)
			}
			if test.failure != nil && !errors.Is(err, sentinel) {
				t.Fatal("lost preparation error")
			}
		})
	}
}

type preparationTestOrderRepo struct{ repository.OrderRepository }

func (preparationTestOrderRepo) WithExecutionAccountLock(_ context.Context, _ uuid.UUID, fn func() error) error {
	return fn()
}

type uncalledPreparation struct{ t *testing.T }

func (p uncalledPreparation) Resolve(context.Context, execution.ExecutionScope, execution.FinalSignal, execution.TradingPlan, float64) (uuid.UUID, float64, error) {
	p.t.Fatal("preparation ran without canonical checker")
	return uuid.Nil, 0, errors.New("unexpected resolve")
}

func (p uncalledPreparation) PersistApproved(context.Context, execution.ExecutionScope, domain.Order) error {
	p.t.Fatal("preparation persisted without canonical checker")
	return errors.New("unexpected persistence")
}

func TestPaperPreparationRequiresCanonicalChecker(t *testing.T) {
	processor := NewPaperOrderManagerProcessor(PaperOrderManagerProcessorDeps{
		OrderRepo: preparationTestOrderRepo{},
		PrepareSignal: func(context.Context, PaperOrderRequest) (execution.SignalOrderPreparation, error) {
			return uncalledPreparation{t}, nil
		},
	})
	_, err := processor.ProcessPaperOrder(context.Background(), PaperOrderRequest{NotionalUSD: 100, Plan: execution.TradingPlan{MarketType: domain.MarketTypeStock}, Signal: execution.FinalSignal{Signal: domain.PipelineSignalBuy}})
	if err == nil || !strings.Contains(err.Error(), "canonical preparation checker") {
		t.Fatalf("missing checker not rejected: %v", err)
	}
}

func TestPaperPreparationRequiresClaimRepository(t *testing.T) {
	processor := NewPaperOrderManagerProcessor(PaperOrderManagerProcessorDeps{
		PrepareSignal: func(context.Context, PaperOrderRequest) (execution.SignalOrderPreparation, error) {
			t.Fatal("factory called without claim repository")
			return nil, nil
		},
	})
	_, err := processor.ProcessPaperOrder(context.Background(), PaperOrderRequest{NotionalUSD: 100, OpportunityID: uuid.New(), ClaimID: uuid.New()})
	if err == nil || !strings.Contains(err.Error(), "claim repository") {
		t.Fatalf("missing claim ownership boundary: %v", err)
	}
}

func TestStockPreparationPreservesOtherRoutes(t *testing.T) {
	for _, market := range []domain.MarketType{domain.MarketTypeKalshi, domain.MarketTypePolymarket, domain.MarketTypeStock} {
		t.Run(string(market), func(t *testing.T) {
			processor := NewPaperOrderManagerProcessor(PaperOrderManagerProcessorDeps{PrepareSignal: func(context.Context, PaperOrderRequest) (execution.SignalOrderPreparation, error) {
				t.Fatal("stock preparation intercepted native or HOLD route")
				return nil, nil
			}})
			signal := domain.PipelineSignalBuy
			if market == domain.MarketTypeStock {
				signal = domain.PipelineSignalHold
			}
			_, _ = processor.ProcessPaperOrder(t.Context(), PaperOrderRequest{NotionalUSD: 100, Plan: execution.TradingPlan{MarketType: market}, Signal: execution.FinalSignal{Signal: signal}})
		})
	}
}
