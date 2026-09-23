// etf-evidence reads public SPY issuer data without database or trading access.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/data/ssga"
)

func main() {
	profile := flag.String("profile", "", "saved issuer product workbook (requires holdings and fetched-at)")
	holdings := flag.String("holdings", "", "saved issuer holdings workbook")
	fetched := flag.String("fetched-at", "", "actual retrieval timestamp for saved files, RFC3339")
	sourceStrategy := flag.String("source-strategy", "", "read-only JSON export of the rejected SPY strategy")
	prepareDir := flag.String("prepare-dir", "", "new local directory for an inactive candidate proposal")
	flag.Parse()
	if (*sourceStrategy == "") != (*prepareDir == "") {
		fail(fmt.Errorf("source-strategy and prepare-dir must be supplied together"))
	}
	var fund *data.ETFFundamentals
	var err error
	if *profile != "" || *holdings != "" || *fetched != "" {
		if *profile == "" || *holdings == "" || *fetched == "" {
			fail(fmt.Errorf("all replay arguments are required"))
		}
		at, parseErr := time.Parse(time.RFC3339Nano, *fetched)
		if parseErr != nil {
			fail(parseErr)
		}
		p, readErr := os.ReadFile(*profile)
		if readErr != nil {
			fail(readErr)
		}
		h, readErr := os.ReadFile(*holdings)
		if readErr != nil {
			fail(readErr)
		}
		fund, err = ssga.Parse(p, h, at, at)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
		defer cancel()
		result, fetchErr := ssga.NewProvider().GetETFFundamentals(ctx, "SPY")
		err = fetchErr
		fund = result.ETF
	}
	if err != nil {
		fail(err)
	}
	if err = data.ValidateSPYETFFundamentals(fund, time.Now().UTC()); err != nil {
		fail(err)
	}
	if *prepareDir != "" {
		if err = prepareCandidate(*sourceStrategy, *prepareDir, fund); err != nil {
			fail(err)
		}
	}
	if err = json.NewEncoder(os.Stdout).Encode(fund); err != nil {
		fail(err)
	}
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
