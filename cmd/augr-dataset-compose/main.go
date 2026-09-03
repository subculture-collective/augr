// Command augr-dataset-compose combines explicit immutable source manifests
// into one promotion-scope candidate. It never selects a latest manifest and
// never calls a market-data provider.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	postgresrepo "github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
)

type composeInput struct {
	ManifestIDs            []uuid.UUID `json:"manifest_ids"`
	DecisionCutoff         time.Time   `json:"decision_cutoff"`
	ExpectedPayloadCount   int         `json:"expected_payload_count"`
	ExpectedPartitionCount int         `json:"expected_partition_count"`
}

type composeSummary struct {
	DryRun            bool        `json:"dry_run"`
	ManifestID        uuid.UUID   `json:"manifest_id"`
	ManifestSHA256    string      `json:"manifest_sha256"`
	SourceManifestIDs []uuid.UUID `json:"source_manifest_ids"`
	PayloadCount      int         `json:"payload_count"`
	PartitionCount    int         `json:"partition_count"`
	PayloadSHA256     []string    `json:"payload_sha256"`
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	flags := flag.NewFlagSet("augr-dataset-compose", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	inputPath := flags.String("input", "-", "JSON input path, or - for stdin")
	dryRun := flags.Bool("dry-run", false, "reconstruct and hash without persisting")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("usage: augr-dataset-compose [--input path|-] [--dry-run]")
	}
	databaseURL := firstSet(os.Getenv("DB_URL"), os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("augr-dataset-compose: DB_URL or DATABASE_URL is required in the environment")
	}
	var input composeInput
	if err := decodeInput(*inputPath, stdin, &input); err != nil {
		return err
	}
	if err := validateInput(input); err != nil {
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
		return fmt.Errorf("augr-dataset-compose: schema version %d does not match required version %d", version, postgresrepo.RequiredSchemaVersion)
	}
	repo := postgresrepo.NewDatasetRepo(db.Pool)
	inputs := make([]*dataset.BoundMarketDataset, 0, len(input.ManifestIDs))
	for _, id := range input.ManifestIDs {
		value, err := repo.LoadBoundMarketDataset(ctx, id)
		if err != nil {
			return fmt.Errorf("augr-dataset-compose: load source manifest %s: %w", id, err)
		}
		inputs = append(inputs, value)
	}
	bound, err := dataset.ComposeBoundMarketDatasets(inputs, input.DecisionCutoff.UTC().Truncate(time.Microsecond))
	if err != nil {
		return err
	}
	if len(bound.Payloads()) != input.ExpectedPayloadCount || len(bound.Manifest().Partitions()) != input.ExpectedPartitionCount {
		return fmt.Errorf("augr-dataset-compose: reconstructed counts payloads=%d partitions=%d differ from expected payloads=%d partitions=%d", len(bound.Payloads()), len(bound.Manifest().Partitions()), input.ExpectedPayloadCount, input.ExpectedPartitionCount)
	}
	summary := composeSummary{
		DryRun: *dryRun, ManifestID: bound.Manifest().ID(), ManifestSHA256: bound.Manifest().Digest(),
		SourceManifestIDs: append([]uuid.UUID(nil), input.ManifestIDs...), PayloadCount: len(bound.Payloads()), PartitionCount: len(bound.Manifest().Partitions()),
	}
	for _, payload := range bound.Payloads() {
		summary.PayloadSHA256 = append(summary.PayloadSHA256, payload.Digest())
	}
	sort.Slice(summary.SourceManifestIDs, func(i, j int) bool {
		return summary.SourceManifestIDs[i].String() < summary.SourceManifestIDs[j].String()
	})
	sort.Strings(summary.PayloadSHA256)
	if !*dryRun {
		if _, err := repo.RecordBoundMarketDataset(ctx, bound, time.Now().UTC().Truncate(time.Microsecond)); err != nil {
			return fmt.Errorf("augr-dataset-compose: persist composition: %w", err)
		}
	}
	return json.NewEncoder(stdout).Encode(summary)
}

func validateInput(input composeInput) error {
	if len(input.ManifestIDs) < 2 || input.ExpectedPayloadCount <= 0 || input.ExpectedPartitionCount <= 0 ||
		input.DecisionCutoff.IsZero() || input.DecisionCutoff.Location() != time.UTC || !input.DecisionCutoff.Equal(input.DecisionCutoff.Truncate(time.Microsecond)) {
		return errors.New("augr-dataset-compose: explicit source manifests, UTC microsecond cutoff, and positive expected counts are required")
	}
	seen := make(map[uuid.UUID]struct{}, len(input.ManifestIDs))
	for _, id := range input.ManifestIDs {
		if id == uuid.Nil {
			return errors.New("augr-dataset-compose: source manifest ID is required")
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("augr-dataset-compose: source manifest %s is duplicated", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func decodeInput(path string, stdin io.Reader, target any) error {
	reader := stdin
	var file *os.File
	if path != "-" {
		var err error
		file, err = os.Open(path)
		if err != nil {
			return fmt.Errorf("augr-dataset-compose: open input: %w", err)
		}
		defer file.Close()
		reader = file
	}
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("augr-dataset-compose: decode input: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("augr-dataset-compose: input must contain exactly one JSON value")
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
