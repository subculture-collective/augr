// Command augr-evaluation-scope evaluates explicit quality evidence and binds
// one immutable manifest to one canonical paper account and policy set.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	postgresrepo "github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
)

type instrumentWindowInput struct {
	InstrumentID   uuid.UUID  `json:"instrument_id"`
	ValidFrom      time.Time  `json:"valid_from"`
	ValidTo        *time.Time `json:"valid_to,omitempty"`
	EvidenceSHA256 string     `json:"evidence_sha256"`
}

type sessionEvidenceInput struct {
	PartitionContentSHA256 string      `json:"partition_content_sha256"`
	ExpectedEffectiveAt    []time.Time `json:"expected_effective_at"`
	EvidenceSHA256         string      `json:"evidence_sha256"`
}

type externalAssessmentInput struct {
	PartitionContentSHA256 string              `json:"partition_content_sha256"`
	Check                  dataset.CheckCode   `json:"check"`
	Status                 dataset.CheckStatus `json:"status"`
	EvidenceSHA256         string              `json:"evidence_sha256"`
}

type scopeInput struct {
	ScopeID                 uuid.UUID                 `json:"scope_id"`
	AccountID               uuid.UUID                 `json:"account_id"`
	CapitalBindingID        uuid.UUID                 `json:"capital_binding_id"`
	ManifestID              uuid.UUID                 `json:"manifest_id"`
	SimulationPolicyVersion string                    `json:"simulation_policy_version"`
	CapitalPolicyVersion    string                    `json:"capital_policy_version"`
	EvaluationStart         time.Time                 `json:"evaluation_start"`
	EvaluationEnd           time.Time                 `json:"evaluation_end"`
	InstrumentWindows       []instrumentWindowInput   `json:"instrument_windows"`
	Sessions                []sessionEvidenceInput    `json:"sessions"`
	ExternalAssessments     []externalAssessmentInput `json:"external_assessments"`
}

type scopeSummary struct {
	DryRun                 bool      `json:"dry_run"`
	ScopeID                uuid.UUID `json:"scope_id"`
	ScopeSHA256            string    `json:"scope_sha256"`
	ManifestID             uuid.UUID `json:"manifest_id"`
	ManifestSHA256         string    `json:"manifest_sha256"`
	QualityResultID        uuid.UUID `json:"quality_result_id"`
	QualitySHA256          string    `json:"quality_sha256"`
	QualityQuarantined     bool      `json:"quality_quarantined"`
	CheckCount             int       `json:"check_count"`
	FindingCount           int       `json:"finding_count"`
	SimulationPolicySHA256 string    `json:"simulation_policy_sha256"`
	CapitalPolicySHA256    string    `json:"capital_policy_sha256"`
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	flags := flag.NewFlagSet("augr-evaluation-scope", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	inputPath := flags.String("input", "-", "JSON input path, or - for stdin")
	dryRun := flags.Bool("dry-run", false, "evaluate and report without persistence")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("usage: augr-evaluation-scope [--input path|-] [--dry-run]")
	}
	databaseURL := firstSet(os.Getenv("DB_URL"), os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("augr-evaluation-scope: DB_URL or DATABASE_URL is required in the environment")
	}
	var input scopeInput
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
		return fmt.Errorf("augr-evaluation-scope: schema version %d does not match required version %d", version, postgresrepo.RequiredSchemaVersion)
	}
	datasets := postgresrepo.NewDatasetRepo(db.Pool)
	manifest, err := datasets.GetDatasetManifest(ctx, input.ManifestID)
	if err != nil {
		return fmt.Errorf("augr-evaluation-scope: load manifest: %w", err)
	}
	policy, err := dataset.NewPolicy(dataset.ReviewedPolicyV1Input())
	if err != nil {
		return err
	}
	quality, err := dataset.Evaluate(dataset.QualityInput{
		Policy: policy, Manifest: manifest, InstrumentWindows: mapInstrumentWindows(input.InstrumentWindows),
		Sessions: mapSessions(input.Sessions), ExternalAssessments: mapExternalAssessments(input.ExternalAssessments),
	})
	if err != nil {
		return fmt.Errorf("augr-evaluation-scope: evaluate quality: %w", err)
	}
	simulationArtifact, err := postgresrepo.NewSimulationPolicyRepo(db.Pool).GetSimulationPolicyByVersion(ctx, input.SimulationPolicyVersion)
	if err != nil {
		return fmt.Errorf("augr-evaluation-scope: load simulation policy: %w", err)
	}
	capitalArtifact, err := postgresrepo.NewCapitalPolicyRepo(db.Pool).GetCapitalPolicyByVersion(ctx, input.CapitalPolicyVersion)
	if err != nil {
		return fmt.Errorf("augr-evaluation-scope: load capital policy: %w", err)
	}
	binding, err := postgresrepo.NewCapitalPolicyRepo(db.Pool).GetCapitalBinding(ctx, input.AccountID)
	if err != nil {
		return fmt.Errorf("augr-evaluation-scope: load capital binding: %w", err)
	}
	account, err := postgresrepo.NewAccountRepo(db.Pool).GetByID(ctx, input.AccountID)
	if err != nil {
		return fmt.Errorf("augr-evaluation-scope: load account: %w", err)
	}
	if binding.ID != input.CapitalBindingID || binding.PolicyVersion != input.CapitalPolicyVersion || string(account.Environment) != "paper_scored" {
		return errors.New("augr-evaluation-scope: account, capital binding, policy, or paper_scored environment does not reconstruct")
	}
	scope, err := postgresrepo.NewPaperEvaluationScope(postgresrepo.PaperEvaluationScope{
		ID: input.ScopeID, AccountID: input.AccountID, CapitalBindingID: input.CapitalBindingID,
		ManifestSHA256: manifest.Digest(), QualitySHA256: quality.Digest(), SimulationPolicySHA256: simulationArtifact.SHA256,
		CapitalPolicySHA256: capitalArtifact.SHA256, EvaluationStart: input.EvaluationStart, EvaluationEnd: input.EvaluationEnd,
	})
	if err != nil {
		return fmt.Errorf("augr-evaluation-scope: construct scope: %w", err)
	}
	scope.ID = input.ScopeID
	summary := scopeSummary{
		DryRun: *dryRun, ScopeID: scope.ID, ScopeSHA256: scope.CanonicalSHA256, ManifestID: manifest.ID(), ManifestSHA256: manifest.Digest(),
		QualityResultID: quality.ID(), QualitySHA256: quality.Digest(), QualityQuarantined: quality.Quarantined(),
		CheckCount: len(quality.Checks()), FindingCount: len(quality.Findings()), SimulationPolicySHA256: simulationArtifact.SHA256, CapitalPolicySHA256: capitalArtifact.SHA256,
	}
	if quality.Quarantined() {
		if err := json.NewEncoder(stdout).Encode(summary); err != nil {
			return err
		}
		return errors.New("augr-evaluation-scope: quality result is quarantined; scope remains promotion-ineligible")
	}
	if *dryRun {
		return json.NewEncoder(stdout).Encode(summary)
	}
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	artifact, err := policy.NewArtifact(createdAt)
	if err != nil {
		return err
	}
	if _, err := datasets.RegisterDatasetPolicy(ctx, artifact); err != nil {
		return err
	}
	if _, err := datasets.RecordDatasetQualityResult(ctx, quality, createdAt); err != nil {
		return err
	}
	if err := postgresrepo.NewReportArtifactRepo(db.Pool).RegisterScope(ctx, scope); err != nil {
		return err
	}
	summary.ScopeID = scope.ID
	return json.NewEncoder(stdout).Encode(summary)
}

func validateInput(input scopeInput) error {
	if input.ScopeID == uuid.Nil || input.AccountID == uuid.Nil || input.CapitalBindingID == uuid.Nil || input.ManifestID == uuid.Nil ||
		input.SimulationPolicyVersion == "" || input.CapitalPolicyVersion == "" ||
		input.EvaluationStart.Location() != time.UTC || input.EvaluationEnd.Location() != time.UTC ||
		!input.EvaluationStart.Equal(input.EvaluationStart.Truncate(time.Microsecond)) || !input.EvaluationEnd.Equal(input.EvaluationEnd.Truncate(time.Microsecond)) ||
		!input.EvaluationStart.Before(input.EvaluationEnd) {
		return errors.New("augr-evaluation-scope: explicit scope, account, binding, manifest, policies, and canonical UTC evaluation interval are required")
	}
	return nil
}

func mapInstrumentWindows(values []instrumentWindowInput) []dataset.InstrumentWindow {
	result := make([]dataset.InstrumentWindow, len(values))
	for i, value := range values {
		result[i] = dataset.InstrumentWindow{InstrumentID: value.InstrumentID, ValidFrom: value.ValidFrom, ValidTo: value.ValidTo, EvidenceSHA256: value.EvidenceSHA256}
	}
	return result
}

func mapSessions(values []sessionEvidenceInput) []dataset.SessionEvidence {
	result := make([]dataset.SessionEvidence, len(values))
	for i, value := range values {
		result[i] = dataset.SessionEvidence{PartitionContentSHA256: value.PartitionContentSHA256, ExpectedEffectiveAt: value.ExpectedEffectiveAt, EvidenceSHA256: value.EvidenceSHA256}
	}
	return result
}

func mapExternalAssessments(values []externalAssessmentInput) []dataset.ExternalAssessment {
	result := make([]dataset.ExternalAssessment, len(values))
	for i, value := range values {
		result[i] = dataset.ExternalAssessment{PartitionContentSHA256: value.PartitionContentSHA256, Check: value.Check, Status: value.Status, EvidenceSHA256: value.EvidenceSHA256}
	}
	return result
}

func decodeInput(path string, stdin io.Reader, target any) error {
	reader := stdin
	var file *os.File
	if path != "-" {
		var err error
		file, err = os.Open(path)
		if err != nil {
			return fmt.Errorf("augr-evaluation-scope: open input: %w", err)
		}
		defer func() { _ = file.Close() }()
		reader = file
	}
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("augr-evaluation-scope: decode input: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("augr-evaluation-scope: input must contain exactly one JSON value")
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
