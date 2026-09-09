// Command augr-reference-import validates and registers an explicit immutable
// reference graph. It never activates a strategy, account, or order writer.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
	postgresrepo "github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
	"github.com/PatrickFanella/get-rich-quick/internal/simulation"
)

type sourceReceipt struct {
	URL        string    `json:"url"`
	SHA256     string    `json:"sha256"`
	Body       []byte    `json:"body"`
	ObservedAt time.Time `json:"observed_at"`
}

type referenceBundle struct {
	Schema     string                    `json:"schema"`
	Sources    []sourceReceipt           `json:"sources"`
	Instrument instrument.Instrument     `json:"instrument"`
	Alias      instrument.AliasEvent     `json:"alias"`
	Contract   instrument.VenueContract  `json:"contract"`
	Policy     simulation.PolicyArtifact `json:"policy"`
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, os.Args[1:], os.Stdin, os.Stdout)
	cancel()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	flags := flag.NewFlagSet("augr-reference-import", flag.ContinueOnError)
	flags.SetOutput(stdout)
	apply := flags.Bool("apply", false, "register validated graph; default validates without connecting to a database")
	version := flags.Bool("version", false, "print bundle schema version")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(stdout, "Usage: augr-reference-import [--apply] < bundle.json\nValidates exact source hashes and immutable graph. DB_URL is required only with --apply.\nRegistration is idempotent but not an atomic multi-row transaction; preserve input for retry.\nNo account, strategy, quote, or execution rows are changed.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments; supply bundle JSON on stdin")
	}
	if *version {
		_, err := fmt.Fprintln(stdout, "augr-reference-bundle-v1")
		return err
	}
	var bundle referenceBundle
	decoder := json.NewDecoder(io.LimitReader(stdin, 32<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return fmt.Errorf("decode reference bundle: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("reference bundle must contain exactly one JSON object")
	}
	if err := bundle.validate(time.Now().UTC()); err != nil {
		return err
	}
	if *apply {
		dsn := os.Getenv("DB_URL")
		if dsn == "" {
			return errors.New("DB_URL is required with --apply")
		}
		db, err := postgresrepo.NewDB(ctx, dsn)
		if err != nil {
			return errors.New("reference database connection failed; verify DB_URL and database availability")
		}
		defer db.Close()
		refs := postgresrepo.NewInstrumentRepo(db.Pool)
		retained, err := refs.CreateInstrument(ctx, &bundle.Instrument)
		if err != nil {
			return fmt.Errorf("register instrument: %w", err)
		}
		if retained.ID != bundle.Instrument.ID {
			return errors.New("existing instrument has another ID; no dependent rows registered")
		}
		if _, err := refs.AppendAliasEvent(ctx, &bundle.Alias); err != nil {
			return fmt.Errorf("register alias: %w", err)
		}
		if _, err := refs.RegisterVenueContract(ctx, &bundle.Contract); err != nil {
			return fmt.Errorf("register contract: %w", err)
		}
		if _, err := postgresrepo.NewSimulationPolicyRepo(db.Pool).RegisterSimulationPolicy(ctx, &bundle.Policy); err != nil {
			return fmt.Errorf("register policy: %w", err)
		}
	}
	return json.NewEncoder(stdout).Encode(map[string]any{"applied": *apply, "instrument_id": bundle.Instrument.ID, "venue_contract_id": bundle.Contract.ID, "simulation_policy_version": bundle.Policy.Version, "source_count": len(bundle.Sources)})
}

func (bundle referenceBundle) validate(now time.Time) error {
	if bundle.Schema != "augr-reference-bundle-v1" {
		return errors.New("unsupported reference bundle schema")
	}
	if len(bundle.Sources) == 0 {
		return errors.New("source receipts are required")
	}
	for _, source := range bundle.Sources {
		digest := sha256.Sum256(source.Body)
		if source.URL == "" || len(source.Body) == 0 || hex.EncodeToString(digest[:]) != source.SHA256 || source.ObservedAt.IsZero() || source.ObservedAt.After(now) {
			return errors.New("invalid source receipt identity, digest, or observation time")
		}
		if source.ObservedAt.After(bundle.Instrument.CreatedAt) || source.ObservedAt.After(bundle.Contract.CreatedAt) || source.ObservedAt.After(bundle.Policy.CreatedAt) || source.ObservedAt.After(bundle.Alias.CreatedAt) {
			return errors.New("reference graph predates retained source evidence")
		}
	}
	for _, created := range []time.Time{bundle.Instrument.CreatedAt, bundle.Alias.CreatedAt, bundle.Contract.CreatedAt, bundle.Policy.CreatedAt} {
		if created.After(now) {
			return errors.New("reference creation time is in the future")
		}
	}
	if err := bundle.Instrument.Validate(); err != nil {
		return fmt.Errorf("instrument: %w", err)
	}
	if err := bundle.Alias.Validate(); err != nil {
		return fmt.Errorf("alias: %w", err)
	}
	if err := bundle.Contract.Validate(); err != nil {
		return fmt.Errorf("contract: %w", err)
	}
	policy, err := simulation.PolicyFromArtifact(bundle.Policy)
	if err != nil {
		return fmt.Errorf("policy: %w", err)
	}
	if bundle.Alias.InstrumentID != bundle.Instrument.ID || bundle.Contract.InstrumentID != bundle.Instrument.ID {
		return errors.New("reference graph instrument IDs disagree")
	}
	if bundle.Alias.Action != instrument.AliasAssigned || bundle.Alias.AliasType != instrument.AliasTicker {
		return errors.New("bundle requires an assigned ticker alias")
	}
	if bundle.Contract.Currency != bundle.Instrument.Currency || bundle.Contract.SettlementMethod != bundle.Instrument.SettlementMethod {
		return errors.New("contract currency or settlement differs from instrument")
	}
	if _, ok := policy.AssetPolicy(bundle.Instrument.AssetClass); !ok {
		return errors.New("policy has no explicit instrument asset class")
	}
	return nil
}
