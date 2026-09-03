package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/economicid"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/portfolio"
)

type PortfolioRiskRepo struct {
	pool      *pgxpool.Pool
	accountID uuid.UUID
	source    balanceSource
}

func NewPortfolioRiskRepo(pool *pgxpool.Pool, accountID uuid.UUID, source ...balanceSource) *PortfolioRiskRepo {
	repo := &PortfolioRiskRepo{pool: pool, accountID: accountID}
	if len(source) != 0 {
		repo.source = source[0]
	}
	return repo
}

func (repo *PortfolioRiskRepo) RegisterPolicy(ctx context.Context, policy *portfolio.PortfolioRiskPolicy) error {
	if repo == nil || repo.pool == nil || policy == nil {
		return fmt.Errorf("postgres: portfolio risk policy is required")
	}
	var envelope struct {
		Schema  string `json:"schema"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(policy.CanonicalBytes(), &envelope); err != nil {
		return err
	}
	_, err := repo.pool.Exec(ctx, `INSERT INTO portfolio_risk_policy_artifacts(id,schema_name,version,sha256,canonical_bytes,canonical_json,created_at)
		VALUES($1,$2,$3,$4,$5,convert_from($5,'UTF8')::jsonb,$6) ON CONFLICT(id) DO NOTHING`, policy.ID(), envelope.Schema,
		envelope.Version, policy.Digest(), policy.CanonicalBytes(), databaseNow())
	return err
}

type riskBindingCanonical struct {
	Schema           string `json:"schema"`
	AccountID        string `json:"account_id"`
	CapitalBindingID string `json:"capital_binding_id"`
	PolicyID         string `json:"policy_id"`
	PolicySHA256     string `json:"policy_sha256"`
	EffectiveAt      string `json:"effective_at"`
}

func (repo *PortfolioRiskRepo) BindPolicy(ctx context.Context, capitalBindingID uuid.UUID, policy *portfolio.PortfolioRiskPolicy, effectiveAt time.Time) (uuid.UUID, error) {
	if capitalBindingID == uuid.Nil || policy == nil || effectiveAt.Location() != time.UTC {
		return uuid.Nil, fmt.Errorf("postgres: portfolio risk binding is invalid")
	}
	effectiveAt = effectiveAt.Truncate(time.Microsecond)
	canonical := riskBindingCanonical{Schema: "account-portfolio-risk-policy-binding-v1", AccountID: repo.accountID.String(), CapitalBindingID: capitalBindingID.String(), PolicyID: policy.ID().String(), PolicySHA256: policy.Digest(), EffectiveAt: effectiveAt.Format("2006-01-02T15:04:05.000000Z")}
	raw, _ := json.Marshal(canonical)
	digest := digestBytes(raw)
	id := economicid.DeterministicUUID("account-portfolio-risk-policy-binding", canonical.Schema+"@sha256:"+digest)
	_, err := repo.pool.Exec(ctx, `INSERT INTO account_portfolio_risk_policy_bindings(id,account_id,capital_binding_id,policy_id,policy_sha256,effective_at,sha256,canonical_bytes,canonical_json,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,convert_from($8,'UTF8')::jsonb,$9) ON CONFLICT(id) DO NOTHING`, id, repo.accountID, capitalBindingID, policy.ID(), policy.Digest(), effectiveAt, digest, raw, databaseNow())
	return id, err
}

type balanceSource interface {
	GetAccountBalance(context.Context) (execution.Balance, error)
}

type accountSnapshotCanonical struct {
	Schema             string `json:"schema"`
	AccountID          string `json:"account_id"`
	Environment        string `json:"environment"`
	ExternalAccountID  string `json:"external_account_id"`
	ObservedAt         string `json:"observed_at"`
	Equity             string `json:"equity"`
	BuyingPower        string `json:"buying_power"`
	OptionsBuyingPower string `json:"options_buying_power"`
	FallbackUsed       bool   `json:"fallback_used"`
}

func (repo *PortfolioRiskRepo) CaptureAccountSnapshot(ctx context.Context) (portfolio.AccountSnapshot, error) {
	var snapshot portfolio.AccountSnapshot
	if repo == nil || repo.pool == nil || repo.accountID == uuid.Nil || repo.source == nil {
		return snapshot, fmt.Errorf("postgres: canonical portfolio account snapshot source is required")
	}
	balance, err := repo.source.GetAccountBalance(ctx)
	if err != nil {
		return snapshot, err
	}
	if balance.Equity <= 0 || balance.BuyingPower < 0 || balance.OptionsBuyingPower < 0 {
		return snapshot, fmt.Errorf("postgres: canonical portfolio account balance is invalid")
	}
	var environment, externalID string
	if err = repo.pool.QueryRow(ctx, `SELECT environment,external_account_id FROM accounts WHERE id=$1 AND status='active'`, repo.accountID).Scan(&environment, &externalID); err != nil {
		return snapshot, err
	}
	if environment != "paper_scored" || externalID == "" {
		return snapshot, fmt.Errorf("postgres: canonical portfolio account is not active paper_scored")
	}
	observedAt := databaseNow()
	canonical := accountSnapshotCanonical{Schema: "portfolio-account-snapshot-v1", AccountID: repo.accountID.String(), Environment: environment, ExternalAccountID: externalID,
		ObservedAt: observedAt.Format("2006-01-02T15:04:05.000000Z"), Equity: fmt.Sprintf("%.8f", balance.Equity), BuyingPower: fmt.Sprintf("%.8f", balance.BuyingPower), OptionsBuyingPower: fmt.Sprintf("%.8f", balance.OptionsBuyingPower), FallbackUsed: false}
	raw, _ := json.Marshal(canonical)
	digest := digestBytes(raw)
	id := economicid.DeterministicUUID("portfolio-account-snapshot", canonical.Schema+"@sha256:"+digest)
	_, err = repo.pool.Exec(ctx, `INSERT INTO portfolio_account_snapshots(id,account_id,environment,external_account_id,observed_at,equity,buying_power,options_buying_power,fallback_used,sha256,canonical_bytes,canonical_json,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,false,$9,$10,convert_from($10,'UTF8')::jsonb,$11) ON CONFLICT(id) DO NOTHING`, id, repo.accountID, environment, externalID, observedAt, balance.Equity, balance.BuyingPower, balance.OptionsBuyingPower, digest, raw, databaseNow())
	if err != nil {
		return snapshot, err
	}
	return portfolio.AccountSnapshot{ID: id, ObservedAt: observedAt, Equity: balance.Equity, BuyingPower: balance.BuyingPower, OptionsBuyingPower: balance.OptionsBuyingPower}, nil
}

func digestBytes(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
