package main

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/config"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
)

type accountContextBroker struct {
	execution.Broker
	balance                  execution.Balance
	positions                []domain.Position
	balanceErr, positionsErr error
}

func (b accountContextBroker) GetAccountBalance(context.Context) (execution.Balance, error) {
	return b.balance, b.balanceErr
}
func (b accountContextBroker) GetPositions(context.Context) ([]domain.Position, error) {
	return b.positions, b.positionsErr
}

func TestLoadAgentAccountContext(t *testing.T) {
	for _, tc := range []struct {
		name    string
		broker  accountContextBroker
		wantErr string
	}{
		{name: "flat", broker: accountContextBroker{balance: execution.Balance{Currency: "USD", Cash: 1234, Equity: 1234}}},
		{name: "held", broker: accountContextBroker{positions: []domain.Position{{Ticker: "SPY", Side: domain.PositionSideLong, Quantity: 4, AvgEntry: 500}, {Ticker: "QQQ", Side: domain.PositionSideShort, Quantity: 2}}}},
		{name: "balance failure", broker: accountContextBroker{balanceErr: errors.New("timeout")}, wantErr: "balance"},
		{name: "position failure", broker: accountContextBroker{positionsErr: errors.New("timeout")}, wantErr: "positions"},
		{name: "invalid balance", broker: accountContextBroker{balance: execution.Balance{Cash: math.NaN()}}, wantErr: "invalid broker values"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &realStrategyRunner{brokerCache: map[string]execution.Broker{"alpaca:paper": tc.broker}}
			r.cfg.Brokers.Alpaca = config.BrokerConfig{APIKey: "test", APISecret: "test", PaperMode: true}
			start := time.Now()
			got, err := r.loadAgentAccountContext(context.Background(), domain.Strategy{Ticker: "SPY", MarketType: domain.MarketTypeStock, IsPaper: true})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || got != nil {
					t.Fatalf("got %+v, %v", got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Source != "alpaca" || !got.IsPaper || got.ObservedAt.Before(start) {
				t.Fatalf("wrong provenance: %+v", got)
			}
			if got.Cash != tc.broker.balance.Cash || got.Equity != tc.broker.balance.Equity || len(got.Positions) != len(tc.broker.positions) {
				t.Fatalf("wrong account: %+v", got)
			}
			if tc.name == "held" && (got.Positions[0].Ticker != "QQQ" || got.Positions[0].Side != domain.PositionSideShort || got.Positions[1].Quantity != 4) {
				t.Fatalf("holdings lost: %+v", got.Positions)
			}
		})
	}
}
