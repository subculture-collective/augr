package alpaca

import (
	"strings"
	"testing"
)

const exactContractRow = `{"id":"6e58f870-fe73-4583-81e4-b9a37892c36f","symbol":"AAPL260116C00150000","underlying_symbol":"AAPL","underlying_asset_id":"b0b6dd9d-8b9b-48a9-ba46-b9d54906e415","type":"call","style":"american","expiration_date":"2026-01-16","strike_price":"150.000","size":"9007199254740993","status":"active","tradable":false}`

func TestDecodeExactOptionContractPreservesReportedFields(t *testing.T) {
	raw := []byte(exactContractRow)
	value, err := decodeExactOptionContract(raw)
	if err != nil || value.StrikePrice != "150" || value.Size != "9007199254740993" || value.Tradable {
		t.Fatalf("exact reported fields lost: %+v err=%v", value, err)
	}
	raw[0] = 'x'
	if string(value.Raw) != exactContractRow {
		t.Fatal("source bytes alias input")
	}
}

func TestDecodeExactOptionContractRejectsDefaultsAndMismatch(t *testing.T) {
	for name, change := range map[string][2]string{
		"missing size":     {`"size"`, `"multiplier"`},
		"missing style":    {`"style"`, `"unrelated"`},
		"null flag":        {`"tradable":false`, `"tradable":null`},
		"missing flag":     {`"tradable"`, `"unrelated"`},
		"wrong strike":     {`"150.000"`, `"150.001"`},
		"wrong expiry":     {`"2026-01-16"`, `"2026-01-17"`},
		"wrong underlying": {`"underlying_symbol":"AAPL"`, `"underlying_symbol":"MSFT"`},
		"wrong type":       {`"type":"call"`, `"type":"put"`},
		"fractional size":  {`"9007199254740993"`, `"100.5"`},
		"zero size":        {`"9007199254740993"`, `"0"`},
		"duplicate size":   {`"size":"9007199254740993"`, `"size":"100","size":"100"`},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeExactOptionContract([]byte(strings.Replace(exactContractRow, change[0], change[1], 1))); err == nil {
				t.Fatal("accepted inferred or mismatched contract evidence")
			}
		})
	}
}
