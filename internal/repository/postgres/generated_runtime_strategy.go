package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/economicid"
	"github.com/PatrickFanella/get-rich-quick/internal/generativestrategy"
)

func (r *GenerativeStrategyRepo) generatedRuntimeBinding(ctx context.Context, sourceVersionID uuid.UUID, expectedEdge, confidence string) (*generativestrategy.RuntimeBinding, error) {
	var specID uuid.UUID
	if err := r.pool.QueryRow(ctx, `SELECT spec_id FROM generated_strategy_compilation_receipts WHERE version_id=$1`, sourceVersionID).Scan(&specID); err != nil {
		return nil, fmt.Errorf("postgres: load generated runtime compilation identity: %w", err)
	}
	spec, version, _, err := r.GetCompilation(ctx, specID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reconstruct generated runtime compilation: %w", err)
	}
	if version.ID() != sourceVersionID || version.CompilerKind() != generativestrategy.CompilerKindV1 || version.ConfigSchema() != generativestrategy.ConfigSchemaV1 {
		return nil, fmt.Errorf("postgres: generated runtime source version is not executable")
	}
	return generativestrategy.NewRuntimeBinding(spec, version, expectedEdge, confidence)
}

func ensureGeneratedRuntimeStrategyTx(
	ctx context.Context,
	tx pgx.Tx,
	accountID, manifestID uuid.UUID,
	binding *generativestrategy.RuntimeBinding,
) (*domain.Strategy, error) {
	if tx == nil || accountID == uuid.Nil || manifestID == uuid.Nil || binding == nil || binding.Spec() == nil {
		return nil, fmt.Errorf("postgres: generated runtime draft requires account, manifest, and binding")
	}
	universe := binding.Spec().Universe()
	if len(universe.Instruments) != 1 {
		return nil, fmt.Errorf("postgres: generated runtime draft requires one target instrument")
	}
	var ticker string
	var tickerCount int
	if err := tx.QueryRow(ctx, `SELECT count(DISTINCT contract.contract_id),COALESCE(min(contract.contract_id),'')
		FROM dataset_manifest_payload_bindings manifest_binding
		JOIN dataset_market_payloads payload ON payload.id=manifest_binding.payload_id
		JOIN venue_contracts contract ON contract.instrument_id=payload.instrument_id AND contract.venue=payload.provider
		WHERE manifest_binding.manifest_id=$1 AND payload.instrument_id=$2 AND payload.payload_kind='stock_bar'
		  AND contract.valid_to IS NULL`, manifestID, universe.Instruments[0]).Scan(&tickerCount, &ticker); err != nil {
		return nil, fmt.Errorf("postgres: resolve generated runtime venue contract: %w", err)
	}
	if tickerCount != 1 || strings.TrimSpace(ticker) == "" {
		return nil, fmt.Errorf("postgres: generated runtime target resolves to %d current venue contracts", tickerCount)
	}
	config, err := json.Marshal(map[string]json.RawMessage{"generated_strategy": binding.CanonicalBytes()})
	if err != nil {
		return nil, fmt.Errorf("postgres: encode generated runtime configuration: %w", err)
	}
	strategyID := economicid.DeterministicUUID("generated-runtime-strategy", accountID.String(), binding.SourceVersionID().String())
	name := "generated/" + binding.Spec().SpecKey()
	result, err := tx.Exec(ctx, `INSERT INTO strategies(
		id,name,description,ticker,market_type,schedule_cron,config,status,skip_next_run,is_paper,is_active,execution_strategy_version_id)
		VALUES($1,$2,$3,$4,$5,'',$6::jsonb,$7,false,true,false,$8)
		ON CONFLICT(id) DO NOTHING`, strategyID, name,
		"Deterministic typed strategy awaiting an authoritative promotion activation.", ticker, domain.MarketTypeStock,
		string(config), domain.StrategyStatusInactive, binding.SourceVersionID())
	if err != nil {
		return nil, fmt.Errorf("postgres: create generated runtime draft: %w", err)
	}
	if result.RowsAffected() == 0 {
		var storedName, storedTicker, storedStatus, storedSchedule string
		var storedConfig []byte
		var storedMarket domain.MarketType
		var storedPaper, storedActive bool
		var storedVersion uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT name,ticker,market_type,schedule_cron,config,status,is_paper,is_active,execution_strategy_version_id
			FROM strategies WHERE id=$1 FOR UPDATE`, strategyID).Scan(&storedName, &storedTicker, &storedMarket, &storedSchedule, &storedConfig, &storedStatus, &storedPaper, &storedActive, &storedVersion); err != nil {
			return nil, fmt.Errorf("postgres: load generated runtime draft retry: %w", err)
		}
		var expectedObject, storedObject any
		_ = json.Unmarshal(config, &expectedObject)
		_ = json.Unmarshal(storedConfig, &storedObject)
		expectedCanonical, _ := json.Marshal(expectedObject)
		storedCanonical, _ := json.Marshal(storedObject)
		if storedName != name || storedTicker != ticker || storedMarket.Normalize() != domain.MarketTypeStock || storedSchedule != "" ||
			storedStatus != domain.StrategyStatusInactive || !storedPaper || storedActive || storedVersion != binding.SourceVersionID() || !bytes.Equal(expectedCanonical, storedCanonical) {
			return nil, fmt.Errorf("postgres: generated runtime draft changed on retry")
		}
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM strategies WHERE execution_strategy_version_id=$1`, binding.SourceVersionID()).Scan(&count); err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, fmt.Errorf("postgres: generated runtime source version resolves to %d drafts", count)
	}
	strategy, err := scanStrategy(tx.QueryRow(ctx, `SELECT id,name,description,ticker,market_type,schedule_cron,config,status,skip_next_run,is_paper,created_at,updated_at,execution_strategy_version_id
		FROM strategies WHERE id=$1`, strategyID))
	if err != nil {
		return nil, err
	}
	return strategy, nil
}
