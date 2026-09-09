// Command augr-reference-export retains one provider reference response without
// creating canonical instruments or claiming verified execution mechanics.
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
	"path/filepath"

	"github.com/PatrickFanella/get-rich-quick/internal/data/polygon"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, os.Args[1:], os.Stdout)
	cancel()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		_, err := fmt.Fprintln(output, "Usage: augr-reference-export --ticker SYMBOL --date YYYY-MM-DD --output NEW_DIRECTORY\nRequires POLYGON_API_KEY in the environment. Parent directory must exist. Retains unqualified reference evidence only; never overwrites an existing directory.")
		return err
	}
	if len(args) == 1 && args[0] == "--version" {
		_, err := fmt.Fprintln(output, "augr-reference-export schema augr-provider-reference-v1")
		return err
	}
	flags := flag.NewFlagSet("augr-reference-export", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	ticker := flags.String("ticker", "", "exact provider ticker")
	date := flags.String("date", "", "provider as-of date YYYY-MM-DD")
	directory := flags.String("output", "", "new private evidence directory; parent must exist")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *ticker == "" || *date == "" || *directory == "" {
		return errors.New("usage: augr-reference-export --ticker SYMBOL --date YYYY-MM-DD --output NEW_DIRECTORY")
	}
	key := os.Getenv("POLYGON_API_KEY")
	if key == "" {
		return errors.New("POLYGON_API_KEY is required")
	}
	if _, err := os.Lstat(*directory); !errors.Is(err, os.ErrNotExist) {
		return errors.New("reference output must not already exist")
	}
	evidence, err := polygon.NewClient(key, nil).GetTickerReference(ctx, *ticker, *date)
	if err != nil {
		return err
	}
	if err := saveEvidence(*directory, evidence); err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(map[string]string{"directory": *directory, "response_sha256": evidence.ResponseSHA256, "status": "retained_reference_only"})
}

// saveEvidence creates a new private directory; no existing evidence is replaced.
// Partial failures are retained without a manifest and are not complete exports.
func saveEvidence(directory string, evidence *polygon.TickerReferenceEvidence) error {
	if evidence == nil || evidence.ObservedAt.IsZero() || len(evidence.RawResponse) == 0 {
		return errors.New("reference evidence is incomplete")
	}
	digest := sha256.Sum256(evidence.RawResponse)
	if hex.EncodeToString(digest[:]) != evidence.ResponseSHA256 {
		return errors.New("reference response hash mismatch")
	}
	metadata := *evidence
	metadata.RawResponse = nil
	manifest, err := json.Marshal(struct {
		Schema       string                          `json:"schema"`
		Status       string                          `json:"status"`
		ResponseFile string                          `json:"response_file"`
		Evidence     polygon.TickerReferenceEvidence `json:"evidence"`
	}{"augr-provider-reference-v1", "unqualified_reference", "response.json", metadata})
	if err != nil {
		return err
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		return fmt.Errorf("create reference directory: %w", err)
	}
	for _, file := range []struct {
		name  string
		bytes []byte
	}{{"response.json", evidence.RawResponse}, {"manifest.json", manifest}} {
		if err := writeExclusive(filepath.Join(directory, file.name), file.bytes); err != nil {
			return err
		}
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	return errors.Join(syncErr, closeErr)
}

func writeExclusive(path string, body []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(body)
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(writeErr, syncErr, closeErr)
}
