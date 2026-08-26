package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// PaperEvaluationScope binds paper evidence to one account, dataset, policy set, and time window.
type PaperEvaluationScope struct {
	ID                     uuid.UUID `json:"id"`
	AccountID              uuid.UUID `json:"account_id"`
	CapitalBindingID       uuid.UUID `json:"capital_binding_id"`
	ManifestSHA256         string    `json:"manifest_sha256"`
	QualitySHA256          string    `json:"quality_sha256"`
	SimulationPolicySHA256 string    `json:"simulation_policy_sha256"`
	CapitalPolicySHA256    string    `json:"capital_policy_sha256"`
	EvaluationStart        time.Time `json:"evaluation_start"`
	EvaluationEnd          time.Time `json:"evaluation_end"`
	CanonicalBytes         []byte    `json:"canonical_bytes"`
	CanonicalSHA256        string    `json:"canonical_sha256"`
	CreatedAt              time.Time `json:"created_at"`
}

// ValidateBacktestConfigScope verifies that executable config facts agree with
// the registered immutable evidence graph. It does not claim that the runtime
// data loader is bound to that graph.
func (r *ReportArtifactRepo) ValidateBacktestConfigScope(ctx context.Context, config *domain.BacktestConfig) error {
	if config == nil || config.ScopeID == nil {
		return fmt.Errorf("paper evaluation scope is required")
	}
	var start, end, cutoff, minEffective, maxEffective time.Time
	var capital float64
	var quarantined bool
	err := r.pool.QueryRow(ctx, `SELECT s.evaluation_start,s.evaluation_end,b.starting_capital,m.decision_cutoff,q.quarantined,
		min(p.effective_start),max(p.effective_end)
		FROM paper_evaluation_scopes s
		JOIN account_capital_policy_bindings b ON b.id=s.capital_binding_id AND b.account_id=s.account_id
		JOIN dataset_manifests m ON m.sha256=s.manifest_sha256
		JOIN dataset_quality_results q ON q.manifest_id=m.id AND q.sha256=s.quality_sha256
		JOIN dataset_manifest_partitions p ON p.manifest_id=m.id
		WHERE s.id=$1
		GROUP BY s.evaluation_start,s.evaluation_end,b.starting_capital,m.decision_cutoff,q.quarantined`, *config.ScopeID).Scan(
		&start, &end, &capital, &cutoff, &quarantined, &minEffective, &maxEffective)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("scope evidence graph is missing or inconsistent")
	}
	if err != nil {
		return fmt.Errorf("validate paper evaluation scope: %w", err)
	}
	if !config.StartDate.Equal(start) || !config.EndDate.Equal(end) {
		return fmt.Errorf("backtest config date range does not match scope")
	}
	if math.Abs(config.Simulation.InitialCapital-capital) > 1e-8 {
		return fmt.Errorf("backtest config initial capital does not match scope binding")
	}
	if quarantined || cutoff.Before(end) || minEffective.After(start) || maxEffective.Before(end) {
		return fmt.Errorf("scope dataset is quarantined, unavailable, or does not cover the evaluation range")
	}
	return nil
}

const DiscoveryDeploymentUnavailableReason = "historical data loader is not bound to the scope's immutable dataset manifest"

// ErrDiscoveryDeploymentImmutableBinding is the authoritative deployment lock
// while the historical loader cannot prove immutable manifest binding.
var ErrDiscoveryDeploymentImmutableBinding = repository.NewImmutableBindingLock(DiscoveryDeploymentUnavailableReason)

// DiscoveryDeploymentReadiness reports whether discovery can deploy from an
// immutable dataset manifest. Scope rows alone do not establish that binding.
func (r *ReportArtifactRepo) DiscoveryDeploymentReadiness(context.Context) (bool, string, error) {
	return false, DiscoveryDeploymentUnavailableReason, ErrDiscoveryDeploymentImmutableBinding
}

// ScopedExecutionBinding retains per-scope validation for backtest callers.
func (r *ReportArtifactRepo) ScopedExecutionBinding(context.Context, uuid.UUID) (bool, string, error) {
	return false, DiscoveryDeploymentUnavailableReason, ErrDiscoveryDeploymentImmutableBinding
}

type paperEvaluationScopeCanonical struct {
	Schema                 string    `json:"schema"`
	AccountID              uuid.UUID `json:"account_id"`
	CapitalBindingID       uuid.UUID `json:"capital_binding_id"`
	ManifestSHA256         string    `json:"manifest_sha256"`
	QualitySHA256          string    `json:"quality_sha256"`
	SimulationPolicySHA256 string    `json:"simulation_policy_sha256"`
	CapitalPolicySHA256    string    `json:"capital_policy_sha256"`
	EvaluationStart        string    `json:"evaluation_start"`
	EvaluationEnd          string    `json:"evaluation_end"`
}

// NewPaperEvaluationScope canonicalizes every scope fact and binds the capital
// policy digest through the account's immutable capital binding.
func NewPaperEvaluationScope(scope PaperEvaluationScope) (*PaperEvaluationScope, error) {
	if scope.AccountID == uuid.Nil || scope.CapitalBindingID == uuid.Nil {
		return nil, fmt.Errorf("paper evaluation scope account and capital binding are required")
	}
	if !scope.EvaluationEnd.After(scope.EvaluationStart) {
		return nil, fmt.Errorf("paper evaluation scope end must be after start")
	}
	for name, value := range map[string]string{
		"manifest": scope.ManifestSHA256, "quality": scope.QualitySHA256,
		"simulation policy": scope.SimulationPolicySHA256, "capital policy": scope.CapitalPolicySHA256,
	} {
		if len(value) != 64 {
			return nil, fmt.Errorf("paper evaluation scope %s SHA-256 is invalid", name)
		}
		if _, err := hex.DecodeString(value); err != nil {
			return nil, fmt.Errorf("paper evaluation scope %s SHA-256 is invalid", name)
		}
	}
	scope.EvaluationStart = scope.EvaluationStart.UTC().Truncate(time.Microsecond)
	scope.EvaluationEnd = scope.EvaluationEnd.UTC().Truncate(time.Microsecond)
	canonical, err := json.Marshal(paperEvaluationScopeCanonical{
		Schema: "paper-evaluation-scope-v1", AccountID: scope.AccountID, CapitalBindingID: scope.CapitalBindingID,
		ManifestSHA256: scope.ManifestSHA256, QualitySHA256: scope.QualitySHA256,
		SimulationPolicySHA256: scope.SimulationPolicySHA256, CapitalPolicySHA256: scope.CapitalPolicySHA256,
		EvaluationStart: scope.EvaluationStart.Format("2006-01-02T15:04:05.000000Z"),
		EvaluationEnd:   scope.EvaluationEnd.Format("2006-01-02T15:04:05.000000Z"),
	})
	if err != nil {
		return nil, fmt.Errorf("paper evaluation scope canonicalize: %w", err)
	}
	digest := sha256.Sum256(canonical)
	scope.CanonicalBytes = canonical
	scope.CanonicalSHA256 = hex.EncodeToString(digest[:])
	return &scope, nil
}

func (r *ReportArtifactRepo) RegisterScope(ctx context.Context, scope *PaperEvaluationScope) error {
	if scope == nil {
		return fmt.Errorf("postgres: paper evaluation scope is required")
	}
	canonical, err := NewPaperEvaluationScope(*scope)
	if err != nil {
		return fmt.Errorf("postgres: register paper evaluation scope: %w", err)
	}
	if (len(scope.CanonicalBytes) != 0 && !bytes.Equal(scope.CanonicalBytes, canonical.CanonicalBytes)) ||
		(scope.CanonicalSHA256 != "" && scope.CanonicalSHA256 != canonical.CanonicalSHA256) {
		return fmt.Errorf("postgres: register paper evaluation scope: canonical identity does not match fields")
	}
	canonical.ID = scope.ID
	*scope = *canonical
	if scope.ID == uuid.Nil {
		scope.ID = uuid.New()
	}
	row := r.pool.QueryRow(ctx, `INSERT INTO paper_evaluation_scopes
		(id,account_id,capital_binding_id,manifest_sha256,quality_sha256,simulation_policy_sha256,capital_policy_sha256,
		 evaluation_start,evaluation_end,canonical_bytes,canonical_sha256)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT(canonical_sha256) DO NOTHING RETURNING id,created_at`,
		scope.ID, scope.AccountID, scope.CapitalBindingID, scope.ManifestSHA256, scope.QualitySHA256, scope.SimulationPolicySHA256,
		scope.CapitalPolicySHA256, scope.EvaluationStart, scope.EvaluationEnd, scope.CanonicalBytes, scope.CanonicalSHA256)
	if err := row.Scan(&scope.ID, &scope.CreatedAt); err == nil {
		return nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("postgres: register paper evaluation scope: %w", err)
	}
	existing, err := r.GetScopeBySHA256(ctx, scope.CanonicalSHA256)
	if err != nil {
		return err
	}
	if existing.AccountID != scope.AccountID || existing.CapitalBindingID != scope.CapitalBindingID || existing.ManifestSHA256 != scope.ManifestSHA256 ||
		existing.QualitySHA256 != scope.QualitySHA256 || existing.SimulationPolicySHA256 != scope.SimulationPolicySHA256 ||
		existing.CapitalPolicySHA256 != scope.CapitalPolicySHA256 || !existing.EvaluationStart.Equal(scope.EvaluationStart) ||
		!existing.EvaluationEnd.Equal(scope.EvaluationEnd) || !bytes.Equal(existing.CanonicalBytes, scope.CanonicalBytes) {
		return fmt.Errorf("postgres: paper evaluation scope conflict: %w", repository.ErrIdempotencyConflict)
	}
	*scope = *existing
	return nil
}

func (r *ReportArtifactRepo) GetScopeBySHA256(ctx context.Context, sha string) (*PaperEvaluationScope, error) {
	var scope PaperEvaluationScope
	err := r.pool.QueryRow(ctx, `SELECT id,account_id,capital_binding_id,manifest_sha256,quality_sha256,simulation_policy_sha256,
		capital_policy_sha256,evaluation_start,evaluation_end,canonical_bytes,canonical_sha256,created_at
		FROM paper_evaluation_scopes WHERE canonical_sha256=$1`, sha).Scan(
		&scope.ID, &scope.AccountID, &scope.CapitalBindingID, &scope.ManifestSHA256, &scope.QualitySHA256, &scope.SimulationPolicySHA256,
		&scope.CapitalPolicySHA256, &scope.EvaluationStart, &scope.EvaluationEnd, &scope.CanonicalBytes,
		&scope.CanonicalSHA256, &scope.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("postgres: paper evaluation scope: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: get paper evaluation scope: %w", err)
	}
	return &scope, nil
}
