package generativestrategy

import (
	"encoding/json"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/experimentrun"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
	"github.com/PatrickFanella/get-rich-quick/internal/marketdata"
)

// BuildObservationMaterial reconstructs the simulation quote used by one
// generated scenario frame from its exact immutable payload. Historical bars
// and trades model a zero-spread executable mark; quote payloads preserve the
// recorded bid, ask, and depth. No provider or mutable cache is consulted.
func BuildObservationMaterial(evidence ScenarioExecutionEvidence, payload *dataset.MarketPayload, contract *instrument.VenueContract) (experimentrun.ObservationMaterial, error) {
	if payload == nil || contract == nil || evidence.PayloadID != payload.ID() || evidence.PayloadSHA256 != payload.Digest() ||
		evidence.InstrumentID != payload.InstrumentID() || evidence.VenueContractID != contract.ID || contract.InstrumentID != evidence.InstrumentID ||
		evidence.AvailableAt != payload.AvailableAt() || evidence.ExecutionPrice == "" {
		return experimentrun.ObservationMaterial{}, fmt.Errorf("generated strategy execution material identities do not reconstruct")
	}
	price, err := decimal.NewFromString(evidence.ExecutionPrice)
	if err != nil || !price.IsPositive() || price.String() != evidence.ExecutionPrice {
		return experimentrun.ObservationMaterial{}, fmt.Errorf("generated strategy execution price is invalid")
	}
	bid, ask, bidSize, askSize, err := executableQuote(payload, price)
	if err != nil {
		return experimentrun.ObservationMaterial{}, err
	}
	metadata := payload.Metadata()
	exchangeAt, availableAt := metadata.EffectiveAt, metadata.AvailableAt
	quoteMetadata, err := json.Marshal(map[string]string{
		"dataset_payload_id": payload.ID().String(), "dataset_payload_sha256": payload.Digest(),
		"partition_content_sha256": evidence.PartitionContentSHA256,
	})
	if err != nil {
		return experimentrun.ObservationMaterial{}, err
	}
	snapshot, err := marketdata.NewQuoteSnapshot(marketdata.QuoteSnapshotInput{
		InstrumentID: evidence.InstrumentID, VenueContractID: &contract.ID, Provider: metadata.Provider, Venue: contract.Venue,
		Source: metadata.Feed, ObservationNamespace: "immutable-dataset/" + evidence.PartitionContentSHA256,
		ObservationID: evidence.SourceKey, SourceRevision: metadata.Revision, ExchangeAt: &exchangeAt,
		ReceivedAt: metadata.ObservedAt, AvailableAt: &availableAt, Bid: &bid, Ask: &ask, BidSize: &bidSize, AskSize: &askSize,
		MarketStatus: "open", SessionStatus: "regular",
		Bids: []marketdata.DepthLevelInput{{Price: bid, Size: bidSize}}, Asks: []marketdata.DepthLevelInput{{Price: ask, Size: askSize}},
		Metadata: quoteMetadata, CreatedAt: availableAt,
	})
	if err != nil {
		return experimentrun.ObservationMaterial{}, fmt.Errorf("build generated strategy immutable quote: %w", err)
	}
	return experimentrun.ObservationMaterial{
		PartitionContentSHA256: evidence.PartitionContentSHA256, ObservationSourceKey: evidence.SourceKey,
		ObservationContentSHA256: evidence.PayloadSHA256, AvailableAt: availableAt,
		CanonicalContent: payload.CanonicalBytes(), Snapshot: *snapshot,
	}, nil
}

func executableQuote(payload *dataset.MarketPayload, executionPrice decimal.Decimal) (decimal.Decimal, decimal.Decimal, decimal.Decimal, decimal.Decimal, error) {
	if bar := payload.Bar(); bar != nil {
		volume, err := decimal.NewFromString(bar.Volume)
		if err != nil || volume.IsNegative() {
			return decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero, fmt.Errorf("generated strategy bar depth is invalid")
		}
		return executionPrice, executionPrice, volume, volume, nil
	}
	quote := payload.Quote()
	if snapshot := payload.Snapshot(); snapshot != nil {
		quote = &snapshot.Quote
	}
	if quote != nil {
		bid, bidErr := decimal.NewFromString(quote.BidPrice)
		ask, askErr := decimal.NewFromString(quote.AskPrice)
		bidSize, bidSizeErr := decimal.NewFromString(quote.BidSize)
		askSize, askSizeErr := decimal.NewFromString(quote.AskSize)
		if bidErr != nil || askErr != nil || bidSizeErr != nil || askSizeErr != nil || bid.IsNegative() || ask.LessThan(bid) || bidSize.IsNegative() || askSize.IsNegative() || executionPrice.LessThan(bid) || executionPrice.GreaterThan(ask) {
			return decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero, fmt.Errorf("generated strategy quote does not contain execution price")
		}
		return bid, ask, bidSize, askSize, nil
	}
	if trade := payload.Trade(); trade != nil {
		tradePrice, priceErr := decimal.NewFromString(trade.Price)
		size, sizeErr := decimal.NewFromString(trade.Size)
		if priceErr != nil || sizeErr != nil || !tradePrice.Equal(executionPrice) || !size.IsPositive() {
			return decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero, fmt.Errorf("generated strategy trade does not reconstruct execution price")
		}
		return tradePrice, tradePrice, size, size, nil
	}
	return decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero, fmt.Errorf("generated strategy payload is not executable market evidence")
}
