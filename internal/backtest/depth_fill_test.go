package backtest

import (
	"errors"
	"math"
	"testing"
	"time"

	marketdata "github.com/PatrickFanella/get-rich-quick/internal/marketdata/polymarket"
)

func TestFillFromBook_BuyExactSingleLevel(t *testing.T) {
	t.Parallel()
	book := marketdata.BookSnapshot{Asks: []marketdata.Level{{Price: 0.50, Size: 100}}, ReceivedAt: time.Now()}
	got, err := FillFromBook(FillBuy, 80, book)
	if err != nil {
		t.Fatalf("FillFromBook() error = %v", err)
	}
	if math.Abs(got.AvgPrice-0.50) > 1e-9 || got.FilledSize != 80 || got.LevelsHit != 1 || got.Partial {
		t.Fatalf("got %+v", got)
	}
}

func TestFillFromBook_BuyWalksMultipleLevels(t *testing.T) {
	t.Parallel()
	book := marketdata.BookSnapshot{Asks: []marketdata.Level{{Price: 0.50, Size: 10}, {Price: 0.51, Size: 20}, {Price: 0.52, Size: 30}}}
	got, err := FillFromBook(FillBuy, 35, book)
	if err != nil {
		t.Fatalf("FillFromBook() error = %v", err)
	}
	expected := (0.50*10 + 0.51*20 + 0.52*5) / 35
	if math.Abs(got.AvgPrice-expected) > 1e-9 || got.FilledSize != 35 || got.LevelsHit != 3 || got.Partial {
		t.Fatalf("got %+v, expected avg %v", got, expected)
	}
}

func TestFillFromBook_SellWalksBids(t *testing.T) {
	t.Parallel()
	book := marketdata.BookSnapshot{Bids: []marketdata.Level{{Price: 0.52, Size: 10}, {Price: 0.51, Size: 20}, {Price: 0.50, Size: 30}}}
	got, err := FillFromBook(FillSell, 35, book)
	if err != nil {
		t.Fatalf("FillFromBook() error = %v", err)
	}
	expected := (0.52*10 + 0.51*20 + 0.50*5) / 35
	if math.Abs(got.AvgPrice-expected) > 1e-9 || got.FilledSize != 35 || got.LevelsHit != 3 || got.Partial {
		t.Fatalf("got %+v, expected avg %v", got, expected)
	}
}

func TestFillFromBook_PartialFill(t *testing.T) {
	t.Parallel()
	book := marketdata.BookSnapshot{Asks: []marketdata.Level{{Price: 0.50, Size: 5}}}
	got, err := FillFromBook(FillBuy, 10, book)
	if err != nil {
		t.Fatalf("FillFromBook() error = %v", err)
	}
	if math.Abs(got.AvgPrice-0.50) > 1e-9 || got.FilledSize != 5 || got.LevelsHit != 1 || !got.Partial {
		t.Fatalf("got %+v", got)
	}
}

func TestFillFromBook_EmptySide_ReturnsError(t *testing.T) {
	t.Parallel()
	_, err := FillFromBook(FillBuy, 10, marketdata.BookSnapshot{})
	if !errors.Is(err, ErrEmptyBook) {
		t.Fatalf("err = %v, want %v", err, ErrEmptyBook)
	}
}

func TestFillFromBook_ZeroSize_NoOp(t *testing.T) {
	t.Parallel()
	got, err := FillFromBook(FillBuy, 0, marketdata.BookSnapshot{})
	if err != nil {
		t.Fatalf("FillFromBook() error = %v", err)
	}
	if got != (DepthFillResult{}) {
		t.Fatalf("got %+v, want zero value", got)
	}
}

func TestFillFromBook_NaNLevelsSkipped(t *testing.T) {
	t.Parallel()
	book := marketdata.BookSnapshot{Asks: []marketdata.Level{{Price: math.NaN(), Size: 10}, {Price: 0.50, Size: 10}}}
	got, err := FillFromBook(FillBuy, 5, book)
	if err != nil {
		t.Fatalf("FillFromBook() error = %v", err)
	}
	if got.LevelsHit != 1 || got.FilledSize != 5 || math.Abs(got.AvgPrice-0.50) > 1e-9 {
		t.Fatalf("got %+v", got)
	}
}
