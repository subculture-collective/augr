package postgres

import (
	"errors"
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestFillOrderStatusMarksIncompleteFillsPartial(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		current  domain.OrderStatus
		observed float64
		quantity float64
		want     domain.OrderStatus
	}{
		{name: "partial progress", current: domain.OrderStatusSubmitted, observed: 3, quantity: 10, want: domain.OrderStatusPartial},
		{name: "broker said filled but quantity short", current: domain.OrderStatusFilled, observed: 3, quantity: 10, want: domain.OrderStatusPartial},
		{name: "complete", current: domain.OrderStatusPartial, observed: 10, quantity: 10, want: domain.OrderStatusFilled},
		{name: "complete within numeric tolerance", current: domain.OrderStatusSubmitted, observed: 9.999999999, quantity: 10, want: domain.OrderStatusFilled},
		{name: "cancelled with partial fill stays cancelled", current: domain.OrderStatusCancelled, observed: 3, quantity: 10, want: domain.OrderStatusCancelled},
		{name: "rejected stays rejected", current: domain.OrderStatusRejected, observed: 3, quantity: 10, want: domain.OrderStatusRejected},
	} {
		if got := fillOrderStatus(tc.current, tc.observed, tc.quantity); got != tc.want {
			t.Errorf("%s: fillOrderStatus(%s, %v, %v) = %s, want %s", tc.name, tc.current, tc.observed, tc.quantity, got, tc.want)
		}
	}
}

func TestRequireOrderStatusTransitionEnforcesDomainStateMachine(t *testing.T) {
	t.Parallel()
	allowed := [][2]domain.OrderStatus{
		{domain.OrderStatusPending, domain.OrderStatusPending},
		{domain.OrderStatusPending, domain.OrderStatusSubmitted},
		{domain.OrderStatusPending, domain.OrderStatusRejected},
		{domain.OrderStatusSubmitted, domain.OrderStatusPartial},
		{domain.OrderStatusSubmitted, domain.OrderStatusFilled},
		{domain.OrderStatusSubmitted, domain.OrderStatusCancelled},
		{domain.OrderStatusPartial, domain.OrderStatusPartial},
		{domain.OrderStatusPartial, domain.OrderStatusFilled},
		{domain.OrderStatusFilled, domain.OrderStatusFilled},
	}
	for _, pair := range allowed {
		if err := requireOrderStatusTransition(pair[0], pair[1]); err != nil {
			t.Errorf("%s -> %s rejected: %v", pair[0], pair[1], err)
		}
	}
	rejected := [][2]domain.OrderStatus{
		{domain.OrderStatusFilled, domain.OrderStatusSubmitted},
		{domain.OrderStatusCancelled, domain.OrderStatusFilled},
		{domain.OrderStatusSubmitted, domain.OrderStatusPending},
		{domain.OrderStatusPending, domain.OrderStatusFilled},
		{domain.OrderStatusSubmitted, domain.OrderStatus("bogus")},
	}
	for _, pair := range rejected {
		err := requireOrderStatusTransition(pair[0], pair[1])
		if err == nil || !errors.Is(err, ErrInvalidOrderTransition) {
			t.Errorf("%s -> %s accepted (err=%v)", pair[0], pair[1], err)
		}
	}
}
