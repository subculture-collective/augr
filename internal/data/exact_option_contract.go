package data

import (
	"context"
	"encoding/json"
	"time"
)

// ExactOptionContract retains provider-reported reference fields, not OCC defaults.
// Size is the provider's contract size; deliverable/settlement support must be
// validated separately before using it as a canonical execution multiplier.
type ExactOptionContract struct {
	ProviderID, Symbol, UnderlyingSymbol, UnderlyingAssetID string
	OptionType, Style, ExpirationDate, StrikePrice, Size    string
	Status                                                  string
	Tradable                                                bool
	Raw                                                     json.RawMessage
}

// ExactOptionContractResult is one current reference observation, not historical
// coverage or an attestation of usage rights or supported execution deliverables.
type ExactOptionContractResult struct {
	Contract   ExactOptionContract
	Page       HistoricalSourcePage
	ObservedAt time.Time
}

// ExactOptionContractProvider retrieves one explicitly requested contract.
type ExactOptionContractProvider interface {
	GetExactOptionContract(context.Context, string) (ExactOptionContractResult, error)
}
