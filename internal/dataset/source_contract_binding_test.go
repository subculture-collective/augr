package dataset

import (
	"bytes"
	"testing"
)

func TestContractSourceWholeObjectBinding(t *testing.T) {
	const symbol = "AAPL260116C00150000"
	const page = "{\n  \"symbol\": \"" + symbol + "\", \"size\": \"100\"\n}"
	for _, mode := range []string{"valid", "wrong key", "wrong response", "wrong path", "extra path", "query", "cursor", "row index", "changed bytes", "duplicate symbol", "missing symbol", "null symbol", "array", "trailing data"} {
		t.Run(mode, func(t *testing.T) {
			source := &SourcePageEvidence{RequestPath: "/v2/options/contracts/" + symbol, SymbolKey: symbol, Page: []byte(page), Row: []byte(page)}
			switch mode {
			case "wrong key":
				source.SymbolKey = "AAPL260116P00150000"
			case "wrong response":
				source.Page = bytes.ReplaceAll(source.Page, []byte(symbol), []byte("AAPL260116P00150000"))
				source.Row = bytes.Clone(source.Page)
			case "wrong path":
				source.RequestPath = "/v2/options/contracts/not-occ"
			case "extra path":
				source.RequestPath += "/extra"
			case "query":
				source.Query = "feed=opra"
			case "cursor":
				source.Query = "page_token=cursor"
			case "row index":
				source.RowIndex = 1
			case "changed bytes":
				source.Row = bytes.ReplaceAll(source.Row, []byte("\n"), nil)
			case "duplicate symbol":
				source.Page = []byte(`{"symbol":"` + symbol + `","symbol":"` + symbol + `"}`)
				source.Row = bytes.Clone(source.Page)
			case "missing symbol":
				source.Page, source.Row = []byte(`{}`), []byte(`{}`)
			case "null symbol":
				source.Page, source.Row = []byte(`{"symbol":null}`), []byte(`{"symbol":null}`)
			case "array":
				source.Page, source.Row = []byte("["+page+"]"), []byte("["+page+"]")
			case "trailing data":
				source.Page, source.Row = []byte(page+" {}"), []byte(page+" {}")
			}
			err := validateSourceEvidence(source)
			if (mode == "valid") != (err == nil) {
				t.Fatalf("mode=%s err=%v", mode, err)
			}
		})
	}
}
