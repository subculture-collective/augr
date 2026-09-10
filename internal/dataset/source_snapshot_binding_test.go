package dataset

import (
	"strings"
	"testing"
)

func TestSnapshotSourceObjectBinding(t *testing.T) {
	const symbol = "AAPL260116C00150000"
	const row = `{"latestQuote":{"bp":1}}`
	const page = `{"snapshots":{"AAPL260116C00150000":` + row + `,"AAPL260116P00150000":{"latestQuote":{"bp":2}}},"next_page_token":null}`
	for _, mode := range []string{"valid", "wrong symbol", "changed row", "array", "nonzero row index", "duplicate symbol", "duplicate collection", "credential", "invalid path", "missing collection"} {
		t.Run(mode, func(t *testing.T) {
			source := &SourcePageEvidence{RequestPath: "/v1beta1/options/snapshots/AAPL", Query: "feed=opra&page_token=cursor", Page: []byte(page), Row: []byte(row), SymbolKey: symbol}
			switch mode {
			case "wrong symbol":
				source.SymbolKey = "MSFT260116C00150000"
			case "changed row":
				source.Row = []byte(`{"latestQuote":{"bp":2}}`)
			case "array":
				source.Page = []byte(strings.Replace(page, row, "["+row+"]", 1))
			case "nonzero row index":
				source.RowIndex = 1
			case "duplicate symbol":
				source.Page = []byte(strings.Replace(page, "AAPL260116P00150000", symbol, 1))
			case "duplicate collection":
				source.Page = []byte(strings.TrimSuffix(page, "}") + `,"snapshots":{}}`)
			case "credential":
				source.Query = "api_token=secret&feed=opra"
			case "invalid path":
				source.RequestPath += "/extra"
			case "missing collection":
				source.Page = []byte(`{"next_page_token":null}`)
			}
			err := validateSourceEvidence(source)
			if (mode == "valid") != (err == nil) {
				t.Fatalf("binding validation: mode=%s err=%v", mode, err)
			}
		})
	}
}
