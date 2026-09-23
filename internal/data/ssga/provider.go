// Package ssga reads the issuer's public SPY workbooks. It requires no API key.
package ssga

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
)

const (
	ProfileURL           = "https://www.ssga.com/library-content/products/fund-data/etfs/us/spdr-product-data-us-en.xlsx"
	HoldingsURL          = "https://www.ssga.com/library-content/products/fund-data/etfs/us/holdings-daily-us-en-spy.xlsx"
	maximumWorkbookBytes = 4 << 20
)

type Provider struct{ client *http.Client }

func NewProvider() *Provider {
	return &Provider{client: &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return fmt.Errorf("ssga: redirect refused") }}}
}

func (p *Provider) GetETFFundamentals(ctx context.Context, ticker string) (data.Fundamentals, error) {
	if ticker != "SPY" {
		return data.Fundamentals{}, fmt.Errorf("ssga: only SPY is supported")
	}
	profile, profileTime, err := p.fetch(ctx, ProfileURL)
	if err != nil {
		return data.Fundamentals{}, err
	}
	holdings, holdingsTime, err := p.fetch(ctx, HoldingsURL)
	if err != nil {
		return data.Fundamentals{}, err
	}
	fund, err := Parse(profile, holdings, profileTime, holdingsTime)
	if err != nil {
		return data.Fundamentals{}, err
	}
	if err = data.ValidateSPYETFFundamentals(fund, time.Now().UTC()); err != nil {
		return data.Fundamentals{}, err
	}
	return data.Fundamentals{Ticker: ticker, ETF: fund, FetchedAt: holdingsTime}, nil
}

func (p *Provider) fetch(ctx context.Context, url string) ([]byte, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, time.Time{}, err
	}
	response, err := p.client.Do(req)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("ssga: fetch: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, time.Time{}, fmt.Errorf("ssga: HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumWorkbookBytes+1))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("ssga: read: %w", err)
	}
	if len(body) > maximumWorkbookBytes {
		return nil, time.Time{}, fmt.Errorf("ssga: workbook too large")
	}
	return body, time.Now().UTC(), nil
}

type sharedString struct {
	Text string `xml:"t"`
	Runs []struct {
		Text string `xml:"t"`
	} `xml:"r"`
}

func (s sharedString) text() string {
	value := s.Text
	for _, run := range s.Runs {
		value += run.Text
	}
	return value
}

type cell struct {
	Reference string       `xml:"r,attr"`
	Type      string       `xml:"t,attr"`
	Value     string       `xml:"v"`
	Inline    sharedString `xml:"is"`
	Formula   *string      `xml:"f"`
}

// workbook reads bounded OOXML values only; formulas and ambiguous layouts fail closed.
func workbook(raw []byte) ([]map[string]string, error) {
	if len(raw) > maximumWorkbookBytes {
		return nil, fmt.Errorf("ssga: workbook too large")
	}
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("ssga: invalid workbook")
	}
	files := map[string]*zip.File{}
	var total uint64
	for _, f := range archive.File {
		if files[f.Name] != nil || f.UncompressedSize64 > 16<<20 {
			return nil, fmt.Errorf("ssga: invalid workbook members")
		}
		total += f.UncompressedSize64
		if total > 32<<20 {
			return nil, fmt.Errorf("ssga: expanded workbook too large")
		}
		files[f.Name] = f
	}
	read := func(name string, dest any) error {
		f := files[name]
		if f == nil {
			return fmt.Errorf("ssga: missing workbook member")
		}
		r, err := f.Open()
		if err != nil {
			return err
		}
		defer func() { _ = r.Close() }()
		return xml.NewDecoder(io.LimitReader(r, 16<<20)).Decode(dest)
	}
	var dictionary struct {
		Items []sharedString `xml:"si"`
	}
	if files["xl/sharedStrings.xml"] != nil {
		if err := read("xl/sharedStrings.xml", &dictionary); err != nil {
			return nil, err
		}
	}
	var sheet struct {
		Rows []struct {
			Cells []cell `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err := read("xl/worksheets/sheet1.xml", &sheet); err != nil {
		return nil, err
	}
	if len(sheet.Rows) > 5000 {
		return nil, fmt.Errorf("ssga: too many workbook rows")
	}
	rows := make([]map[string]string, 0, len(sheet.Rows))
	for _, row := range sheet.Rows {
		values := map[string]string{}
		seen := map[string]bool{}
		for _, c := range row.Cells {
			if c.Formula != nil {
				return nil, fmt.Errorf("ssga: formulas not accepted")
			}
			column := strings.TrimRight(c.Reference, "0123456789")
			if column == "" || seen[column] {
				return nil, fmt.Errorf("ssga: ambiguous cell")
			}
			seen[column] = true
			value := c.Value
			switch c.Type {
			case "s":
				index, err := strconv.Atoi(value)
				if err != nil || index < 0 || index >= len(dictionary.Items) {
					return nil, fmt.Errorf("ssga: invalid shared string")
				}
				value = dictionary.Items[index].text()
			case "inlineStr":
				value = c.Inline.text()
			case "", "n", "str":
			default:
				return nil, fmt.Errorf("ssga: unsupported cell type")
			}
			values[column] = strings.TrimSpace(value)
		}
		rows = append(rows, values)
	}
	return rows, nil
}

var (
	dollarsMillions = regexp.MustCompile(`^\$([0-9]+(?:,[0-9]{3})*(?:\.[0-9]+)?) M$`)
	percent         = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)%$`)
)

// Parse binds normalized data to the exact issuer bytes and their retrieval times.
func Parse(profile, holdings []byte, profileTime, holdingsTime time.Time) (*data.ETFFundamentals, error) {
	products, err := workbook(profile)
	if err != nil {
		return nil, err
	}
	positions, err := workbook(holdings)
	if err != nil {
		return nil, err
	}
	if len(products) < 3 || products[1]["B"] != "Ticker" || products[1]["D"] != "ISIN" || products[1]["G"] != "Gross Expense Ratio" || products[1]["W"] != "Total Net Assets" {
		return nil, fmt.Errorf("ssga: product schema changed")
	}
	var row map[string]string
	for _, value := range products[2:] {
		if value["B"] == "SPY" {
			if row != nil {
				return nil, fmt.Errorf("ssga: duplicate SPY profile")
			}
			row = value
		}
	}
	if row == nil || row["D"] != "US78462F1030" || row["E"] != "78462F103" || row["I"] != "Equity" {
		return nil, fmt.Errorf("ssga: SPY profile identity mismatch")
	}
	assets, fee := dollarsMillions.FindStringSubmatch(row["W"]), percent.FindStringSubmatch(row["G"])
	if len(assets) != 2 || len(fee) != 2 {
		return nil, fmt.Errorf("ssga: assets or expense units changed")
	}
	netAssets, err := strconv.ParseFloat(strings.ReplaceAll(assets[1], ",", ""), 64)
	if err != nil {
		return nil, err
	}
	expense, err := strconv.ParseFloat(fee[1], 64)
	if err != nil {
		return nil, err
	}
	expense /= 100
	profileDate, err := time.Parse("Jan 02 2006", row["A"])
	if err != nil {
		return nil, fmt.Errorf("ssga: profile date invalid")
	}
	if len(positions) < 5 || positions[0]["A"] != "Fund Name:" || positions[1]["A"] != "Ticker Symbol:" || positions[1]["B"] != "SPY" || positions[2]["A"] != "Holdings:" || positions[3]["B"] != "Ticker" || positions[3]["E"] != "Weight" || positions[3]["H"] != "Local Currency" {
		return nil, fmt.Errorf("ssga: holdings schema or identity changed")
	}
	holdingsDate, err := time.Parse("As of 02-Jan-2006", positions[2]["B"])
	if err != nil {
		return nil, fmt.Errorf("ssga: holdings date invalid")
	}
	result := &data.ETFFundamentals{Contract: data.SPYETFContractV1, Ticker: "SPY", ISIN: row["D"], Currency: "USD", NetAssetsUSD: netAssets * 1e6, GrossExpenseRatio: &expense}
	// Footer rows have no weight or ticker. A partly populated holding is rejected.
	for _, position := range positions[4:] {
		if position["B"] == "" && position["E"] == "" {
			continue
		}
		weight, err := strconv.ParseFloat(position["E"], 64)
		if err != nil || position["B"] == "" || position["H"] != "USD" {
			return nil, fmt.Errorf("ssga: malformed holding")
		}
		result.Holdings = append(result.Holdings, data.ETFHolding{Symbol: position["B"], Weight: weight / 100})
	}
	source := func(raw []byte, url string, asOf, fetched time.Time) data.ETFSource {
		sum := sha256.Sum256(raw)
		return data.ETFSource{URL: url, SHA256: hex.EncodeToString(sum[:]), AsOf: asOf, FetchedAt: fetched}
	}
	result.ProfileSource = source(profile, ProfileURL, profileDate, profileTime)
	result.HoldingsSource = source(holdings, HoldingsURL, holdingsDate, holdingsTime)
	return result, nil
}
