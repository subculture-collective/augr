package dataset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// SourcePageEvidence preserves the unmodified response page and its selected
// source row. Byte slices serialize as base64, preserving source whitespace.
// This is provenance, not an entitlement or dataset-quality attestation.
type SourcePageEvidence struct {
	RequestPath string `json:"request_path"`
	Query       string `json:"query"`
	Page        []byte `json:"page"`
	RowIndex    int    `json:"row_index"`
	Row         []byte `json:"row"`
	SymbolKey   string `json:"symbol_key,omitempty"`
}

func cloneSourceEvidence(value *SourcePageEvidence) *SourcePageEvidence {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.Page, cloned.Row = bytes.Clone(value.Page), bytes.Clone(value.Row)
	return &cloned
}

func validateSourceEvidence(value *SourcePageEvidence) error {
	if value == nil {
		return nil
	}
	if len(value.Page) == 0 || len(value.Page) > 16*1024*1024 || len(value.Row) == 0 || len(value.Row) > 65536 || value.RowIndex < 0 {
		return fmt.Errorf("source evidence exceeds page/row bounds")
	}
	path, err := url.Parse(value.RequestPath)
	if err != nil || !strings.HasPrefix(value.RequestPath, "/") || strings.HasPrefix(value.RequestPath, "//") || path.IsAbs() || path.Host != "" || path.User != nil || path.RawQuery != "" || path.Fragment != "" {
		return fmt.Errorf("source evidence requires a relative request path")
	}
	query, err := url.ParseQuery(value.Query)
	if err != nil || query.Encode() != value.Query {
		return fmt.Errorf("source evidence query is not canonical")
	}
	for name := range query {
		// This exact provider parameter is an opaque pagination cursor, not
		// an authentication token. All other token parameters remain forbidden.
		if name == "page_token" && value.RequestPath == "/v1beta1/options/bars" && value.SymbolKey != "" && len(query[name]) == 1 && len(query.Get(name)) > 0 && len(query.Get(name)) <= 4096 {
			continue
		}
		key := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(name, "_", ""), "-", ""))
		if strings.Contains(key, "key") || strings.Contains(key, "token") || strings.Contains(key, "secret") || strings.Contains(key, "authorization") {
			return fmt.Errorf("source evidence query contains credential parameter")
		}
	}
	var page struct {
		Results []json.RawMessage `json:"results"`
	}
	if _, err := sourceObjectFields(value.Page); err != nil {
		return err
	}
	if _, err := sourceObjectFields(value.Row); err != nil {
		return err
	}
	if value.SymbolKey != "" {
		fields, err := sourceObjectFields(value.Page)
		if err != nil {
			return err
		}
		keyed, err := sourceObjectFields(fields["bars"])
		if err != nil || len(keyed) != 1 {
			return fmt.Errorf("source evidence requires exact symbol map")
		}
		rows, ok := keyed[value.SymbolKey]
		if !ok || json.Unmarshal(rows, &page.Results) != nil || page.Results == nil {
			return fmt.Errorf("source evidence symbol rows missing")
		}
	} else if err := json.Unmarshal(value.Page, &page); err != nil {
		return err
	}
	if value.RowIndex >= len(page.Results) || !bytes.Equal(page.Results[value.RowIndex], value.Row) {
		return fmt.Errorf("source evidence row does not reconstruct from page")
	}
	return nil
}

func sourceObjectFields(raw []byte) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("source must be JSON object")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("invalid source key")
		}
		if _, exists := fields[name]; exists {
			return nil, fmt.Errorf("duplicate source key")
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, err
		}
		fields[name] = value
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return nil, fmt.Errorf("invalid source object terminator")
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing source data")
	}
	return fields, nil
}

// validateBarSource rejects attaching unrelated raw evidence to a canonical bar.
// Only the explicitly supported source layout can carry this envelope for now.
func validateBarSource(input *MarketPayloadInput) error {
	source := input.SourceEvidence
	if source == nil {
		return nil
	}
	if err := validateSourceEvidence(source); err != nil {
		return err
	}
	if input.Kind == MarketPayloadOptionBar && input.Provider == "alpaca" {
		return validateAlpacaOptionBarSource(input)
	}
	if source.SymbolKey != "" {
		return fmt.Errorf("symbol-keyed evidence requires supported options layout")
	}
	if input.Kind != MarketPayloadStockBar || input.Provider != "polygon" || input.Bar == nil {
		return fmt.Errorf("unsupported source evidence payload layout")
	}
	if !strings.HasPrefix(source.RequestPath, "/v2/aggs/ticker/"+url.PathEscape(input.Symbol)+"/range/") {
		return fmt.Errorf("source request symbol mismatch")
	}
	parts := strings.Split(strings.TrimPrefix(source.RequestPath, "/"), "/")
	units := map[string]string{"1d": "1/day", "1Day": "1/day", "1m": "1/minute", "1Min": "1/minute", "5m": "5/minute", "15m": "15/minute", "1h": "1/hour"}
	if len(parts) != 9 || units[input.Timeframe] == "" || parts[5]+"/"+parts[6] != units[input.Timeframe] {
		return fmt.Errorf("source request timeframe mismatch")
	}
	from, fromErr := strconv.ParseInt(parts[7], 10, 64)
	to, toErr := strconv.ParseInt(parts[8], 10, 64)
	if fromErr != nil || toErr != nil || from > to || input.EffectiveAt.Before(time.UnixMilli(from)) || input.EffectiveAt.After(time.UnixMilli(to)) {
		return fmt.Errorf("source request interval mismatch")
	}
	query, _ := url.ParseQuery(source.Query)
	wantAdjusted := map[string]string{"raw": "false", "adjusted": "true"}[input.AdjustmentPolicy]
	if input.Feed != "sip" || wantAdjusted == "" || len(query["adjusted"]) != 1 || query.Get("adjusted") != wantAdjusted {
		return fmt.Errorf("source adjustment or feed mismatch")
	}
	pageFields, err := sourceObjectFields(source.Page)
	if err != nil {
		return err
	}
	if raw, ok := pageFields["ticker"]; ok {
		var ticker string
		if json.Unmarshal(raw, &ticker) != nil || ticker != input.Symbol {
			return fmt.Errorf("source response ticker mismatch")
		}
	}
	if raw, ok := pageFields["adjusted"]; ok {
		var adjusted bool
		if json.Unmarshal(raw, &adjusted) != nil || adjusted != (input.AdjustmentPolicy == "adjusted") {
			return fmt.Errorf("source response adjustment mismatch")
		}
	}
	if raw, ok := pageFields["status"]; ok {
		var status string
		if json.Unmarshal(raw, &status) != nil || (status != "OK" && status != "DELAYED") {
			return fmt.Errorf("source provider status unsuccessful")
		}
	}
	fields, err := sourceObjectFields(source.Row)
	if err != nil {
		return err
	}
	for _, field := range []struct{ name, want string }{
		{"o", input.Bar.Open},
		{"h", input.Bar.High},
		{"l", input.Bar.Low},
		{"c", input.Bar.Close},
		{"v", input.Bar.Volume},
		{"n", input.Bar.TradeCount},
		{"vw", input.Bar.VWAP},
	} {
		raw, ok := fields[field.name]
		if !ok || len(raw) > 128 || len(raw) == 0 || raw[0] == '"' {
			return fmt.Errorf("missing exact bar source field %s", field.name)
		}
		value, err := decimal.NewFromString(string(raw))
		if err != nil || value.Exponent() < -128 || value.Exponent() > 128 || value.String() != field.want {
			return fmt.Errorf("source bar field %s mismatch", field.name)
		}
	}
	timestamp, err := strconv.ParseInt(string(fields["t"]), 10, 64)
	if err != nil || !time.UnixMilli(timestamp).UTC().Equal(input.EffectiveAt) {
		return fmt.Errorf("source timestamp mismatch")
	}
	return nil
}
