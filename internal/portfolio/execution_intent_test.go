package portfolio

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestExecutionIntentIsStableAndRejectsReorderedOptionLegs(t *testing.T) {
	now := time.Date(2026, 9, 3, 15, 0, 0, 0, time.UTC)
	opportunity := executionIntentOptionOpportunity(now)
	first, err := NewExecutionIntent(opportunity)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := NewExecutionIntent(opportunity)
	if err != nil || first.Digest() != retry.Digest() || !bytes.Equal(first.CanonicalBytes(), retry.CanonicalBytes()) {
		t.Fatalf("retry diverged: %v", err)
	}
	opportunity.OptionLegs[0], opportunity.OptionLegs[1] = opportunity.OptionLegs[1], opportunity.OptionLegs[0]
	if _, err = NewExecutionIntent(opportunity); err == nil {
		t.Fatal("reordered legs accepted")
	}
}

func executionIntentOptionOpportunity(quoteAt time.Time) domain.Opportunity {
	expiry := time.Date(2026, 10, 16, 20, 0, 0, 0, time.UTC)
	return domain.Opportunity{
		MarketType: domain.MarketTypeOptions, Ticker: "SPY", Side: domain.OrderSideBuy,
		EntryPrice: 4.5, ProposedNotional: 900, ExpectedLossUSD: 500, LiquidityUSD: 100000, SpreadPct: .02,
		MaxLossPerUnit: 500, RequiredCapitalUnit: 450, QuoteObservedAt: &quoteAt,
		OptionLegs: []domain.OpportunityOptionLeg{
			{Sequence: 0, ContractID: uuid.New(), ContractPayloadID: uuid.New(), ContractSHA256: strings.Repeat("a", 64), QuotePayloadID: uuid.New(), QuoteSHA256: strings.Repeat("b", 64), SnapshotPayloadID: uuid.New(), SnapshotSHA256: strings.Repeat("c", 64), OCCSymbol: "SPY261016C00500000", Underlying: "SPY", Expiry: expiry, OptionType: "call", Strike: 500, Ratio: 1, Side: domain.OrderSideBuy, PositionIntent: "buy_to_open", Bid: 10, Ask: 10.2, Multiplier: 100},
			{Sequence: 1, ContractID: uuid.New(), ContractPayloadID: uuid.New(), ContractSHA256: strings.Repeat("d", 64), QuotePayloadID: uuid.New(), QuoteSHA256: strings.Repeat("e", 64), SnapshotPayloadID: uuid.New(), SnapshotSHA256: strings.Repeat("f", 64), OCCSymbol: "SPY261016C00505000", Underlying: "SPY", Expiry: expiry, OptionType: "call", Strike: 505, Ratio: 1, Side: domain.OrderSideSell, PositionIntent: "sell_to_open", Bid: 5.5, Ask: 5.7, Multiplier: 100},
		},
	}
}
