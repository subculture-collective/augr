package dataset

import (
	"bytes"
	"fmt"
	"testing"
)

func TestSourceEvidencePageBinding(t *testing.T) {
	row := []byte(`{ "c": 1.123456789012345678 }`)
	input := &SourcePageEvidence{RequestPath: "/v2/aggs/ticker/TEST/range/1/day/1/2", Query: "adjusted=false&sort=asc", Page: append(append([]byte(`{"results":[`), row...), []byte(`]}`)...), Row: row}
	if err := validateSourceEvidence(input); err != nil {
		t.Fatal(err)
	}
	cloned := cloneSourceEvidence(input)
	cloned.Row[0] = ' '
	if bytes.Equal(cloned.Row, input.Row) {
		t.Fatal("mutable source row alias")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*SourcePageEvidence)
	}{
		{"wrong_row", func(v *SourcePageEvidence) { v.Row = []byte(`{"c":1}`) }},
		{"wrong_index", func(v *SourcePageEvidence) { v.RowIndex = 1 }},
		{"negative_index", func(v *SourcePageEvidence) { v.RowIndex = -1 }},
		{"oversized_row", func(v *SourcePageEvidence) { v.Row = make([]byte, 65537) }},
		{"oversized_page", func(v *SourcePageEvidence) { v.Page = make([]byte, 16*1024*1024+1) }},
		{"absolute_url", func(v *SourcePageEvidence) { v.RequestPath = "https://example.com/" }},
		{"credential", func(v *SourcePageEvidence) { v.Query = "apiKey=synthetic" }},
		{"noncanonical_query", func(v *SourcePageEvidence) { v.Query = "sort=asc&adjusted=false" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := cloneSourceEvidence(input)
			tc.mutate(v)
			if validateSourceEvidence(v) == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
}

func TestMarketPayloadSourceEvidenceRoundtrip(t *testing.T) {
	input := testStockBarPayloadInput()
	legacy, err := NewMarketPayload(input)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(legacy.CanonicalBytes(), []byte("source_evidence")) {
		t.Fatal("absent source changed legacy format")
	}
	input.Provider = "polygon"
	input.AdjustmentPolicy = "raw"
	row := []byte(fmt.Sprintf(`{ "o":500,"h":503,"l":498,"c":501.25,"v":1234567,"n":9876,"vw":500.75,"t":%d }`, input.EffectiveAt.UnixMilli()))
	input.SourceEvidence = &SourcePageEvidence{RequestPath: fmt.Sprintf("/v2/aggs/ticker/SPY/range/1/day/%d/%d", input.EffectiveAt.UnixMilli(), input.EffectiveAt.UnixMilli()), Query: "adjusted=false", Page: append(append([]byte(`{"results":[`), row...), []byte(`]}`)...), Row: row}
	payload, err := NewMarketPayload(input)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := MarketPayloadFromCanonical(payload.ID(), payload.Digest(), payload.CanonicalBytes())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored.CanonicalBytes(), payload.CanonicalBytes()) || !bytes.Equal(restored.canonical.SourceEvidence.Row, row) {
		t.Fatal("raw evidence roundtrip changed bytes")
	}
	input.SourceEvidence.Row[0] = ' '
	if restored.canonical.SourceEvidence.Row[0] != '{' {
		t.Fatal("source evidence aliases input")
	}
	input.SourceEvidence = cloneSourceEvidence(restored.canonical.SourceEvidence)
	input.Bar.Close = "502"
	if _, err := NewMarketPayload(input); err == nil {
		t.Fatal("source-to-bar mismatch accepted")
	}
	input.Bar.Close = "501.25"
	input.SourceEvidence.Page = append(input.SourceEvidence.Page[:len(input.SourceEvidence.Page)-1], []byte(`,"results":[]}`)...)
	if _, err := NewMarketPayload(input); err == nil {
		t.Fatal("duplicate source results accepted")
	}
}
