package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/execution"
)

type countingPortfolioBalanceSource struct{ calls int }

func (source *countingPortfolioBalanceSource) GetAccountBalance(context.Context) (execution.Balance, error) {
	source.calls++
	return execution.Balance{Equity: 100000, BuyingPower: 100000}, nil
}

func TestInternalAccountCapitalSnapshotDoesNotCallBroker(t *testing.T) {
	ctx := context.Background()
	pools := newProjectionIntegrationPool(t, ctx)
	for _, test := range []struct {
		name string
		id   uuid.UUID
	}{
		{"internal", uuid.MustParse("00000000-0000-4000-8000-000000000064")},
		{"missing", uuid.New()},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := &countingPortfolioBalanceSource{}
			_, err := NewPortfolioRiskRepo(pools.owner, test.id, source).CaptureAccountSnapshot(ctx)
			if err == nil {
				t.Fatal("unsupported account snapshot was accepted")
			}
			if source.calls != 0 {
				t.Fatalf("broker called %d times before resolving supported account identity", source.calls)
			}
		})
	}
}
