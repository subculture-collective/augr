package execution

import (
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
)

func TestNewPosition(t *testing.T) {
	scope, err := NewStrategyExecutionScope(
		testScopeAccountID,
		domain.AccountEnvironmentPaperScored,
		testStrategyID,
		domain.PipelineRunRef{ID: testRunID, TradeDate: testTradeDate},
	)
	if err != nil {
		t.Fatalf("NewStrategyExecutionScope() error = %v", err)
	}

	t.Run("stamps canonical scope", func(t *testing.T) {
		position, err := NewPosition(scope, "AAPL", domain.PositionSideLong, 10, 150)
		if err != nil {
			t.Fatalf("NewPosition() error = %v", err)
		}
		if position.AccountID != testScopeAccountID || position.Environment != domain.AccountEnvironmentPaperScored {
			t.Fatalf("position account/environment = %s/%q", position.AccountID, position.Environment)
		}
		if position.OriginType != string(ledger.ExecutionOriginStrategyVersion) || position.OriginID != testStrategyID.String() {
			t.Fatalf("position origin = %q/%q", position.OriginType, position.OriginID)
		}
		if position.Ticker != "AAPL" || position.Side != domain.PositionSideLong || position.Quantity != 10 || position.AvgEntry != 150 {
			t.Fatalf("position trade fields = %+v", position)
		}
	})

	tests := []struct {
		name     string
		scope    ExecutionScope
		ticker   string
		side     domain.PositionSide
		quantity float64
		avgEntry float64
	}{
		{name: "zero scope", ticker: "AAPL", side: domain.PositionSideLong, quantity: 10, avgEntry: 150},
		{name: "empty ticker", scope: scope, side: domain.PositionSideLong, quantity: 10, avgEntry: 150},
		{name: "invalid side", scope: scope, ticker: "AAPL", side: domain.PositionSide("bad"), quantity: 10, avgEntry: 150},
		{name: "zero quantity", scope: scope, ticker: "AAPL", side: domain.PositionSideLong, avgEntry: 150},
		{name: "negative average entry", scope: scope, ticker: "AAPL", side: domain.PositionSideLong, quantity: 10, avgEntry: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewPosition(tt.scope, tt.ticker, tt.side, tt.quantity, tt.avgEntry); err == nil {
				t.Fatal("NewPosition() error = nil")
			}
		})
	}
}
