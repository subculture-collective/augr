package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/capital"
	"github.com/PatrickFanella/get-rich-quick/internal/economicid"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/portfolio"
)

// InternalPortfolioCapitalSnapshot is a ledger-derived capital observation,
// not a broker account snapshot or a venue reconciliation. Options margin is
// explicitly unsupported by this version.
type InternalPortfolioCapitalSnapshot struct {
	ID                     uuid.UUID
	ObservedAt             time.Time
	Equity                 decimal.Decimal
	LongBuyingPower        decimal.Decimal
	ProjectionCheckpointID uuid.UUID
}

// GetAccountBalance reads attested internal capital for diagnostics without
// creating snapshot evidence or falling back to an unrelated broker account.
func (repo *PortfolioRiskRepo) GetAccountBalance(ctx context.Context) (execution.Balance, error) {
	var balance execution.Balance
	if repo == nil || repo.internal == nil {
		return balance, fmt.Errorf("postgres: internal capital reader is required")
	}
	state, err := repo.internal.LoadInternalAccountCapitalState(ctx, repo.accountID)
	if err != nil {
		return balance, err
	}
	account, err := NewAccountRepo(repo.pool).GetByID(ctx, repo.accountID)
	if err != nil {
		return balance, err
	}
	policyRepo := NewCapitalPolicyRepo(repo.pool)
	binding, err := policyRepo.GetCapitalBinding(ctx, repo.accountID)
	if err != nil {
		return balance, err
	}
	artifact, err := policyRepo.GetCapitalPolicyByVersion(ctx, binding.PolicyVersion)
	if err != nil {
		return balance, err
	}
	policy, err := capital.PolicyFromArtifact(*artifact)
	if err != nil {
		return balance, err
	}
	capacity, err := capital.LongBuyingPower(*account, *binding, policy, state)
	if err != nil {
		return balance, err
	}
	return execution.Balance{Equity: state.Equity().InexactFloat64(), BuyingPower: capacity.Truncate(8).InexactFloat64()}, nil
}

func (repo *PortfolioRiskRepo) verifyInternalAccountConsistency(ctx context.Context, id uuid.UUID, asOf time.Time) (string, error) {
	if repo.internal == nil {
		return "", fmt.Errorf("postgres: internal consistency attestor is required")
	}
	var checkpointID uuid.UUID
	var observedAt time.Time
	if err := repo.pool.QueryRow(ctx, `SELECT projection_checkpoint_id,observed_at FROM internal_portfolio_capital_snapshots WHERE id=$1 AND account_id=$2`, id, repo.accountID).Scan(&checkpointID, &observedAt); err != nil {
		return "", err
	}
	if observedAt.After(asOf) || asOf.Sub(observedAt) > repo.internal.maxAge {
		return "", fmt.Errorf("postgres: internal consistency checkpoint is stale")
	}
	checkpoint, err := NewProjectionRepo(repo.pool, repo.internal.attestor).GetProjectionCheckpointByID(ctx, checkpointID)
	if err != nil {
		return "", err
	}
	if checkpoint.AccountID != repo.accountID || !checkpoint.AsOf.Equal(observedAt) {
		return "", fmt.Errorf("postgres: internal consistency checkpoint differs")
	}
	if err := repo.internal.verifyCheckpointAttestation(checkpoint); err != nil {
		return "", err
	}
	// The explicit type prefix prevents this identity being mistaken for an
	// external venue-reconciliation UUID. The referenced row is immutable.
	return "internal-ledger-capital/v1/" + id.String(), nil
}

func (repo *PortfolioRiskRepo) captureInternalAccountSnapshot(ctx context.Context) (portfolio.AccountSnapshot, error) {
	var result portfolio.AccountSnapshot
	observation, err := repo.internal.CaptureInternalPortfolioCapital(ctx, repo.accountID)
	if err != nil {
		return result, err
	}
	if !observation.Equity.Equal(observation.Equity.Truncate(8)) {
		return result, fmt.Errorf("postgres: internal equity exceeds allocator snapshot precision")
	}
	capacity := observation.LongBuyingPower.Truncate(8)
	canonical := struct {
		Schema                    string `json:"schema"`
		AccountID                 string `json:"account_id"`
		InternalCapitalSnapshotID string `json:"internal_capital_snapshot_id"`
		ObservedAt                string `json:"observed_at"`
		Equity                    string `json:"equity"`
		BuyingPower               string `json:"buying_power"`
		OptionsMarginSupported    bool   `json:"options_margin_supported"`
	}{
		"portfolio-account-snapshot-internal-v1", repo.accountID.String(), observation.ID.String(),
		observation.ObservedAt.Format("2006-01-02T15:04:05.000000Z"), observation.Equity.StringFixed(8), capacity.StringFixed(8), false,
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return result, err
	}
	digest := digestBytes(raw)
	id := economicid.DeterministicUUID("portfolio-account-snapshot", canonical.Schema+"@sha256:"+digest)
	_, err = repo.pool.Exec(ctx, `INSERT INTO portfolio_account_snapshots
		(id,account_id,environment,external_account_id,observed_at,equity,buying_power,options_buying_power,fallback_used,sha256,canonical_bytes,canonical_json,created_at,internal_capital_snapshot_id)
		VALUES($1,$2,'paper_scored',NULL,$3,$4,$5,0,false,$6,$7,convert_from($7,'UTF8')::jsonb,$8,$9) ON CONFLICT(id) DO NOTHING`,
		id, repo.accountID, observation.ObservedAt, observation.Equity, capacity, digest, raw, databaseNow(), observation.ID)
	if err != nil {
		return result, fmt.Errorf("postgres: bind internal account snapshot: %w", err)
	}
	return portfolio.AccountSnapshot{ID: id, ObservedAt: observation.ObservedAt, Equity: observation.Equity.InexactFloat64(), BuyingPower: capacity.InexactFloat64()}, nil
}

type internalPortfolioCapitalCanonical struct {
	Schema                 string `json:"schema"`
	AccountID              string `json:"account_id"`
	CapitalBindingID       string `json:"capital_binding_id"`
	ProjectionCheckpointID string `json:"projection_checkpoint_id"`
	ThroughTransactionID   string `json:"through_transaction_id"`
	ObservedAt             string `json:"observed_at"`
	Equity                 string `json:"equity"`
	LongBuyingPower        string `json:"long_buying_power"`
	OptionsMarginSupported bool   `json:"options_margin_supported"`
}

// CaptureInternalPortfolioCapital persists independently reconstructed capital
// from a fresh HMAC-verified projection. Replays preserve the original identity.
func (source *CanonicalExperimentCapitalStateSource) CaptureInternalPortfolioCapital(ctx context.Context, accountID uuid.UUID) (InternalPortfolioCapitalSnapshot, error) {
	var snapshot InternalPortfolioCapitalSnapshot
	state, err := source.LoadInternalAccountCapitalState(ctx, accountID)
	if err != nil {
		return snapshot, err
	}
	account, err := NewAccountRepo(source.pool).GetByID(ctx, accountID)
	if err != nil {
		return snapshot, err
	}
	policyRepo := NewCapitalPolicyRepo(source.pool)
	binding, err := policyRepo.GetCapitalBinding(ctx, accountID)
	if err != nil {
		return snapshot, err
	}
	artifact, err := policyRepo.GetCapitalPolicyByVersion(ctx, binding.PolicyVersion)
	if err != nil {
		return snapshot, err
	}
	policy, err := capital.PolicyFromArtifact(*artifact)
	if err != nil {
		return snapshot, err
	}
	capacity, err := capital.LongBuyingPower(*account, *binding, policy, state)
	if err != nil {
		return snapshot, err
	}
	checkpoint, err := NewProjectionRepo(source.pool, source.attestor).GetProjectionCheckpointByID(ctx, state.ProjectionCheckpointID())
	if err != nil {
		return snapshot, err
	}
	canonical := internalPortfolioCapitalCanonical{
		Schema: "internal-portfolio-capital-snapshot-v1", AccountID: accountID.String(), CapitalBindingID: binding.ID.String(),
		ProjectionCheckpointID: checkpoint.ID.String(), ThroughTransactionID: checkpoint.ThroughTransactionID.String(),
		ObservedAt: checkpoint.AsOf.Format("2006-01-02T15:04:05.000000Z"), Equity: state.Equity().String(), LongBuyingPower: capacity.String(),
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return snapshot, err
	}
	digest := digestBytes(raw)
	id := economicid.DeterministicUUID("internal-portfolio-capital-snapshot", digest)
	_, err = source.pool.Exec(ctx, `INSERT INTO internal_portfolio_capital_snapshots
		(id,account_id,capital_binding_id,projection_checkpoint_id,through_transaction_id,observed_at,equity,long_buying_power,sha256,canonical_bytes,canonical_json)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,convert_from($10,'UTF8')::jsonb) ON CONFLICT(id) DO NOTHING`,
		id, accountID, binding.ID, checkpoint.ID, checkpoint.ThroughTransactionID, checkpoint.AsOf, state.Equity(), capacity, digest, raw)
	if err != nil {
		return snapshot, fmt.Errorf("postgres: persist internal portfolio capital: %w", err)
	}
	var matches bool
	if err := source.pool.QueryRow(ctx, `SELECT canonical_bytes=$2 FROM internal_portfolio_capital_snapshots WHERE id=$1`, id, raw).Scan(&matches); err != nil {
		return snapshot, err
	}
	if !matches {
		return snapshot, fmt.Errorf("postgres: internal portfolio capital replay differs")
	}
	return InternalPortfolioCapitalSnapshot{ID: id, ObservedAt: checkpoint.AsOf, Equity: state.Equity(), LongBuyingPower: capacity, ProjectionCheckpointID: checkpoint.ID}, nil
}
