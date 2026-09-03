package dataset

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/economicid"
)

const (
	MarketPayloadSchemaV1 = "dataset-market-payload-v1"
	marketPayloadDomain   = "dataset-market-payload"
)

type MarketPayloadKind string

const (
	MarketPayloadStockBar       MarketPayloadKind = "stock_bar"
	MarketPayloadOptionBar      MarketPayloadKind = "option_bar"
	MarketPayloadOptionQuote    MarketPayloadKind = "option_quote"
	MarketPayloadOptionTrade    MarketPayloadKind = "option_trade"
	MarketPayloadOptionContract MarketPayloadKind = "option_contract"
	MarketPayloadOptionSnapshot MarketPayloadKind = "option_snapshot"
)

type BarPayload struct {
	Open       string `json:"open"`
	High       string `json:"high"`
	Low        string `json:"low"`
	Close      string `json:"close"`
	Volume     string `json:"volume"`
	TradeCount string `json:"trade_count"`
	VWAP       string `json:"vwap"`
}

type QuotePayload struct {
	BidPrice string `json:"bid_price"`
	BidSize  string `json:"bid_size"`
	AskPrice string `json:"ask_price"`
	AskSize  string `json:"ask_size"`
	Exchange string `json:"exchange"`
}

type TradePayload struct {
	Price    string `json:"price"`
	Size     string `json:"size"`
	Exchange string `json:"exchange"`
}

type OptionContractPayload struct {
	OptionType string `json:"option_type"`
	Strike     string `json:"strike"`
	Expiry     string `json:"expiry"`
	Multiplier string `json:"multiplier"`
	Style      string `json:"style"`
}

type OptionSnapshotPayload struct {
	Quote             QuotePayload `json:"quote"`
	LastTradePrice    string       `json:"last_trade_price"`
	LastTradeSize     string       `json:"last_trade_size"`
	ImpliedVolatility string       `json:"implied_volatility"`
	Delta             string       `json:"delta"`
	Gamma             string       `json:"gamma"`
	Theta             string       `json:"theta"`
	Vega              string       `json:"vega"`
	Rho               string       `json:"rho"`
}

type MarketPayloadInput struct {
	Kind                   MarketPayloadKind
	InstrumentID           uuid.UUID
	UnderlyingInstrumentID uuid.UUID
	Provider               string
	Feed                   string
	Symbol                 string
	UnderlyingSymbol       string
	Timeframe              string
	AdjustmentPolicy       string
	EffectiveAt            time.Time
	PublishedAt            *time.Time
	ObservedAt             time.Time
	AvailableAt            time.Time
	Revision               string
	CorrectionOfSHA256     string
	Bar                    *BarPayload
	Quote                  *QuotePayload
	Trade                  *TradePayload
	Contract               *OptionContractPayload
	Snapshot               *OptionSnapshotPayload
}

type marketPayloadCanonical struct {
	Schema                 string                 `json:"schema"`
	Kind                   MarketPayloadKind      `json:"kind"`
	InstrumentID           string                 `json:"instrument_id"`
	UnderlyingInstrumentID string                 `json:"underlying_instrument_id"`
	Provider               string                 `json:"provider"`
	Feed                   string                 `json:"feed"`
	Symbol                 string                 `json:"symbol"`
	UnderlyingSymbol       string                 `json:"underlying_symbol"`
	Timeframe              string                 `json:"timeframe"`
	AdjustmentPolicy       string                 `json:"adjustment_policy"`
	EffectiveAt            string                 `json:"effective_at"`
	PublishedAt            string                 `json:"published_at"`
	ObservedAt             string                 `json:"observed_at"`
	AvailableAt            string                 `json:"available_at"`
	Revision               string                 `json:"revision"`
	CorrectionOfSHA256     string                 `json:"correction_of_sha256"`
	Bar                    *BarPayload            `json:"bar"`
	Quote                  *QuotePayload          `json:"quote"`
	Trade                  *TradePayload          `json:"trade"`
	Contract               *OptionContractPayload `json:"contract"`
	Snapshot               *OptionSnapshotPayload `json:"snapshot"`
}

type MarketPayload struct {
	canonical marketPayloadCanonical
	bytes     json.RawMessage
	digest    string
	id        uuid.UUID
}

type MarketPayloadMetadata struct {
	Kind                   MarketPayloadKind
	InstrumentID           uuid.UUID
	UnderlyingInstrumentID uuid.UUID
	Provider               string
	Feed                   string
	Symbol                 string
	UnderlyingSymbol       string
	Timeframe              string
	AdjustmentPolicy       string
	EffectiveAt            time.Time
	PublishedAt            *time.Time
	ObservedAt             time.Time
	AvailableAt            time.Time
	Revision               string
	CorrectionOfSHA256     string
}

func NewMarketPayload(input MarketPayloadInput) (*MarketPayload, error) {
	if err := validateMarketPayloadInput(&input); err != nil {
		return nil, err
	}
	publishedAt := ""
	if input.PublishedAt != nil {
		publishedAt = formatTime(*input.PublishedAt)
	}
	canonical := marketPayloadCanonical{
		Schema: MarketPayloadSchemaV1, Kind: input.Kind, InstrumentID: input.InstrumentID.String(),
		Provider: input.Provider, Feed: input.Feed, Symbol: input.Symbol,
		UnderlyingSymbol: input.UnderlyingSymbol, Timeframe: input.Timeframe,
		AdjustmentPolicy: input.AdjustmentPolicy, EffectiveAt: formatTime(input.EffectiveAt),
		PublishedAt: publishedAt, ObservedAt: formatTime(input.ObservedAt), AvailableAt: formatTime(input.AvailableAt),
		Revision: input.Revision, CorrectionOfSHA256: input.CorrectionOfSHA256,
		Bar: cloneBar(input.Bar), Quote: cloneQuote(input.Quote), Trade: cloneTrade(input.Trade),
		Contract: cloneContract(input.Contract), Snapshot: cloneSnapshot(input.Snapshot),
	}
	if input.UnderlyingInstrumentID != uuid.Nil {
		canonical.UnderlyingInstrumentID = input.UnderlyingInstrumentID.String()
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("marshal dataset market payload: %w", err)
	}
	digestBytes := sha256.Sum256(encoded)
	digest := hex.EncodeToString(digestBytes[:])
	return &MarketPayload{
		canonical: canonical, bytes: encoded, digest: digest,
		id: economicid.DeterministicUUID(marketPayloadDomain, MarketPayloadSchemaV1+"@sha256:"+digest),
	}, nil
}

func MarketPayloadFromCanonical(id uuid.UUID, digest string, raw []byte) (*MarketPayload, error) {
	if id == uuid.Nil || !sha256Pattern.MatchString(digest) || hashBytes(raw) != digest {
		return nil, fmt.Errorf("dataset market payload envelope is invalid")
	}
	var canonical marketPayloadCanonical
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&canonical); err != nil {
		return nil, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	instrumentID, err := uuid.Parse(canonical.InstrumentID)
	if err != nil {
		return nil, fmt.Errorf("dataset market payload instrument: %w", err)
	}
	input := MarketPayloadInput{
		Kind: canonical.Kind, InstrumentID: instrumentID, Provider: canonical.Provider, Feed: canonical.Feed,
		Symbol: canonical.Symbol, UnderlyingSymbol: canonical.UnderlyingSymbol, Timeframe: canonical.Timeframe,
		AdjustmentPolicy: canonical.AdjustmentPolicy, EffectiveAt: parseTime(canonical.EffectiveAt),
		ObservedAt: parseTime(canonical.ObservedAt), AvailableAt: parseTime(canonical.AvailableAt),
		Revision: canonical.Revision, CorrectionOfSHA256: canonical.CorrectionOfSHA256,
		Bar: cloneBar(canonical.Bar), Quote: cloneQuote(canonical.Quote), Trade: cloneTrade(canonical.Trade),
		Contract: cloneContract(canonical.Contract), Snapshot: cloneSnapshot(canonical.Snapshot),
	}
	if canonical.UnderlyingInstrumentID != "" {
		input.UnderlyingInstrumentID, err = uuid.Parse(canonical.UnderlyingInstrumentID)
		if err != nil {
			return nil, fmt.Errorf("dataset market payload underlying instrument: %w", err)
		}
	}
	if canonical.PublishedAt != "" {
		publishedAt := parseTime(canonical.PublishedAt)
		input.PublishedAt = &publishedAt
	}
	value, err := NewMarketPayload(input)
	if err != nil {
		return nil, err
	}
	if canonical.Schema != MarketPayloadSchemaV1 || value.ID() != id || value.Digest() != digest || !bytes.Equal(value.bytes, raw) {
		return nil, fmt.Errorf("dataset market payload identity does not reconstruct")
	}
	return value, nil
}

func validateMarketPayloadInput(input *MarketPayloadInput) error {
	if input == nil || input.InstrumentID == uuid.Nil || !validMarketPayloadKind(input.Kind) ||
		!canonicalRequired(input.Provider) || !canonicalRequired(input.Feed) || !canonicalRequired(input.Symbol) ||
		!canonicalToken(input.UnderlyingSymbol) || !canonicalRequired(input.Timeframe) ||
		!canonicalRequired(input.AdjustmentPolicy) || !canonicalTimeValue(input.EffectiveAt) ||
		!canonicalTimeValue(input.ObservedAt) || !canonicalTimeValue(input.AvailableAt) ||
		input.ObservedAt.After(input.AvailableAt) || !canonicalToken(input.Revision) ||
		(input.CorrectionOfSHA256 != "" && !sha256Pattern.MatchString(input.CorrectionOfSHA256)) {
		return fmt.Errorf("dataset market payload metadata is invalid")
	}
	if input.PublishedAt != nil && (!canonicalTimeValue(*input.PublishedAt) || input.PublishedAt.After(input.ObservedAt)) {
		return fmt.Errorf("dataset market payload publication time is invalid")
	}
	variants := 0
	for _, present := range []bool{input.Bar != nil, input.Quote != nil, input.Trade != nil, input.Contract != nil, input.Snapshot != nil} {
		if present {
			variants++
		}
	}
	if variants != 1 {
		return fmt.Errorf("dataset market payload requires exactly one typed body")
	}
	switch input.Kind {
	case MarketPayloadStockBar:
		if input.Bar == nil || input.UnderlyingInstrumentID != uuid.Nil || input.UnderlyingSymbol != "" {
			return fmt.Errorf("dataset stock bar payload is invalid")
		}
	case MarketPayloadOptionBar:
		if input.Bar == nil || !validOptionIdentity(input) {
			return fmt.Errorf("dataset option bar payload is invalid")
		}
	case MarketPayloadOptionQuote:
		if input.Quote == nil || !validOptionIdentity(input) {
			return fmt.Errorf("dataset option quote payload is invalid")
		}
	case MarketPayloadOptionTrade:
		if input.Trade == nil || !validOptionIdentity(input) {
			return fmt.Errorf("dataset option trade payload is invalid")
		}
	case MarketPayloadOptionContract:
		if input.Contract == nil || !validOptionIdentity(input) {
			return fmt.Errorf("dataset option contract payload is invalid")
		}
	case MarketPayloadOptionSnapshot:
		if input.Snapshot == nil || !validOptionIdentity(input) {
			return fmt.Errorf("dataset option snapshot payload is invalid")
		}
	}
	if input.Bar != nil {
		if err := validateBar(*input.Bar); err != nil {
			return err
		}
	}
	if input.Quote != nil {
		if err := validateQuote(*input.Quote); err != nil {
			return err
		}
	}
	if input.Trade != nil {
		if err := validateTrade(*input.Trade); err != nil {
			return err
		}
	}
	if input.Contract != nil {
		if err := validateContract(*input.Contract); err != nil {
			return err
		}
	}
	if input.Snapshot != nil {
		if err := validateSnapshot(*input.Snapshot); err != nil {
			return err
		}
	}
	return nil
}

func validOptionIdentity(input *MarketPayloadInput) bool {
	return input.UnderlyingInstrumentID != uuid.Nil && canonicalRequired(input.UnderlyingSymbol)
}

func validMarketPayloadKind(kind MarketPayloadKind) bool {
	switch kind {
	case MarketPayloadStockBar, MarketPayloadOptionBar, MarketPayloadOptionQuote,
		MarketPayloadOptionTrade, MarketPayloadOptionContract, MarketPayloadOptionSnapshot:
		return true
	default:
		return false
	}
}

func validateBar(value BarPayload) error {
	open, err := canonicalDecimal(value.Open, false, true)
	if err != nil {
		return fmt.Errorf("dataset market bar open is invalid")
	}
	high, err := canonicalDecimal(value.High, false, true)
	if err != nil {
		return fmt.Errorf("dataset market bar high is invalid")
	}
	low, err := canonicalDecimal(value.Low, false, true)
	if err != nil {
		return fmt.Errorf("dataset market bar low is invalid")
	}
	closeValue, err := canonicalDecimal(value.Close, false, true)
	if err != nil {
		return fmt.Errorf("dataset market bar close is invalid")
	}
	if _, err = canonicalDecimal(value.Volume, false, false); err != nil {
		return fmt.Errorf("dataset market bar volume is invalid")
	}
	if _, err = canonicalDecimal(value.TradeCount, false, false); err != nil {
		return fmt.Errorf("dataset market bar trade count is invalid")
	}
	if _, err = canonicalDecimal(value.VWAP, false, false); err != nil {
		return fmt.Errorf("dataset market bar vwap is invalid")
	}
	if high.LessThan(open) || high.LessThan(closeValue) || high.LessThan(low) || low.GreaterThan(open) || low.GreaterThan(closeValue) {
		return fmt.Errorf("dataset market bar OHLC relationship is invalid")
	}
	return nil
}

func validateQuote(value QuotePayload) error {
	bid, err := canonicalDecimal(value.BidPrice, false, false)
	if err != nil {
		return fmt.Errorf("dataset market quote bid is invalid")
	}
	ask, err := canonicalDecimal(value.AskPrice, false, false)
	if err != nil || ask.LessThan(bid) {
		return fmt.Errorf("dataset market quote ask is invalid")
	}
	if _, err = canonicalDecimal(value.BidSize, false, false); err != nil {
		return fmt.Errorf("dataset market quote bid size is invalid")
	}
	if _, err = canonicalDecimal(value.AskSize, false, false); err != nil {
		return fmt.Errorf("dataset market quote ask size is invalid")
	}
	if !canonicalToken(value.Exchange) {
		return fmt.Errorf("dataset market quote exchange is invalid")
	}
	return nil
}

func validateTrade(value TradePayload) error {
	if _, err := canonicalDecimal(value.Price, false, true); err != nil {
		return fmt.Errorf("dataset market trade price is invalid")
	}
	if _, err := canonicalDecimal(value.Size, false, true); err != nil {
		return fmt.Errorf("dataset market trade size is invalid")
	}
	if !canonicalToken(value.Exchange) {
		return fmt.Errorf("dataset market trade exchange is invalid")
	}
	return nil
}

func validateContract(value OptionContractPayload) error {
	if value.OptionType != "call" && value.OptionType != "put" {
		return fmt.Errorf("dataset option contract type is invalid")
	}
	if _, err := canonicalDecimal(value.Strike, false, true); err != nil {
		return fmt.Errorf("dataset option contract strike is invalid")
	}
	if _, err := canonicalDecimal(value.Multiplier, false, true); err != nil {
		return fmt.Errorf("dataset option contract multiplier is invalid")
	}
	if _, err := time.Parse("2006-01-02", value.Expiry); err != nil {
		return fmt.Errorf("dataset option contract expiry is invalid")
	}
	if value.Style != "american" && value.Style != "european" {
		return fmt.Errorf("dataset option contract style is invalid")
	}
	return nil
}

func validateSnapshot(value OptionSnapshotPayload) error {
	if err := validateQuote(value.Quote); err != nil {
		return err
	}
	for name, number := range map[string]string{
		"last trade price": value.LastTradePrice, "last trade size": value.LastTradeSize,
		"implied volatility": value.ImpliedVolatility,
	} {
		if _, err := canonicalDecimal(number, false, false); err != nil {
			return fmt.Errorf("dataset option snapshot %s is invalid", name)
		}
	}
	for name, number := range map[string]string{"delta": value.Delta, "gamma": value.Gamma, "theta": value.Theta, "vega": value.Vega, "rho": value.Rho} {
		if _, err := canonicalDecimal(number, true, false); err != nil {
			return fmt.Errorf("dataset option snapshot %s is invalid", name)
		}
	}
	return nil
}

func canonicalDecimal(value string, allowNegative, positive bool) (decimal.Decimal, error) {
	if value == "" || value != strings.TrimSpace(value) || strings.ContainsAny(value, "eE+") {
		return decimal.Zero, fmt.Errorf("decimal is not canonical")
	}
	parsed, err := decimal.NewFromString(value)
	if err != nil || parsed.String() != value || !allowNegative && parsed.IsNegative() || positive && !parsed.IsPositive() {
		return decimal.Zero, fmt.Errorf("decimal is invalid")
	}
	return parsed, nil
}

func (value *MarketPayload) ID() uuid.UUID {
	if value == nil {
		return uuid.Nil
	}
	return value.id
}

func (value *MarketPayload) Digest() string {
	if value == nil {
		return ""
	}
	return value.digest
}

func (value *MarketPayload) CanonicalBytes() json.RawMessage {
	if value == nil {
		return nil
	}
	return append(json.RawMessage(nil), value.bytes...)
}

func (value *MarketPayload) Kind() MarketPayloadKind {
	if value == nil {
		return ""
	}
	return value.canonical.Kind
}

func (value *MarketPayload) Metadata() MarketPayloadMetadata {
	if value == nil {
		return MarketPayloadMetadata{}
	}
	metadata := MarketPayloadMetadata{
		Kind: value.canonical.Kind, InstrumentID: uuid.MustParse(value.canonical.InstrumentID),
		Provider: value.canonical.Provider, Feed: value.canonical.Feed, Symbol: value.canonical.Symbol,
		UnderlyingSymbol: value.canonical.UnderlyingSymbol, Timeframe: value.canonical.Timeframe,
		AdjustmentPolicy: value.canonical.AdjustmentPolicy, EffectiveAt: parseTime(value.canonical.EffectiveAt),
		ObservedAt: parseTime(value.canonical.ObservedAt), AvailableAt: parseTime(value.canonical.AvailableAt),
		Revision: value.canonical.Revision, CorrectionOfSHA256: value.canonical.CorrectionOfSHA256,
	}
	if value.canonical.UnderlyingInstrumentID != "" {
		metadata.UnderlyingInstrumentID = uuid.MustParse(value.canonical.UnderlyingInstrumentID)
	}
	if value.canonical.PublishedAt != "" {
		publishedAt := parseTime(value.canonical.PublishedAt)
		metadata.PublishedAt = &publishedAt
	}
	return metadata
}

func (value *MarketPayload) InstrumentID() uuid.UUID {
	if value == nil {
		return uuid.Nil
	}
	return uuid.MustParse(value.canonical.InstrumentID)
}

func (value *MarketPayload) EffectiveAt() time.Time {
	if value == nil {
		return time.Time{}
	}
	return parseTime(value.canonical.EffectiveAt)
}

func (value *MarketPayload) AvailableAt() time.Time {
	if value == nil {
		return time.Time{}
	}
	return parseTime(value.canonical.AvailableAt)
}

func (value *MarketPayload) Symbol() string {
	if value == nil {
		return ""
	}
	return value.canonical.Symbol
}

func (value *MarketPayload) Timeframe() string {
	if value == nil {
		return ""
	}
	return value.canonical.Timeframe
}

func (value *MarketPayload) Bar() *BarPayload {
	if value == nil {
		return nil
	}
	return cloneBar(value.canonical.Bar)
}

func (value *MarketPayload) Quote() *QuotePayload {
	if value == nil {
		return nil
	}
	return cloneQuote(value.canonical.Quote)
}

func (value *MarketPayload) Trade() *TradePayload {
	if value == nil {
		return nil
	}
	return cloneTrade(value.canonical.Trade)
}

func (value *MarketPayload) Contract() *OptionContractPayload {
	if value == nil {
		return nil
	}
	return cloneContract(value.canonical.Contract)
}

func (value *MarketPayload) Snapshot() *OptionSnapshotPayload {
	if value == nil {
		return nil
	}
	return cloneSnapshot(value.canonical.Snapshot)
}

func cloneBar(value *BarPayload) *BarPayload {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneQuote(value *QuotePayload) *QuotePayload {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneTrade(value *TradePayload) *TradePayload {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneContract(value *OptionContractPayload) *OptionContractPayload {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneSnapshot(value *OptionSnapshotPayload) *OptionSnapshotPayload {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}
