package postgres

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/capital"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

type CanonicalExperimentCapitalStateSource struct {
	pool     *pgxpool.Pool
	attestor ProjectionCheckpointAttestor
	maxAge   time.Duration
}

var _ ExperimentCapitalStateSource = (*CanonicalExperimentCapitalStateSource)(nil)

func NewCanonicalExperimentCapitalStateSource(pool *pgxpool.Pool, attestor ProjectionCheckpointAttestor, maxAge time.Duration) (*CanonicalExperimentCapitalStateSource, error) {
	if pool == nil || attestor.KeyID == "" || len(attestor.Secret) != sha256.Size || maxAge <= 0 {
		return nil, fmt.Errorf("postgres: canonical experiment capital source requires database, attestor, and positive freshness")
	}
	attestor.Secret = append([]byte(nil), attestor.Secret...)
	return &CanonicalExperimentCapitalStateSource{pool: pool, attestor: attestor, maxAge: maxAge}, nil
}

func (source *CanonicalExperimentCapitalStateSource) LoadExperimentCapitalState(
	ctx context.Context,
	experiment *strategycatalog.Experiment,
	account *domain.Account,
	binding *capital.Binding,
	policy *capital.Policy,
) (*capital.State, error) {
	if source == nil || source.pool == nil || experiment == nil || account == nil || binding == nil || policy == nil ||
		experiment.AccountID() != account.ID || experiment.CapitalBindingID() != binding.ID || experiment.CapitalPolicyVersion() != policy.Version() {
		return nil, fmt.Errorf("postgres: canonical experiment capital request does not reconstruct")
	}
	return source.loadAccountCapitalState(ctx, account, binding, policy)
}

// LoadInternalAccountCapitalState resolves the account and its immutable policy
// binding independently before deriving capital from an attested checkpoint.
// It never consults a broker balance or creates an experiment as a side effect.
func (source *CanonicalExperimentCapitalStateSource) LoadInternalAccountCapitalState(
	ctx context.Context,
	accountID uuid.UUID,
) (*capital.State, error) {
	if source == nil || source.pool == nil || accountID == uuid.Nil {
		return nil, fmt.Errorf("postgres: internal capital account source is required")
	}
	account, err := NewAccountRepo(source.pool).GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account.Venue != "internal" || account.ExternalAccountID != "" ||
		account.Environment != domain.AccountEnvironmentPaperScored || account.Status != domain.AccountStatusActive {
		return nil, fmt.Errorf("postgres: internal capital requires an active internal paper_scored account")
	}
	policyRepo := NewCapitalPolicyRepo(source.pool)
	binding, err := policyRepo.GetCapitalBinding(ctx, accountID)
	if err != nil {
		return nil, err
	}
	artifact, err := policyRepo.GetCapitalPolicyByVersion(ctx, binding.PolicyVersion)
	if err != nil {
		return nil, err
	}
	policy, err := capital.PolicyFromArtifact(*artifact)
	if err != nil {
		return nil, err
	}
	return source.loadAccountCapitalState(ctx, account, binding, policy)
}

func (source *CanonicalExperimentCapitalStateSource) loadAccountCapitalState(
	ctx context.Context,
	account *domain.Account,
	binding *capital.Binding,
	policy *capital.Policy,
) (*capital.State, error) {
	if err := binding.Validate(*account, policy); err != nil {
		return nil, fmt.Errorf("postgres: capital account binding does not reconstruct: %w", err)
	}
	var checkpointID uuid.UUID
	err := source.pool.QueryRow(ctx, `SELECT id FROM projection_checkpoints
		WHERE account_id=$1 AND projection_type=$2 AND projection_version=$3
			AND as_of<=clock_timestamp() AND as_of>=clock_timestamp()-($4 * interval '1 microsecond')
		ORDER BY as_of DESC,created_at DESC,id DESC LIMIT 1`,
		account.ID, ledger.PortfolioProjectionType, ledger.PortfolioProjectionVersion, source.maxAge.Microseconds()).Scan(&checkpointID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: select fresh canonical experiment checkpoint: %w", err)
	}
	checkpoint, err := NewProjectionRepo(source.pool, source.attestor).GetProjectionCheckpointByID(ctx, checkpointID)
	if err != nil {
		return nil, fmt.Errorf("postgres: load canonical experiment checkpoint: %w", err)
	}
	if err := source.verifyCheckpointAttestation(checkpoint); err != nil {
		return nil, err
	}
	valuation, err := ledger.DecodeProjectionValuation(checkpoint)
	if err != nil {
		return nil, fmt.Errorf("postgres: decode canonical experiment valuation: %w", err)
	}
	projection := &ledger.PortfolioProjection{
		CheckpointID: checkpoint.ID, ProjectionType: checkpoint.ProjectionType, Version: checkpoint.ProjectionVersion,
		FIFO: checkpoint.FIFO, AccountID: checkpoint.AccountID, BaseCurrency: checkpoint.BaseCurrency, AsOf: checkpoint.AsOf,
		MarkSource: checkpoint.MarkSource, MarkNamespace: checkpoint.MarkNamespace, MaxMarkAge: checkpoint.MaxMarkAge,
		ThroughTransactionID: checkpoint.ThroughTransactionID, TransactionCount: checkpoint.TransactionCount,
		InputChecksum: checkpoint.InputChecksum, OutputChecksum: checkpoint.OutputChecksum,
		Positions: valuation.Positions, Totals: valuation.Totals, PayloadBytes: append([]byte(nil), checkpoint.PayloadBytes...),
	}
	instrumentRepo := NewInstrumentRepo(source.pool)
	instruments := make([]instrument.Instrument, 0, len(valuation.Positions))
	for _, position := range valuation.Positions {
		if !position.Open {
			continue
		}
		value, err := instrumentRepo.GetInstrumentByID(ctx, position.InstrumentID)
		if err != nil {
			return nil, fmt.Errorf("postgres: load canonical experiment exposure instrument: %w", err)
		}
		instruments = append(instruments, *value)
	}
	state, err := capital.StateFromProjection(*account, *binding, policy, projection, instruments)
	if err != nil {
		return nil, fmt.Errorf("postgres: derive canonical experiment capital state: %w", err)
	}
	return state, nil
}

func (source *CanonicalExperimentCapitalStateSource) verifyCheckpointAttestation(checkpoint *ledger.ProjectionCheckpoint) error {
	if checkpoint == nil || checkpoint.AttestationKeyID != source.attestor.KeyID || len(checkpoint.AttestationHMAC) != sha256.Size {
		return fmt.Errorf("postgres: canonical experiment checkpoint attestation is missing or uses another key")
	}
	mac := hmac.New(sha256.New, source.attestor.Secret)
	_, _ = mac.Write([]byte(projectionCheckpointHMACDomain))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(source.attestor.KeyID))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(checkpoint.PayloadBytes)
	if !hmac.Equal(mac.Sum(nil), checkpoint.AttestationHMAC) || len(checkpoint.PayloadBytes) == 0 {
		return fmt.Errorf("postgres: canonical experiment checkpoint attestation does not verify")
	}
	return nil
}
