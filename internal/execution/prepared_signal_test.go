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
	"github.com/PatrickFanella/get-rich-quick/internal/risk"
)

type preparedSignalWriter struct {
	*testAcceptedOrderFillWriter
	check func(*domain.Order) error
}

type signalPreparationStub struct {
	resolve func(float64) (uuid.UUID, float64, error)
	persist func(domain.Order) error
}

func (p signalPreparationStub) Resolve(_ context.Context, _ execution.ExecutionScope, _ execution.FinalSignal, _ execution.TradingPlan, requested float64) (uuid.UUID, float64, error) {
	return p.resolve(requested)
}

func (p signalPreparationStub) PersistApproved(_ context.Context, _ execution.ExecutionScope, order domain.Order) error {
	return p.persist(order)
}

func TestSignalPreparationUsesActualRiskAdmission(t *testing.T) {
	for _, name := range []string{"approved", "risk_rejected", "quantity_increased", "persistence_failed"} {
		t.Run(name, func(t *testing.T) {
			orders, positions, trades := &mockOrderRepo{}, &mockPositionRepo{}, &mockTradeRepo{}
			orderID := uuid.New()
			var resolvedQuantity float64
			riskChecks, writes, checks, submissions := 0, 0, 0, 0
			engine := &mockRiskEngine{checkPreTradeFn: func(_ context.Context, order *domain.Order, _ risk.Portfolio) (bool, string, error) {
				riskChecks++
				if writes != 0 || order.ID != orderID || order.Quantity != resolvedQuantity {
					t.Fatal("risk did not see the resolved command before preparation writes")
				}
				return name != "risk_rejected", "test risk decision", nil
			}}
			broker := &mockBroker{submitOrderFn: func(_ context.Context, order *domain.Order) (string, error) {
				submissions++
				if writes != 1 || checks != 1 || order.ID != orderID || order.Quantity != resolvedQuantity {
					t.Fatal("broker called before checked preparation")
				}
				return "admitted-external", nil
			}}
			manager := newTestOrderManager(broker, engine, orders, positions, trades, &mockAuditLogRepo{})
			manager.WithAcceptedOrderFillWriter(&preparedSignalWriter{
				testAcceptedOrderFillWriter: &testAcceptedOrderFillWriter{orders: orders, positions: positions, trades: trades},
				check: func(order *domain.Order) error {
					checks++
					if writes != 1 || order.ID != orderID || order.Quantity != resolvedQuantity {
						t.Fatal("checker did not receive persisted resolved command")
					}
					return nil
				},
			})
			preparation := signalPreparationStub{
				resolve: func(requested float64) (uuid.UUID, float64, error) {
					resolvedQuantity = requested / 2
					if name == "quantity_increased" {
						resolvedQuantity = requested * 2
					}
					return orderID, resolvedQuantity, nil
				},
				persist: func(order domain.Order) error {
					writes++
					if riskChecks != 1 || order.Quantity != resolvedQuantity || order.ID != orderID {
						t.Fatal("preparation wrote without exact risk admission")
					}
					if name == "persistence_failed" {
						return errors.New("durable route failed")
					}
					return nil
				},
			}
			err := manager.ProcessSignalWithPreparation(context.Background(), strategyScope(uuid.New(), uuid.New()), defaultSignal(), defaultPlan(), preparation)
			if name == "approved" {
				if err != nil || submissions != 1 || writes != 1 {
					t.Fatalf("approved preparation: err=%v writes=%d submissions=%d", err, writes, submissions)
				}
				return
			}
			if err == nil || submissions != 0 || len(orders.orders) != 0 {
				t.Fatalf("failed preparation produced effects: err=%v submissions=%d", err, submissions)
			}
			if name != "persistence_failed" && writes != 0 {
				t.Fatal("unapproved command persisted")
			}
		})
	}
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
