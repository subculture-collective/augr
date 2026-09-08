// Command augr-dataset-import performs a bounded operator-only import directly
// from a configured provider into immutable schema-110 evidence. Credentials
// are accepted only through the environment and are never emitted.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data/alpaca"
	"github.com/PatrickFanella/get-rich-quick/internal/data/polygon"
	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/datasetimport"
	postgresrepo "github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
)

type importInput struct {
	Mode                   datasetimport.Mode `json:"mode"`
	Provider               string             `json:"provider"`
	Feed                   string             `json:"feed"`
	Timeframe              string             `json:"timeframe"`
	AdjustmentPolicy       string             `json:"adjustment_policy"`
	From                   time.Time          `json:"from"`
	To                     time.Time          `json:"to"`
	DecisionCutoff         time.Time          `json:"decision_cutoff,omitempty"`
	Universe               []string           `json:"universe"`
	OptionSymbols          []string           `json:"option_symbols,omitempty"`
	MaxPayloads            int                `json:"max_payloads"`
	ExpectedPayloadCount   *int               `json:"expected_payload_count"`
	ExpectedPartitionCount *int               `json:"expected_partition_count"`
	SourceName             string             `json:"source_name"`
	Namespace              string             `json:"namespace"`
	SymbologyVersion       string             `json:"symbology_version"`
	Timezone               string             `json:"timezone"`
	Calendar               string             `json:"calendar"`
	License                string             `json:"license"`
	RetentionPolicy        string             `json:"retention_policy"`
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	flags := flag.NewFlagSet("augr-dataset-import", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	inputPath := flags.String("input", "-", "JSON input path, or - for stdin")
	dryRun := flags.Bool("dry-run", false, "fetch and report exact hashes without persisting")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("usage: augr-dataset-import [--input path|-] [--dry-run]")
	}
	databaseURL := firstSet(os.Getenv("DB_URL"), os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("augr-dataset-import: DB_URL or DATABASE_URL is required in the environment")
	}
	var input importInput
	if err := decodeInput(*inputPath, stdin, &input); err != nil {
		return err
	}
	db, err := postgresrepo.NewDB(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	version, err := postgresrepo.CurrentSchemaVersion(ctx, db.Pool)
	if err != nil || !postgresrepo.IsSchemaVersionCompatible(version) {
		if err != nil {
			return err
		}
		return fmt.Errorf("augr-dataset-import: schema version %d does not match required version %d", version, postgresrepo.RequiredSchemaVersion)
	}

	providerSource := &datasetimport.ProviderSource{
		Mode: input.Mode, Instruments: postgresrepo.NewInstrumentRepo(db.Pool), OptionSymbols: input.OptionSymbols, Clock: time.Now,
	}
	switch input.Mode {
	case datasetimport.ModeStockBars:
		if input.Provider != "polygon" || strings.TrimSpace(os.Getenv("POLYGON_API_KEY")) == "" {
			return errors.New("augr-dataset-import: stock_bars requires provider polygon and POLYGON_API_KEY")
		}
		providerSource.Stock = polygon.NewProvider(polygon.NewClient(os.Getenv("POLYGON_API_KEY"), slog.Default()))
	case datasetimport.ModeOptionBars, datasetimport.ModeOptionTrades, datasetimport.ModeOptionChainSnapshot:
		if input.Provider != "alpaca" || strings.TrimSpace(os.Getenv("ALPACA_API_KEY")) == "" || strings.TrimSpace(os.Getenv("ALPACA_API_SECRET")) == "" {
			return errors.New("augr-dataset-import: option imports require provider alpaca and ALPACA_API_KEY plus ALPACA_API_SECRET")
		}
		providerSource.Options = alpaca.NewOptionsDataProvider(os.Getenv("ALPACA_API_KEY"), os.Getenv("ALPACA_API_SECRET"), slog.Default())
	default:
		return fmt.Errorf("augr-dataset-import: unsupported mode %q", input.Mode)
	}
	importer, err := dataset.NewMarketImporter(providerSource, postgresrepo.NewDatasetRepo(db.Pool))
	if err != nil {
		return err
	}
	summary, err := importer.Import(ctx, dataset.MarketImportRequest{
		Provider: input.Provider, Feed: input.Feed, Timeframe: input.Timeframe, AdjustmentPolicy: input.AdjustmentPolicy,
		From: input.From, To: input.To, DecisionCutoff: input.DecisionCutoff, Universe: input.Universe,
		MaxPayloads: input.MaxPayloads, ExpectedPayloadCount: input.ExpectedPayloadCount,
		ExpectedPartitionCount: input.ExpectedPartitionCount, SourceName: input.SourceName, Namespace: input.Namespace,
		SymbologyVersion: input.SymbologyVersion, Timezone: input.Timezone, Calendar: input.Calendar,
		License: input.License, RetentionPolicy: input.RetentionPolicy, DryRun: *dryRun,
	}, time.Now().UTC().Truncate(time.Microsecond))
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(summary)
}

func decodeInput(path string, stdin io.Reader, target any) error {
	reader := stdin
	var file *os.File
	if path != "-" {
		var err error
		file, err = os.Open(path)
		if err != nil {
			return fmt.Errorf("augr-dataset-import: open input: %w", err)
		}
		defer func() { _ = file.Close() }()
		reader = file
	}
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("augr-dataset-import: decode input: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("augr-dataset-import: input must contain exactly one JSON value")
	}
	return nil
}

func firstSet(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
