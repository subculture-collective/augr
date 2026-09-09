package postgres

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	alpacadata "github.com/PatrickFanella/get-rich-quick/internal/data/alpaca"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/shopspring/decimal"
)

func TestAlpacaStockQuotePersistenceAndReplay(t *testing.T) {
	f := newExecutionLifecycleFixture(t)
	raw := []byte(`{"symbol":"FIXTURE","quote":{"bp":10.24,"ap":10.26,"bs":80,"as":90,"bx":"V","ax":"V","t":"2026-08-15T17:59:59.123456789Z"}}`)
	digest := sha256.Sum256(raw)
	exchange, err := time.Parse(time.RFC3339Nano, "2026-08-15T17:59:59.123456789Z")
	if err != nil {
		t.Fatal(err)
	}
	evidence := &alpacadata.StockQuoteEvidence{Ticker: "FIXTURE", Feed: "iex", RequestPath: "/v2/stocks/FIXTURE/quotes/latest?currency=USD&feed=iex", ResponseSHA256: hex.EncodeToString(digest[:]), BidExchange: "V", AskExchange: "V", Bid: decimal.RequireFromString("10.24"), Ask: decimal.RequireFromString("10.26"), BidSize: decimal.NewFromInt(80), AskSize: decimal.NewFromInt(90), ExchangeAt: exchange, ObservedAt: f.baseTime, RawResponse: raw}
	retainedAt := f.baseTime.Add(time.Millisecond)
	snapshot, err := evidence.QuoteSnapshot(*f.instrument, *f.contract, retainedAt)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewQuoteSnapshotRepo(f.pool)
	persisted, err := repo.RecordQuoteSnapshot(f.ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := repo.GetQuoteSnapshotByID(f.ctx, persisted.ID)
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Receipt    alpacadata.StockQuoteEvidence `json:"receipt"`
		DepthScope string                        `json:"depth_scope"`
	}
	if err := json.Unmarshal(reloaded.Metadata, &metadata); err != nil {
		t.Fatal(err)
	}
	if string(metadata.Receipt.RawResponse) != string(raw) || !metadata.Receipt.ExchangeAt.Equal(exchange) || metadata.DepthScope != "top_of_book" || !reloaded.BidSize.Equal(decimal.NewFromInt(80)) || reloaded.MarketStatus != "" || reloaded.SessionStatus != "" {
		t.Fatal("persisted normalization changed source evidence or inferred status")
	}
	replay, err := evidence.QuoteSnapshot(*f.instrument, *f.contract, retainedAt)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := repo.RecordQuoteSnapshot(f.ctx, replay)
	if err != nil || replayed.ID != persisted.ID {
		t.Fatalf("exact receipt replay did not reuse identity: %v", err)
	}
	changed, err := evidence.QuoteSnapshot(*f.instrument, *f.contract, retainedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RecordQuoteSnapshot(f.ctx, changed); !errors.Is(err, repository.ErrIdempotencyConflict) {
		t.Fatalf("changed retention accepted on replay: %v", err)
	}
	var count int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM quote_snapshots WHERE instrument_id=$1 AND observation_namespace=$2`, f.instrument.ID, snapshot.ObservationNamespace).Scan(&count); err != nil || count != 1 {
		t.Fatalf("quote count=%d err=%v", count, err)
	}
}
