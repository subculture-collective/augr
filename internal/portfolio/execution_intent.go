package portfolio

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

const ExecutionIntentSchemaV1 = "portfolio-execution-intent-v1"

type ExecutionIntent struct { bytes []byte; digest string }

type executionIntentCanonical struct {
	Schema string `json:"schema"`; MarketType string `json:"market_type"`; Ticker string `json:"ticker"`; Side string `json:"side"`
	EntryPrice string `json:"entry_price"`; ProposedNotional string `json:"proposed_notional"`; ExpectedLoss string `json:"expected_loss"`
	Liquidity string `json:"liquidity"`; Spread string `json:"spread"`; MaxLossPerUnit string `json:"max_loss_per_unit"`
	RequiredCapitalPerUnit string `json:"required_capital_per_unit"`; QuoteObservedAt string `json:"quote_observed_at"`
	Delta string `json:"delta"`; Gamma string `json:"gamma"`; Theta string `json:"theta"`; Vega string `json:"vega"`
	Legs []executionIntentLeg `json:"legs"`
}
type executionIntentLeg struct {
	Sequence int `json:"sequence"`; ContractID string `json:"contract_id"`; OCCSymbol string `json:"occ_symbol"`; Underlying string `json:"underlying"`
	Expiry string `json:"expiry"`; OptionType string `json:"option_type"`; Strike string `json:"strike"`; Ratio int `json:"ratio"`; Side string `json:"side"`
	PositionIntent string `json:"position_intent"`; Bid string `json:"bid"`; Ask string `json:"ask"`; Multiplier int `json:"multiplier"`
}

func NewExecutionIntent(opportunity domain.Opportunity) (*ExecutionIntent, error) {
	if opportunity.MarketType != domain.MarketTypeStock && opportunity.MarketType != domain.MarketTypeOptions { return nil, fmt.Errorf("portfolio execution intent supports stocks and options") }
	if opportunity.Ticker == "" || !opportunity.Side.IsValid() || opportunity.EntryPrice < 0 || opportunity.ProposedNotional < 0 || opportunity.ExpectedLossUSD < 0 { return nil, fmt.Errorf("portfolio execution intent is invalid") }
	legs := make([]executionIntentLeg,0,len(opportunity.OptionLegs))
	quoteAt := ""
	if opportunity.QuoteObservedAt != nil { quoteAt = formatIntentTime(*opportunity.QuoteObservedAt) }
	if opportunity.MarketType == domain.MarketTypeOptions {
		if !validVertical(opportunity.OptionLegs) || opportunity.MaxLossPerUnit <= 0 || opportunity.RequiredCapitalUnit <= 0 || quoteAt == "" { return nil, fmt.Errorf("portfolio option execution intent is not defined-risk executable") }
		for _, leg := range opportunity.OptionLegs { legs=append(legs,executionIntentLeg{Sequence:leg.Sequence,ContractID:leg.ContractID.String(),OCCSymbol:leg.OCCSymbol,Underlying:leg.Underlying,Expiry:formatIntentTime(leg.Expiry),OptionType:leg.OptionType,Strike:intentDecimal(leg.Strike),Ratio:leg.Ratio,Side:leg.Side.String(),PositionIntent:leg.PositionIntent,Bid:intentDecimal(leg.Bid),Ask:intentDecimal(leg.Ask),Multiplier:leg.Multiplier}) }
	} else if len(opportunity.OptionLegs) != 0 { return nil, fmt.Errorf("stock execution intent cannot contain option legs") }
	canonical:=executionIntentCanonical{Schema:ExecutionIntentSchemaV1,MarketType:opportunity.MarketType.String(),Ticker:opportunity.Ticker,Side:opportunity.Side.String(),EntryPrice:intentDecimal(opportunity.EntryPrice),ProposedNotional:intentDecimal(opportunity.ProposedNotional),ExpectedLoss:intentDecimal(opportunity.ExpectedLossUSD),Liquidity:intentDecimal(opportunity.LiquidityUSD),Spread:intentDecimal(opportunity.SpreadPct),MaxLossPerUnit:intentDecimal(opportunity.MaxLossPerUnit),RequiredCapitalPerUnit:intentDecimal(opportunity.RequiredCapitalUnit),QuoteObservedAt:quoteAt,Delta:intentDecimal(opportunity.Delta),Gamma:intentDecimal(opportunity.Gamma),Theta:intentDecimal(opportunity.Theta),Vega:intentDecimal(opportunity.Vega),Legs:legs}
	raw,err:=json.Marshal(canonical);if err!=nil{return nil,err}; sum:=sha256.Sum256(raw)
	return &ExecutionIntent{bytes:raw,digest:hex.EncodeToString(sum[:])},nil
}

func (value *ExecutionIntent) Digest() string { if value==nil{return ""};return value.digest }
func (value *ExecutionIntent) CanonicalBytes() []byte { if value==nil{return nil};return append([]byte(nil),value.bytes...) }
func intentDecimal(value float64) string { return strconv.FormatFloat(value,'f',-1,64) }
func formatIntentTime(value time.Time) string { return value.UTC().Truncate(time.Microsecond).Format("2006-01-02T15:04:05.000000Z") }
