package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/promotion"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

var deploymentRiskPolicyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}@sha256:[0-9a-f]{64}$`)

type PromotionActivationProjection struct {
	Activation *promotion.Activation
	Changed    bool
	Reason     string
}

type PromotionActivationBatch struct {
	Eligible  int
	Activated int
	Suspended int
	Noop      int
}

// ProjectEligibleActivations evaluates every authoritative head for one exact
// canonical account and scope. A single failed projection fails the batch;
// each deployment remains independently atomic and replayable.
func (repo *PromotionRepo) ProjectEligibleActivations(ctx context.Context, accountID, scopeID uuid.UUID, enabled bool) (PromotionActivationBatch, error) {
	var summary PromotionActivationBatch
	if !enabled {
		return summary, nil
	}
	rows, err := repo.pool.Query(ctx, `SELECT DISTINCT deployment.id
		FROM strategy_deployments deployment
		JOIN promotion_retirement_decisions decision ON decision.deployment_id=deployment.id
		JOIN statistical_robustness_assessments assessment ON assessment.id=decision.assessment_id
		WHERE deployment.account_id=$1 AND assessment.scope_id=$2
		AND NOT EXISTS(SELECT 1 FROM promotion_retirement_decisions child WHERE child.prior_decision_id=decision.id)
		ORDER BY deployment.id`, accountID, scopeID)
	if err != nil {
		return summary, err
	}
	defer rows.Close()
	ids := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			return summary, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return summary, err
	}
	summary.Eligible = len(ids)
	for _, id := range ids {
		projection, projectErr := repo.ProjectAuthoritativeActivation(ctx, id, accountID, scopeID, true)
		if projectErr != nil {
			return summary, fmt.Errorf("project promotion deployment %s: %w", id, projectErr)
		}
		if projection == nil || !projection.Changed {
			summary.Noop++
			continue
		}
		switch projection.Activation.Action() {
		case promotion.ActivationAction:
			summary.Activated++
		case promotion.SuspensionAction:
			summary.Suspended++
		}
	}
	return summary, nil
}

// ProjectAuthoritativeActivation applies one authoritative promotion head to
// its legacy runtime strategy. It is inert unless explicitly enabled.
func (repo *PromotionRepo) ProjectAuthoritativeActivation(ctx context.Context, deploymentID, accountID, scopeID uuid.UUID, enabled bool) (*PromotionActivationProjection, error) {
	if !enabled {
		return &PromotionActivationProjection{Reason: "AUTOMATIC_SHADOW_PROMOTION is disabled"}, nil
	}
	if repo == nil || repo.pool == nil || deploymentID == uuid.Nil || accountID == uuid.Nil || scopeID == uuid.Nil {
		return nil, fmt.Errorf("postgres: promotion activation requires deployment, canonical account, and evaluation scope")
	}
	readiness, err := NewReportArtifactRepo(repo.pool).DiscoveryDeploymentReadinessForScope(ctx, scopeID, accountID)
	if err != nil {
		return nil, fmt.Errorf("postgres: promotion activation scope is not ready: %w", err)
	}
	deployment, err := repo.GetDeployment(ctx, deploymentID)
	if err != nil {
		return nil, err
	}
	if deployment.AccountID() != accountID || deployment.CapitalBindingID() == uuid.Nil || deployment.Mode() != "paper_scored" ||
		!deploymentRiskPolicyPattern.MatchString(deployment.RiskPolicyVersion()) {
		return nil, fmt.Errorf("postgres: promotion deployment account, mode, capital binding, or risk policy is invalid")
	}
	state, headID, err := repo.ProjectDeploymentState(ctx, deploymentID)
	if err != nil {
		return nil, err
	}
	if headID == uuid.Nil {
		return &PromotionActivationProjection{Reason: "deployment has no authoritative promotion decision"}, nil
	}
	decision, err := repo.GetDecision(ctx, headID)
	if err != nil {
		return nil, err
	}
	if decision.DeploymentID() != deploymentID || decision.VersionID() != deployment.VersionID() || decision.AssessmentID() == uuid.Nil {
		return nil, fmt.Errorf("postgres: authoritative promotion decision diverges from deployment")
	}
	assessment, err := repo.GetAssessment(ctx, decision.AssessmentID())
	if err != nil {
		return nil, err
	}
	if assessment.ScopeID() != scopeID {
		return nil, fmt.Errorf("postgres: promotion assessment belongs to a different evaluation scope")
	}

	tx, err := repo.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, deploymentID.String()); err != nil {
		return nil, err
	}
	var currentHead uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT decision.id FROM promotion_retirement_decisions decision
		WHERE decision.deployment_id=$1 AND NOT EXISTS(
			SELECT 1 FROM promotion_retirement_decisions child WHERE child.prior_decision_id=decision.id)`, deploymentID).Scan(&currentHead); err != nil {
		return nil, fmt.Errorf("postgres: promotion deployment does not have exactly one authoritative head: %w", err)
	}
	if currentHead != headID {
		return nil, fmt.Errorf("postgres: authoritative promotion head changed during projection: %w", repository.ErrIdempotencyConflict)
	}

	prior, err := loadActivationHeadTx(ctx, tx, deploymentID)
	if err != nil {
		return nil, err
	}
	if prior != nil && prior.DecisionID() == decision.ID() {
		return &PromotionActivationProjection{Activation: prior, Reason: "authoritative decision already projected"}, nil
	}

	action := ""
	switch {
	case decision.Outcome() == promotion.OutcomeApproved && state == promotion.StateShadow:
		action = promotion.ActivationAction
	case decision.Outcome() == promotion.OutcomeHeld || decision.Outcome() == promotion.OutcomeRetired:
		if prior == nil || prior.Action() == promotion.SuspensionAction {
			return &PromotionActivationProjection{Activation: prior, Reason: "non-approved decision has no active schedule"}, nil
		}
		action = promotion.SuspensionAction
	default:
		return &PromotionActivationProjection{Reason: "authoritative decision is not activation-eligible"}, nil
	}

	strategy, err := loadActivationStrategyTx(ctx, tx, deployment.VersionID(), prior)
	if err != nil {
		return nil, err
	}
	if strategy.MarketType == domain.MarketTypeOptions && !readiness.Options.Ready {
		return nil, fmt.Errorf("postgres: options promotion activation blocked: %s", readiness.Options.Reason)
	}
	if strategy.MarketType != domain.MarketTypeOptions && !readiness.Stock.Ready {
		return nil, fmt.Errorf("postgres: stock promotion activation blocked: %s", readiness.Stock.Reason)
	}
	var breakerCount int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM risk_breaker_state WHERE reset_at IS NULL`).Scan(&breakerCount); err != nil {
		return nil, err
	}
	if breakerCount != 0 {
		return nil, fmt.Errorf("postgres: promotion activation blocked by %d open risk breaker(s)", breakerCount)
	}

	strategy.Status = domain.StrategyStatusActive
	strategy.ScheduleCron = deployment.ScheduleCron()
	if action == promotion.SuspensionAction {
		strategy.Status = domain.StrategyStatusInactive
		strategy.ScheduleCron = ""
	}
	deploymentBudget, err := strconv.ParseFloat(deployment.Budget(), 64)
	if err != nil || deploymentBudget <= 0 {
		return nil, fmt.Errorf("postgres: deployment budget is not executable")
	}
	strategy.Config, err = projectedResearchLifecycle(strategy.Config, action, deploymentID, decision.ID(), scopeID, accountID,
		readiness.ManifestID, readiness.QualityResultID, deployment.CapitalBindingID(), deploymentBudget, deployment.RiskPolicyVersion())
	if err != nil {
		return nil, err
	}
	configBytes, err := marshalConfig(strategy.Config)
	if err != nil {
		return nil, err
	}
	if err = tx.QueryRow(ctx, `UPDATE strategies SET schedule_cron=$1,config=$2,status=$3,skip_next_run=false,is_paper=true,updated_at=NOW()
		WHERE id=$4 RETURNING updated_at`, strategy.ScheduleCron, configBytes, strategy.Status, strategy.ID).Scan(&strategy.UpdatedAt); err != nil {
		return nil, err
	}
	runtimeVersionID, err := bindExecutionVersion(ctx, tx, strategy)
	if err != nil {
		return nil, err
	}
	var runtimeDigest string
	if err = tx.QueryRow(ctx, `SELECT sha256 FROM strategy_versions WHERE id=$1`, runtimeVersionID).Scan(&runtimeDigest); err != nil {
		return nil, err
	}
	input := promotion.ActivationInput{
		Action: action, DeploymentID: deployment.ID(), DeploymentSHA256: deployment.Digest(), DecisionID: decision.ID(),
		DecisionSHA256: decision.Digest(), StrategyID: strategy.ID, SourceVersionID: deployment.VersionID(),
		RuntimeVersionID: runtimeVersionID, RuntimeVersionSHA256: runtimeDigest, AccountID: accountID, ScopeID: scopeID,
		CapitalBindingID: deployment.CapitalBindingID(), ScheduleCron: strategy.ScheduleCron, Timezone: deployment.Timezone(),
		RiskPolicyVersion: deployment.RiskPolicyVersion(),
	}
	if prior != nil {
		input.PriorActivationID = prior.ID()
		input.PriorActivationSHA = prior.Digest()
	}
	activation, err := promotion.NewActivation(input)
	if err != nil {
		return nil, err
	}
	var priorID any
	if input.PriorActivationID != uuid.Nil {
		priorID = input.PriorActivationID
	}
	result, err := tx.Exec(ctx, `INSERT INTO strategy_promotion_activations(
		id,schema_name,action,deployment_id,deployment_sha256,decision_id,decision_sha256,strategy_id,source_version_id,
		runtime_version_id,runtime_version_sha256,account_id,scope_id,capital_binding_id,schedule_cron,timezone_name,
		risk_policy_version,prior_activation_id,prior_activation_sha256,sha256,canonical_bytes,canonical_json,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,convert_from($21,'UTF8')::jsonb,$22)
		ON CONFLICT(id) DO NOTHING`, activation.ID(), promotion.ActivationSchemaV1, action, deployment.ID(), deployment.Digest(),
		decision.ID(), decision.Digest(), strategy.ID, deployment.VersionID(), runtimeVersionID, runtimeDigest, accountID, scopeID,
		deployment.CapitalBindingID(), strategy.ScheduleCron, deployment.Timezone(), deployment.RiskPolicyVersion(), priorID,
		input.PriorActivationSHA, activation.Digest(), activation.CanonicalBytes(), databaseNow())
	if err != nil {
		return nil, evaluationWriteError("insert strategy promotion activation", err)
	}
	if result.RowsAffected() != 1 {
		return nil, fmt.Errorf("postgres: promotion activation changed on retry: %w", repository.ErrIdempotencyConflict)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, evaluationWriteError("commit strategy promotion activation", err)
	}
	return &PromotionActivationProjection{Activation: activation, Changed: true}, nil
}

func loadActivationHeadTx(ctx context.Context, tx pgx.Tx, deploymentID uuid.UUID) (*promotion.Activation, error) {
	var id uuid.UUID
	var digest string
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT activation.id,activation.sha256,activation.canonical_bytes
		FROM strategy_promotion_activations activation WHERE activation.deployment_id=$1
		AND NOT EXISTS(SELECT 1 FROM strategy_promotion_activations child WHERE child.prior_activation_id=activation.id)`, deploymentID).
		Scan(&id, &digest, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: promotion deployment does not have a unique activation head: %w", err)
	}
	return promotion.ActivationFromCanonical(id, digest, raw)
}

func loadActivationStrategyTx(ctx context.Context, tx pgx.Tx, sourceVersionID uuid.UUID, prior *promotion.Activation) (*domain.Strategy, error) {
	query := `SELECT id,name,description,ticker,market_type,schedule_cron,config,status,skip_next_run,is_paper,created_at,updated_at,execution_strategy_version_id
		FROM strategies WHERE execution_strategy_version_id=$1 FOR UPDATE`
	versionID := sourceVersionID
	if prior != nil {
		versionID = prior.RuntimeVersionID()
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM strategies WHERE execution_strategy_version_id=$1`, versionID).Scan(&count); err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, fmt.Errorf("postgres: promotion deployment resolves to %d runtime strategies", count)
	}
	strategy, err := scanStrategy(tx.QueryRow(ctx, query, versionID))
	if err != nil {
		return nil, fmt.Errorf("postgres: promotion deployment must resolve exactly one runtime strategy: %w", err)
	}
	return strategy, nil
}

func projectedResearchLifecycle(raw json.RawMessage, action string, deploymentID, decisionID, scopeID, accountID, manifestID, qualityResultID, capitalBindingID uuid.UUID, deploymentBudget float64, riskPolicyVersion string) (json.RawMessage, error) {
	config := map[string]any{}
	if len(raw) != 0 {
		if err := json.Unmarshal(raw, &config); err != nil || config == nil {
			return nil, fmt.Errorf("postgres: strategy config is not a JSON object")
		}
	}
	stage := "shadow"
	blocked := false
	if action == promotion.SuspensionAction {
		stage, blocked = "held", true
	}
	config["research_lifecycle"] = map[string]any{
		"stage": stage, "activation": "promotion_evaluator_v1", "auto_activation_blocked": blocked,
		"deployment_id": deploymentID.String(), "promotion_decision_id": decisionID.String(),
		"evaluation_scope_id": scopeID.String(), "account_id": accountID.String(),
		"manifest_id": manifestID.String(), "quality_result_id": qualityResultID.String(),
		"capital_binding_id": capitalBindingID.String(), "deployment_budget_usd": deploymentBudget,
		"risk_policy_version": riskPolicyVersion,
	}
	return json.Marshal(config)
}
