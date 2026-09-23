package ssga

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
)

func book(t *testing.T, rows []map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	archive := zip.NewWriter(&b)
	file, err := archive.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(file, `<worksheet><sheetData>`)
	for index, row := range rows {
		_, _ = io.WriteString(file, "<row>")
		for column, value := range row {
			_, _ = fmt.Fprintf(file, `<c r="%s%d" t="inlineStr"><is><t>`, column, index+1)
			if err := xml.EscapeText(file, []byte(value)); err != nil {
				t.Fatal(err)
			}
			_, _ = io.WriteString(file, `</t></is></c>`)
		}
		_, _ = io.WriteString(file, "</row>")
	}
	_, _ = io.WriteString(file, `</sheetData></worksheet>`)
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func fixtureRows(now time.Time) ([]map[string]string, []map[string]string) {
	p := []map[string]string{
		{"A": "Synthetic test only"},
		{"B": "Ticker", "D": "ISIN", "G": "Gross Expense Ratio", "W": "Total Net Assets"},
		{"A": now.Format("Jan 02 2006"), "B": "SPY", "D": "US78462F1030", "E": "78462F103", "I": "Equity", "G": "0.0945%", "W": "$123,456.78 M"},
	}
	h := []map[string]string{
		{"A": "Fund Name:", "B": "Synthetic SPY"},
		{"A": "Ticker Symbol:", "B": "SPY"},
		{"A": "Holdings:", "B": now.Format("As of 02-Jan-2006")},
		{"B": "Ticker", "E": "Weight", "H": "Local Currency"},
		{"B": "AAA", "E": "60", "H": "USD"},
		{"B": "BBB", "E": "40", "H": "USD"},
		{"A": "Synthetic footer"},
	}
	return p, h
}

func TestParsePreservesIdentityDatesUnitsAndHashes(t *testing.T) {
	now := time.Now().UTC()
	p, h := fixtureRows(now)
	fund, err := Parse(book(t, p), book(t, h), now, now)
	if err != nil {
		t.Fatal(err)
	}
	if fund.NetAssetsUSD != 123456780000 || *fund.GrossExpenseRatio != 0.000945 || len(fund.Holdings) != 2 || fund.Holdings[0].Weight != 0.6 {
		t.Fatalf("incorrect units: %+v", fund)
	}
	if err := data.ValidateSPYETFFundamentals(fund, now); err != nil {
		t.Fatal(err)
	}
	if fund.ProfileSource.SHA256 == fund.HoldingsSource.SHA256 {
		t.Fatal("source hashes collapsed")
	}
}

func TestMalformedWorkbookFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		edit func([]map[string]string, []map[string]string)
	}{
		{"wrong symbol", func(_, h []map[string]string) { h[1]["B"] = "QQQ" }},
		{"wrong isin", func(p, _ []map[string]string) { p[2]["D"] = "wrong" }},
		{"ambiguous units", func(p, _ []map[string]string) { p[2]["W"] = "123" }},
		{"missing fee", func(p, _ []map[string]string) { p[2]["G"] = "-" }},
		{"changed headers", func(_, h []map[string]string) { h[3]["E"] = "Weight Fraction" }},
		{"bad profile date", func(p, _ []map[string]string) { p[2]["A"] = "unknown" }},
		{"bad holdings date", func(_, h []map[string]string) { h[2]["B"] = "unknown" }},
		{"partial holding", func(_, h []map[string]string) { delete(h[4], "B") }},
		{"wrong currency", func(_, h []map[string]string) { h[4]["H"] = "EUR" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, h := fixtureRows(time.Now().UTC())
			tc.edit(p, h)
			if _, err := Parse(book(t, p), book(t, h), time.Now(), time.Now()); err == nil {
				t.Fatal("accepted malformed issuer data")
			}
		})
	}
	for _, raw := range [][]byte{nil, []byte("<html>blocked</html>"), make([]byte, maximumWorkbookBytes+1)} {
		if _, err := workbook(raw); err == nil {
			t.Fatal("accepted non-workbook")
		}
	}
	p, h := fixtureRows(time.Now().UTC())
	p = append(p, p[2])
	if _, err := Parse(book(t, p), book(t, h), time.Now(), time.Now()); err == nil {
		t.Fatal("accepted duplicate profile")
	}
}

type transport func(*http.Request) (*http.Response, error)

func (fn transport) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestProviderRequestsOnlyIssuerAndRejectsFailure(t *testing.T) {
	now := time.Now().UTC()
	p, h := fixtureRows(now)
	profile, holdings := book(t, p), book(t, h)
	calls := 0
	provider := NewProvider()
	provider.client.Transport = transport(func(r *http.Request) (*http.Response, error) {
		calls++
		var body []byte
		switch r.URL.String() {
		case ProfileURL:
			body = profile
		case HoldingsURL:
			body = holdings
		default:
			t.Fatalf("unexpected URL %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})
	result, err := provider.GetETFFundamentals(context.Background(), "SPY")
	if err != nil || result.ETF == nil || calls != 2 {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls, err)
	}
	if _, err := provider.GetETFFundamentals(context.Background(), "QQQ"); err == nil || calls != 2 {
		t.Fatal("unsupported symbol made request")
	}
	for _, status := range []int{302, 403, 429, 503} {
		provider.client.Transport = transport(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("unavailable")), Header: make(http.Header)}, nil
		})
		if _, err := provider.GetETFFundamentals(context.Background(), "SPY"); err == nil {
			t.Fatal("accepted HTTP failure")
		}
	}
	provider.client.Transport = transport(func(_ *http.Request) (*http.Response, error) { return nil, context.Canceled })
	if _, err := provider.GetETFFundamentals(context.Background(), "SPY"); err == nil {
		t.Fatal("accepted cancellation")
	}
	if err := provider.client.CheckRedirect(nil, nil); err == nil {
		t.Fatal("redirect allowed")
	}
}
