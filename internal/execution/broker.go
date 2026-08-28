package execution

import (
	"context"
	"errors"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// ErrBrokerOrderNotFound is authoritative evidence that a client order ID has
// no broker-side order.
var ErrBrokerOrderNotFound = errors.New("broker order not found")

// Broker defines the market-agnostic execution contract for routing orders.
type Broker interface {
	SubmitOrder(ctx context.Context, order *domain.Order) (externalID string, err error)
	CancelOrder(ctx context.Context, externalID string) error
	GetOrderStatus(ctx context.Context, externalID string) (domain.OrderStatus, error)
	GetPositions(ctx context.Context) ([]domain.Position, error)
	GetAccountBalance(ctx context.Context) (Balance, error)
}
