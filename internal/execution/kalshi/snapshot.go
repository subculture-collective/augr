package kalshi

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
)

const (
	KalshiMarkSource    = "kalshi"
	KalshiMarkNamespace = "quotes/kalshi/orderbook"
)

type KalshiMarkInput struct {
	AccountID       uuid.UUID
	InstrumentID    uuid.UUID
	VenueContractID uuid.UUID
	Side            domain.PositionSide
	Ticker          string
	Quote           Snapshot
	ObservedAt      time.Time
	MaxAge          time.Duration
}

// NewMarkObservation returns a conservative liquidation mark. Canonical
// Kalshi tickers use MARKET:OUTCOME so YES and NO holdings select their own bid.
func NewMarkObservation(input KalshiMarkInput) (*ledger.MarkObservation, error) {
	if input.AccountID == uuid.Nil || input.InstrumentID == uuid.Nil || input.VenueContractID == uuid.Nil {
		return nil, errors.New("kalshi mark: canonical account, instrument, and venue contract are required")
	}
	if !input.Side.IsValid() {
		return nil, errors.New("kalshi mark: valid position side is required")
	}
	if input.Side != domain.PositionSideLong {
		return nil, errors.New("kalshi mark: short canonical lots are unavailable")
	}
	marketTicker, outcome, ok := strings.Cut(strings.TrimSpace(input.Ticker), ":")
	if !ok || marketTicker == "" || (strings.ToUpper(outcome) != "YES" && strings.ToUpper(outcome) != "NO") {
		return nil, errors.New("kalshi mark: ticker must contain a canonical YES or NO outcome")
	}
	if !strings.EqualFold(strings.TrimSpace(input.Quote.Ticker), marketTicker) {
		return nil, errors.New("kalshi mark: snapshot ticker does not match canonical contract")
	}
	status := strings.ToLower(strings.TrimSpace(input.Quote.Status))
	if status != "active" && status != "open" {
		return nil, fmt.Errorf("kalshi mark: market status %q is unavailable", input.Quote.Status)
	}
	evaluatedAt := input.ObservedAt.UTC().Truncate(time.Microsecond)
	snapshotObservedAt := input.Quote.FetchedAt.UTC().Truncate(time.Microsecond)
	if evaluatedAt.IsZero() || snapshotObservedAt.IsZero() || input.MaxAge <= 0 || snapshotObservedAt.After(evaluatedAt) || evaluatedAt.Sub(snapshotObservedAt) > input.MaxAge {
		return nil, errors.New("kalshi mark: snapshot is stale or has invalid observation time")
	}

	yes := strings.EqualFold(outcome, "YES")
	bid, ask := input.Quote.BestBidNo, input.Quote.BestAskNo
	if yes {
		bid, ask = input.Quote.BestBidYes, input.Quote.BestAskYes
	}
	if math.IsNaN(bid) || math.IsInf(bid, 0) || math.IsNaN(ask) || math.IsInf(ask, 0) ||
		bid <= 0 || ask <= 0 || bid > 1 || ask > 1 || bid > ask {
		return nil, fmt.Errorf("kalshi mark: valid nonzero noncrossed %s book is required", strings.ToUpper(outcome))
	}
	metadata, err := json.Marshal(map[string]any{
		"account_id": input.AccountID.String(), "venue_contract_id": input.VenueContractID.String(), "ticker": marketTicker,
		"outcome": strings.ToLower(outcome), "convention": "executable_bid",
		"currency": "USD", "bid": bid, "ask": ask, "status": status, "fetched_at": snapshotObservedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("kalshi mark: encode metadata: %w", err)
	}
	return ledger.NewMarkObservation(ledger.MarkObservationInput{
		InstrumentID: input.InstrumentID, Price: decimal.NewFromFloat(bid), PriceCurrency: "USD",
		Source: KalshiMarkSource, SourceNamespace: KalshiAccountMarkNamespace(input.AccountID),
		SourceObservationID: input.AccountID.String() + "/" + input.VenueContractID.String() + "/" + marketTicker + "/" + strings.ToLower(outcome) + "/" + snapshotObservedAt.Format("20060102T150405.000000Z"),
		SourceRevision:      "", EffectiveAt: snapshotObservedAt, ObservedAt: snapshotObservedAt, Metadata: metadata,
	})
}

func KalshiAccountMarkNamespace(accountID uuid.UUID) string {
	return KalshiMarkNamespace + "/accounts/" + accountID.String()
}

// Snapshot captures the native Kalshi execution state needed to decide whether
// a market can be activated safely.
type Snapshot struct {
	Ticker       string
	Title        string
	Status       string
	BestBidYes   float64
	BestAskYes   float64
	BestBidNo    float64
	BestAskNo    float64
	Volume       float64
	OpenInterest float64
	CloseTime    time.Time
	FetchedAt    time.Time
}

// ValidateExecutableSide checks whether the snapshot has an executable book for
// the selected side.
func (s Snapshot) ValidateExecutableSide(side string, minLiquidity float64, now time.Time) error {
	if err := s.validateExecutableBase(minLiquidity, now); err != nil {
		return err
	}

	normalizedSide := strings.ToUpper(strings.TrimSpace(side))
	switch normalizedSide {
	case "YES", "NO":
	default:
		return errors.New("kalshi snapshot: side must be YES or NO")
	}

	bid, ask, ok := s.quoteForSide(normalizedSide)
	if !ok || bid <= 0 || ask <= 0 || ask < bid || ask > 1 {
		return fmt.Errorf("kalshi snapshot: valid %s orderbook quote is required", normalizedSide)
	}

	return nil
}

// EntryPriceForSide returns the executable ask price for a YES or NO buy.
func (s Snapshot) EntryPriceForSide(side string) (float64, bool) {
	_, ask, ok := s.quoteForSide(side)
	return ask, ok
}

// ExitPriceForSide returns the executable bid for selling a held contract.
func (s Snapshot) ExitPriceForSide(side string) (float64, bool) {
	bid, _, ok := s.quoteForSide(side)
	return bid, ok
}

// SpreadForSide returns the bid/ask spread for the selected side when known.
func (s Snapshot) SpreadForSide(side string) (float64, bool) {
	bid, ask, ok := s.quoteForSide(side)
	if !ok {
		return 0, false
	}
	return max0(ask - bid), true
}

func (s Snapshot) validateExecutableBase(minLiquidity float64, now time.Time) error {
	switch {
	case strings.TrimSpace(s.Ticker) == "":
		return errors.New("kalshi snapshot: ticker is required")
	case strings.TrimSpace(s.Title) == "":
		return errors.New("kalshi snapshot: title is required")
	case strings.TrimSpace(s.Status) == "":
		return errors.New("kalshi snapshot: status is required")
	}

	status := strings.ToLower(strings.TrimSpace(s.Status))
	switch status {
	case "closed", "settled", "expired":
		return fmt.Errorf("kalshi snapshot: market status %q is not executable", s.Status)
	}

	if s.CloseTime.IsZero() || !s.CloseTime.After(now) {
		return errors.New("kalshi snapshot: valid future close time is required")
	}

	if s.liquidityScore() < minLiquidity {
		return fmt.Errorf("kalshi snapshot: liquidity %.2f below minimum %.2f", s.liquidityScore(), minLiquidity)
	}

	return nil
}

func (s Snapshot) liquidityScore() float64 {
	if s.OpenInterest > s.Volume {
		return s.OpenInterest
	}
	return s.Volume
}

func (s Snapshot) quoteForSide(side string) (bid, ask float64, ok bool) {
	switch strings.ToUpper(strings.TrimSpace(side)) {
	case "YES":
		return s.BestBidYes, s.BestAskYes, s.BestBidYes > 0 && s.BestAskYes > 0 && s.BestAskYes >= s.BestBidYes
	case "NO":
		return s.BestBidNo, s.BestAskNo, s.BestBidNo > 0 && s.BestAskNo > 0 && s.BestAskNo >= s.BestBidNo
	default:
		return 0, 0, false
	}
}

func max0(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}
