package execution

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// ErrBrokerOrderNotFound is authoritative evidence that a client order ID has
// no broker-side order.
var ErrBrokerOrderNotFound = errors.New("broker order not found")

// ErrBrokerOrderRejected marks provider-authoritative rejection. Errors not
// matching this value are ambiguous transport outcomes and remain pending.
var ErrBrokerOrderRejected = errors.New("broker order rejected")

func IsDefinitiveBrokerRejection(err error) bool {
	if errors.Is(err, ErrBrokerOrderRejected) {
		return true
	}
	var provider interface{ StatusCode() int }
	if !errors.As(err, &provider) {
		return false
	}
	status := provider.StatusCode()
	return status >= 400 && status < 500 && status != http.StatusRequestTimeout && status != http.StatusTooManyRequests
}

// Broker defines the market-agnostic execution contract for routing orders.
type Broker interface {
	SubmitOrder(ctx context.Context, order *domain.Order) (externalID string, err error)
	CancelOrder(ctx context.Context, externalID string) error
	GetOrderStatus(ctx context.Context, externalID string) (domain.OrderStatus, error)
	GetPositions(ctx context.Context) ([]domain.Position, error)
	GetAccountBalance(ctx context.Context) (Balance, error)
}

// BrokerOrderStatus carries broker-authoritative fill evidence used during
// restart recovery.
type BrokerOrderStatus struct {
	Status         domain.OrderStatus
	FilledQuantity float64
	FilledAvgPrice *float64
	FilledAt       *time.Time
}

// BrokerOrderStatusProvider exposes fill evidence without widening the core
// broker contract for venues that do not support allocator recovery.
type BrokerOrderStatusProvider interface {
	GetOrderStatusResult(context.Context, string) (BrokerOrderStatus, error)
}

// BrokerClientOrderStatusProvider resolves the provider's client-order
// idempotency key and returns the real provider order ID.
type BrokerClientOrderStatusProvider interface {
	GetOrderStatusByClientOrderIDResult(context.Context, string) (string, BrokerOrderStatus, error)
}

type BrokerOrderFillCompensator interface {
	RollbackOrderFill(context.Context, string) error
	CommitOrderFill(string)
}
