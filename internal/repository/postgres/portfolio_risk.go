package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
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
	if repo == nil || repo.pool == nil || repo.accountID == uuid.Nil {
		return snapshot, fmt.Errorf("postgres: canonical portfolio account snapshot source is required")
	}
	account, err := NewAccountRepo(repo.pool).GetByID(ctx, repo.accountID)
	if err != nil {
		return snapshot, fmt.Errorf("postgres: load portfolio account identity: %w", err)
	}
	if string(account.Environment) != "paper_scored" || string(account.Status) != "active" {
		return snapshot, fmt.Errorf("postgres: canonical portfolio account is not active paper_scored")
	}
	if string(account.Venue) != "alpaca" || account.ExternalAccountID == "" {
		return snapshot, fmt.Errorf("postgres: portfolio account requires a supported account-specific snapshot source")
	}
	if repo.source == nil {
		return snapshot, fmt.Errorf("postgres: canonical portfolio account snapshot source is required")
	}
	balance, err := repo.source.GetAccountBalance(ctx)
	if err != nil {
		return snapshot, err
	}
	if balance.Equity <= 0 || balance.BuyingPower < 0 || balance.OptionsBuyingPower < 0 {
		return snapshot, fmt.Errorf("postgres: canonical portfolio account balance is invalid")
	}
	environment, externalID := string(account.Environment), account.ExternalAccountID
	observedAt := databaseNow()
	canonical := accountSnapshotCanonical{
		Schema: "portfolio-account-snapshot-v1", AccountID: repo.accountID.String(), Environment: environment, ExternalAccountID: externalID,
		ObservedAt: observedAt.Format("2006-01-02T15:04:05.000000Z"), Equity: fmt.Sprintf("%.8f", balance.Equity), BuyingPower: fmt.Sprintf("%.8f", balance.BuyingPower), OptionsBuyingPower: fmt.Sprintf("%.8f", balance.OptionsBuyingPower), FallbackUsed: false,
	}
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

func (repo *PortfolioRiskRepo) LoadPortfolioRiskState(ctx context.Context, accountSnapshotID uuid.UUID, asOf time.Time) (portfolio.RuntimeRiskState, error) {
	state := portfolio.RuntimeRiskState{UnderlyingRisk: map[string]float64{}}
	if repo == nil || repo.pool == nil || repo.accountID == uuid.Nil || accountSnapshotID == uuid.Nil || asOf.IsZero() {
		return state, fmt.Errorf("postgres: portfolio risk state identity is incomplete")
	}
	var currentEquity, dayOpeningEquity, peakEquity float64
	err := repo.pool.QueryRow(ctx, `SELECT current.equity::double precision,
		COALESCE((SELECT equity::double precision FROM portfolio_account_snapshots day_open
			WHERE day_open.account_id=current.account_id AND (day_open.observed_at AT TIME ZONE 'America/New_York')::date=(current.observed_at AT TIME ZONE 'America/New_York')::date
			ORDER BY day_open.observed_at,day_open.id LIMIT 1),current.equity::double precision),
		COALESCE((SELECT max(equity)::double precision FROM portfolio_account_snapshots peak WHERE peak.account_id=current.account_id AND peak.observed_at<=current.observed_at),current.equity::double precision)
		FROM portfolio_account_snapshots current WHERE current.id=$1 AND current.account_id=$2`, accountSnapshotID, repo.accountID).
		Scan(&currentEquity, &dayOpeningEquity, &peakEquity)
	if err != nil || currentEquity <= 0 || dayOpeningEquity <= 0 || peakEquity <= 0 {
		return state, fmt.Errorf("postgres: load canonical equity history: %w", err)
	}
	state.DailyLossPct = math.Max(0, (dayOpeningEquity-currentEquity)/dayOpeningEquity)
	state.DrawdownPct = math.Max(0, (peakEquity-currentEquity)/peakEquity)
	if err = repo.pool.QueryRow(ctx, `SELECT count(*) FROM allocation_decisions WHERE account_id=$1
		AND (created_at AT TIME ZONE 'America/New_York')::date=($2::timestamptz AT TIME ZONE 'America/New_York')::date
		AND action IN ('shadow_selected','paper_order_intent','executed')`, repo.accountID, asOf).Scan(&state.NewOrdersToday); err != nil {
		return state, fmt.Errorf("postgres: count daily allocation selections: %w", err)
	}
	if err = repo.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM risk_breaker_state WHERE reset_at IS NULL)`).Scan(&state.CircuitBreakerOpen); err != nil {
		return state, fmt.Errorf("postgres: load risk breaker state: %w", err)
	}
	var reconciliationID uuid.UUID
	var reconciliationClean bool
	var reconciliationAt time.Time
	err = repo.pool.QueryRow(ctx, `SELECT run.id,run.clean,run.created_at FROM venue_reconciliation_runs run
		JOIN venue_local_snapshots snapshot ON snapshot.id=run.local_snapshot_id
		WHERE snapshot.account_id=$1 AND snapshot.provider='alpaca'
		ORDER BY run.created_at DESC,run.id DESC LIMIT 1`, repo.accountID).Scan(&reconciliationID, &reconciliationClean, &reconciliationAt)
	if err != nil {
		return state, fmt.Errorf("postgres: load latest Alpaca reconciliation: %w", err)
	}
	if reconciliationID == uuid.Nil || !reconciliationClean {
		return state, fmt.Errorf("postgres: latest Alpaca reconciliation is not clean")
	}
	policy, policyErr := portfolio.ReviewedPortfolioRiskPolicyV1()
	if policyErr != nil || reconciliationAt.After(asOf) || asOf.Sub(reconciliationAt) > policy.MaxReconciliationAge() {
		return state, fmt.Errorf("postgres: latest Alpaca reconciliation is stale")
	}
	state.ReconciliationID = reconciliationID.String()
	rows, err := repo.pool.Query(ctx, `SELECT upper(opportunity.ticker),sum(decision.reserved_risk_usd)::double precision
		FROM allocation_decisions decision
		JOIN portfolio_opportunities opportunity ON opportunity.id=decision.opportunity_id AND opportunity.account_id=decision.account_id AND opportunity.market_type='options'
		JOIN orders opening_order ON opening_order.id=decision.created_order_id AND opening_order.account_id=decision.account_id AND opening_order.leg_group_id IS NOT NULL
		WHERE decision.account_id=$1 AND decision.action IN ('paper_order_intent','executed')
		AND opening_order.status NOT IN ('rejected','cancelled')
		AND EXISTS(SELECT 1 FROM positions position WHERE position.account_id=decision.account_id AND position.leg_group_id=opening_order.leg_group_id AND position.closed_at IS NULL)
		GROUP BY upper(opportunity.ticker)`, repo.accountID)
	if err != nil {
		return state, fmt.Errorf("postgres: load open options reserved risk: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var underlying string
		var reserved float64
		if err := rows.Scan(&underlying, &reserved); err != nil {
			return state, err
		}
		underlying = strings.ToUpper(strings.TrimSpace(underlying))
		if underlying == "" || reserved <= 0 {
			return state, fmt.Errorf("postgres: open options reserved risk is invalid")
		}
		state.UnderlyingRisk[underlying] = reserved
	}
	if err := rows.Err(); err != nil {
		return state, err
	}
	return state, nil
}

func digestBytes(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
