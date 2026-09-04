package postgres

import (
	"bytes"
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/agent/rules"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/economicid"
	"github.com/PatrickFanella/get-rich-quick/internal/optionsstrategy"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// ObservedOptionsStrategyRepo registers immutable native options versions and
// their inert runtime drafts. It cannot activate or schedule them.
type ObservedOptionsStrategyRepo struct{ pool *pgxpool.Pool }

func NewObservedOptionsStrategyRepo(pool *pgxpool.Pool) *ObservedOptionsStrategyRepo {
	return &ObservedOptionsStrategyRepo{pool: pool}
}

func (repo *ObservedOptionsStrategyRepo) RegisterCandidate(ctx context.Context, accountID, scopeID uuid.UUID, config rules.OptionsRulesConfig, sourceCommit, sourceTreeSHA256 string) (*domain.Strategy, bool, error) {
	if repo == nil || repo.pool == nil || accountID == uuid.Nil || scopeID == uuid.Nil {
		return nil, false, fmt.Errorf("postgres: observed options candidate requires database, account, and scope")
	}
	pool := repo.pool
	readiness, err := NewReportArtifactRepo(pool).DiscoveryDeploymentReadinessForScope(ctx, scopeID, accountID)
	if err != nil || !readiness.Options.Ready {
		if err != nil {
			return nil, false, fmt.Errorf("postgres: observed options scope: %w", err)
		}
		return nil, false, fmt.Errorf("postgres: observed options evidence: %s", readiness.Options.Reason)
	}
	family, version, err := optionsstrategy.Compile(config, sourceCommit, sourceTreeSHA256)
	if err != nil {
		return nil, false, err
	}
	catalog := NewStrategyCatalogRepo(pool)
	if _, err := catalog.RegisterStrategyFamily(ctx, family); err != nil {
		return nil, false, fmt.Errorf("postgres: register observed options family: %w", err)
	}
	if _, err := catalog.RegisterStrategyVersion(ctx, version); err != nil {
		return nil, false, fmt.Errorf("postgres: register observed options version: %w", err)
	}
	runtimeConfig, err := optionsstrategy.RuntimeConfig(config)
	if err != nil {
		return nil, false, err
	}
	strategyID := economicid.DeterministicUUID("observed-options-runtime-strategy", version.ID().String())
	name := "generated-options/" + config.Underlying + "/" + string(config.StrategyType)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "augr:observed-options:"+version.ID().String()); err != nil {
		return nil, false, err
	}
	result, err := tx.Exec(ctx, `INSERT INTO strategies(id,name,description,ticker,market_type,schedule_cron,config,status,skip_next_run,is_paper,is_active,execution_strategy_version_id)
		VALUES($1,$2,$3,$4,$5,'',$6::jsonb,$7,false,true,false,$8) ON CONFLICT(id) DO NOTHING`,
		strategyID, name, "Immutable observed options candidate awaiting authoritative promotion.", config.Underlying, domain.MarketTypeOptions,
		string(runtimeConfig), domain.StrategyStatusInactive, version.ID())
	if err != nil {
		return nil, false, fmt.Errorf("postgres: create observed options runtime draft: %w", err)
	}
	created := result.RowsAffected() == 1
	var storedName, ticker, schedule, status string
	var market domain.MarketType
	var storedConfig []byte
	var paper, active bool
	var storedVersion uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT name,ticker,market_type,schedule_cron,convert_to(strategy_canonical_json(config),'UTF8'),status,is_paper,is_active,execution_strategy_version_id
		FROM strategies WHERE id=$1 FOR UPDATE`, strategyID).Scan(&storedName, &ticker, &market, &schedule, &storedConfig, &status, &paper, &active, &storedVersion); err != nil {
		return nil, false, err
	}
	if storedName != name || ticker != config.Underlying || market.Normalize() != domain.MarketTypeOptions || schedule != "" || status != domain.StrategyStatusInactive || !paper || active || storedVersion != version.ID() || !bytes.Equal(storedConfig, runtimeConfig) {
		return nil, false, fmt.Errorf("postgres: observed options runtime draft changed on retry: %w", repository.ErrIdempotencyConflict)
	}
	var versionUses int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM strategies WHERE execution_strategy_version_id=$1`, version.ID()).Scan(&versionUses); err != nil || versionUses != 1 {
		return nil, false, fmt.Errorf("postgres: observed options version resolves to %d runtime drafts", versionUses)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	strategy, err := scanStrategy(pool.QueryRow(ctx, `SELECT id,name,description,ticker,market_type,schedule_cron,config,status,skip_next_run,is_paper,created_at,updated_at,execution_strategy_version_id FROM strategies WHERE id=$1`, strategyID))
	if err != nil {
		return nil, false, err
	}
	return strategy, created, nil
}
