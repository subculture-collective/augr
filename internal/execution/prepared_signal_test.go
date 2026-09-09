package execution_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type preparedSignalWriter struct {
	*testAcceptedOrderFillWriter
	check func(*domain.Order) error
}

func (w *preparedSignalWriter) RequireAcceptedOrderPrepared(_ context.Context, _ execution.ExecutionScope, order *domain.Order) error {
	return w.check(order)
}

func TestProcessPreparedSignalIdentityHandoff(t *testing.T) {
	for _, name := range []string{"accepted", "rejected", "missing_checker", "missing_identity", "conflicting_retry"} {
		t.Run(name, func(t *testing.T) {
			orderID := uuid.New()
			orders, positions, trades := &mockOrderRepo{}, &mockPositionRepo{}, &mockTradeRepo{}
			checks, submissions := 0, 0
			orders.getByRunFn = func(_ context.Context, _ uuid.UUID, _ repository.OrderFilter, _, _ int) ([]domain.Order, error) {
				if checks != 1 {
					t.Fatalf("durable lookup preceded preparation validation: checks=%d", checks)
				}
				if name == "conflicting_retry" {
					return []domain.Order{{ID: uuid.New()}}, nil
				}
				return nil, nil
			}
			broker := &mockBroker{submitOrderFn: func(_ context.Context, order *domain.Order) (string, error) {
				submissions++
				if checks != 2 || order.ID != orderID || order.ClientOrderID != orderID.String() {
					t.Fatalf("submission did not retain checked canonical identity: checks=%d order=%+v", checks, order)
				}
				return "prepared-external", nil
			}}
			manager := newTestOrderManager(broker, &mockRiskEngine{}, orders, positions, trades, &mockAuditLogRepo{})
			if name != "missing_checker" {
				manager.WithAcceptedOrderFillWriter(&preparedSignalWriter{
					testAcceptedOrderFillWriter: &testAcceptedOrderFillWriter{orders: orders, positions: positions, trades: trades},
					check: func(order *domain.Order) error {
						checks++
						if order.ID != orderID || order.ClientOrderID != orderID.String() {
							t.Fatal("checker received legacy identity")
						}
						if name == "rejected" {
							return errors.New("route does not match")
						}
						return nil
					},
				})
			}
			if name == "missing_identity" {
				orderID = uuid.Nil
			}
			err := manager.ProcessPreparedSignal(context.Background(), strategyScope(uuid.New(), uuid.New()), defaultSignal(), defaultPlan(), orderID)
			if name == "accepted" {
				if err != nil || submissions != 1 || len(orders.orders) != 1 || orders.orders[0].ID != orderID {
					t.Fatalf("prepared dispatch: err=%v submissions=%d orders=%v", err, submissions, orders.orders)
				}
				return
			}
			if err == nil || submissions != 0 || len(orders.orders) != 0 {
				t.Fatalf("invalid preparation produced effects: err=%v submissions=%d orders=%d", err, submissions, len(orders.orders))
			}
			if name == "rejected" && !strings.Contains(err.Error(), "route does not match") {
				t.Fatalf("lost preparation error: %v", err)
			}
			if name == "conflicting_retry" && !strings.Contains(err.Error(), "durable effect key conflicts") {
				t.Fatalf("conflicting retry was not rejected: %v", err)
			}
		})
	}
}

func TestProcessPreparedSignalSeparatesReferenceAndRoutePrices(t *testing.T) {
	for _, entryType := range []string{"market", "limit", "stop", "stop_limit"} {
		t.Run(entryType, func(t *testing.T) {
			orders, positions, trades := &mockOrderRepo{}, &mockPositionRepo{}, &mockTradeRepo{}
			plan := defaultPlan()
			plan.EntryType = entryType
			plan.EntryPrice, plan.StopLoss = 150, 140
			checks, submits := 0, 0
			assertPrices := func(order *domain.Order) {
				t.Helper()
				if order.ReferencePrice == nil || *order.ReferencePrice != plan.EntryPrice || order.StopPrice != nil {
					t.Fatalf("entry reference or protective-stop mapping is wrong: %+v", order)
				}
				if entryType == "market" && order.LimitPrice != nil {
					t.Fatal("market command retained a limit price")
				}
				if entryType == "limit" && (order.LimitPrice == nil || *order.LimitPrice != plan.EntryPrice) {
					t.Fatal("limit command lost its limit price")
				}
			}
			broker := &mockBroker{submitOrderFn: func(_ context.Context, order *domain.Order) (string, error) {
				submits++
				assertPrices(order)
				return "prepared-price-external", nil
			}}
			manager := newTestOrderManager(broker, &mockRiskEngine{}, orders, positions, trades, &mockAuditLogRepo{})
			manager.WithAcceptedOrderFillWriter(&preparedSignalWriter{
				testAcceptedOrderFillWriter: &testAcceptedOrderFillWriter{orders: orders, positions: positions, trades: trades},
				check:                       func(order *domain.Order) error { checks++; assertPrices(order); return nil },
			})
			err := manager.ProcessPreparedSignal(context.Background(), strategyScope(uuid.New(), uuid.New()), defaultSignal(), plan, uuid.New())
			if entryType == "stop" || entryType == "stop_limit" {
				if err == nil || !strings.Contains(err.Error(), "explicit entry trigger") || checks != 0 || submits != 0 || len(orders.orders) != 0 {
					t.Fatalf("ambiguous stop command produced effects: err=%v checks=%d submits=%d", err, checks, submits)
				}
				return
			}
			if err != nil || checks != 2 || submits != 1 {
				t.Fatalf("prepared price dispatch: err=%v checks=%d submits=%d", err, checks, submits)
			}
		})
	}
}
