package options

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/agent/rules"
	"github.com/PatrickFanella/get-rich-quick/internal/backtest"
	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/discovery"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// OptionsDiscoveryConfig controls the full options discovery pipeline.
type OptionsDiscoveryConfig struct {
	Screener         OptionsScreenerConfig
	Scoring          OptionsScoringConfig
	Generator        discovery.GeneratorConfig
	BacktestCfg      discovery.ScoringConfig // reuse stock scoring thresholds
	Validation       discovery.ValidationConfig
	MaxWinners       int
	DryRun           bool
	ScheduleCron     string
	EvaluationStart  time.Time
	EvaluationEnd    time.Time
	DecisionCutoff   time.Time
	AccountID        uuid.UUID
	ScopeID          uuid.UUID
	SourceCommit     string
	SourceTreeSHA256 string
}

// OptionsDiscoveryDeps holds dependencies for the options pipeline.
type OptionsDiscoveryDeps struct {
	DataService      *data.DataService
	OptionsProvider  data.OptionsDataProvider
	HistoricalReader data.ManifestBoundOptionChainReader
	LLMProvider      interface {
		Complete(context.Context, interface{}) (interface{}, error)
	} // unused — use Generator
	CandidateRegistrar interface {
		RegisterCandidate(context.Context, uuid.UUID, uuid.UUID, rules.OptionsRulesConfig, string, string) (*domain.Strategy, bool, error)
	}
	Logger *slog.Logger
}

// OptionsDeployedStrategy is the backward-compatible result envelope for a
// winning research idea. Creation is inert and does not count as deployment;
// only the authoritative promotion projector can activate a schedule.
type OptionsDeployedStrategy struct {
	StrategyID               uuid.UUID                `json:"strategy_id"`
	Ticker                   string                   `json:"ticker"`
	Config                   rules.OptionsRulesConfig `json:"config"`
	InSample                 backtest.Metrics         `json:"in_sample"`
	OutOfSample              backtest.Metrics         `json:"out_of_sample"`
	Score                    float64                  `json:"score"`
	CalibrationPayloadSHA256 []string                 `json:"calibration_payload_sha256"`
	OOSPayloadSHA256         []string                 `json:"oos_payload_sha256"`
	EvaluationStart          time.Time                `json:"evaluation_start"`
	CalibrationEnd           time.Time                `json:"calibration_end"`
	EvaluationEnd            time.Time                `json:"evaluation_end"`
}

// OptionsDiscoveryResult summarises the pipeline run.
type OptionsDiscoveryResult struct {
	Candidates         int                         `json:"candidates"`
	Scored             int                         `json:"scored"`
	Generated          int                         `json:"generated"`
	Swept              int                         `json:"swept"`
	Validated          int                         `json:"validated"`
	Deployed           int                         `json:"deployed"`
	Proposed           int                         `json:"proposed"`
	Created            int                         `json:"created"`
	Reused             int                         `json:"reused"`
	Winners            []OptionsDeployedStrategy   `json:"winners"`
	GenerationEvidence []OptionsGenerationEvidence `json:"generation_evidence"`
	Duration           time.Duration               `json:"duration_ns"`
	Errors             []string                    `json:"errors"`
	EvidenceClass      string                      `json:"evidence_class"`
}

// RunOptionsDiscovery executes the full options discovery pipeline:
// Screen → Score → Generate → Sweep → Validate → Deploy.
func RunOptionsDiscovery(ctx context.Context, cfg OptionsDiscoveryConfig, deps OptionsDiscoveryDeps) (*OptionsDiscoveryResult, error) {
	start := time.Now()
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	result := &OptionsDiscoveryResult{EvidenceClass: "immutable_observed_options_v1"}

	if cfg.MaxWinners <= 0 {
		cfg.MaxWinners = 3
	}
	if cfg.BacktestCfg.MinSharpe == 0 {
		cfg.BacktestCfg = discovery.DefaultScoringConfig()
	}
	if !canonicalDecisionTime(cfg.EvaluationStart) || !canonicalDecisionTime(cfg.EvaluationEnd) || !canonicalDecisionTime(cfg.DecisionCutoff) || cfg.DecisionCutoff.Before(cfg.EvaluationStart.AddDate(0, 9, 0)) {
		return nil, fmt.Errorf("options/discovery: immutable evaluation interval must contain at least six calibration months plus three out-of-sample months")
	}
	if deps.HistoricalReader == nil {
		return nil, fmt.Errorf("options/discovery: manifest-bound historical options reader is required")
	}
	if !cfg.DryRun && (deps.CandidateRegistrar == nil || cfg.AccountID == uuid.Nil || cfg.ScopeID == uuid.Nil) {
		return nil, fmt.Errorf("options/discovery: native candidate registrar, canonical account, and exact scope are required")
	}
	evaluationCutoff := cfg.EvaluationEnd
	if cfg.DecisionCutoff.Before(evaluationCutoff) {
		evaluationCutoff = cfg.DecisionCutoff
	}
	cfg.Screener.DecisionAt = evaluationCutoff

	// Stage 1: Screen.
	logger.Info("options/discovery: screening candidates")
	candidates, err := ScreenOptions(ctx, deps.DataService, deps.OptionsProvider, cfg.Screener, logger)
	if err != nil {
		return nil, fmt.Errorf("options/discovery: screen: %w", err)
	}
	result.Candidates = len(candidates)
	if len(candidates) == 0 {
		logger.Info("options/discovery: no candidates passed screening")
		result.Duration = time.Since(start)
		return result, nil
	}

	// Stage 2: Score.
	logger.Info("options/discovery: scoring candidates", slog.Int("candidates", len(candidates)))
	scored := ScoreOptionsCandidates(candidates, cfg.Scoring)
	result.Scored = len(scored)

	// Take top 2x max winners for generation.
	limit := cfg.MaxWinners * 2
	if limit > len(scored) {
		limit = len(scored)
	}
	scored = scored[:limit]

	// Stage 3: Generate + Sweep + Validate per candidate.
	type sweepWinner struct {
		ticker                   string
		config                   rules.OptionsRulesConfig
		metrics                  backtest.Metrics
		score                    float64
		bars                     []domain.OHLCV
		inSample                 backtest.Metrics
		oosSample                backtest.Metrics
		calibrationPayloadSHA256 []string
		oosPayloadSHA256         []string
		validated                bool
	}
	var winners []sweepWinner

	validationMonths := cfg.Validation.TestMonths
	if validationMonths == 0 {
		validationMonths = 3
	}
	calibrationMonths := cfg.Validation.CalibrationMonths
	if calibrationMonths == 0 {
		calibrationMonths = 6
	}
	evaluationEnd := evaluationCutoff
	evaluationStart := evaluationEnd.AddDate(0, -(calibrationMonths + validationMonths), 0)
	if evaluationStart.Before(cfg.EvaluationStart) {
		evaluationStart = cfg.EvaluationStart
	}
	calibrationEnd := evaluationStart.AddDate(0, calibrationMonths, 0)
	if calibrationEnd.AddDate(0, validationMonths, 0).After(evaluationEnd) {
		return nil, fmt.Errorf("options/discovery: canonical scope cannot fit the configured calibration and out-of-sample windows")
	}

	for _, candidate := range scored {
		if ctx.Err() != nil {
			break
		}

		// Generate.
		optConfig, generationEvidence, genErr := GenerateOptionsStrategyWithEvidence(ctx, cfg.Generator, candidate, logger)
		if generationEvidence != nil {
			result.GenerationEvidence = append(result.GenerationEvidence, *generationEvidence)
		}
		if genErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("generate %s: %v", candidate.Ticker, genErr))
			continue
		}
		if eligibilityErr := rules.ValidateDefinedRiskVertical(optConfig); eligibilityErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("ineligible generated vertical %s: %v", candidate.Ticker, eligibilityErr))
			continue
		}
		result.Generated++

		// Load the underlying only from the configured immutable scope. The
		// matching options chains are reconstructed at each bar's decision time.
		barsMap, dlErr := deps.DataService.DownloadHistoricalOHLCV(
			ctx, domain.MarketTypeStock,
			[]string{candidate.Ticker},
			data.Timeframe1d, evaluationStart, evaluationEnd, false,
		)
		if dlErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("download %s: %v", candidate.Ticker, dlErr))
			continue
		}
		bars := barsMap[candidate.Ticker]
		if len(bars) < 100 {
			result.Errors = append(result.Errors, fmt.Sprintf("insufficient bars for %s: %d", candidate.Ticker, len(bars)))
			continue
		}
		frames, frameErr := LoadManifestBoundOptionFrames(ctx, deps.HistoricalReader, candidate.Ticker, bars, evaluationStart, evaluationEnd)
		if frameErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("bind historical options %s: %v", candidate.Ticker, frameErr))
			continue
		}
		calibrationFrames := historicalFramesBetween(frames, evaluationStart, calibrationEnd, false)
		validationFrames := historicalFramesBetween(frames, calibrationEnd, evaluationEnd, true)
		inSample, sweepErr := EvaluateManifestBoundOptions(ctx, *optConfig, calibrationFrames, 100_000, backtest.DefaultOptionsFillConfig().FeePerContract)
		if sweepErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("observed calibration %s: %v", candidate.Ticker, sweepErr))
			continue
		}
		result.Swept++
		score := discovery.ScoreMetrics(inSample.Metrics, cfg.BacktestCfg)
		if inSample.OpenedPackages == 0 || math.IsInf(score, -1) {
			continue
		}
		outOfSample, valErr := EvaluateManifestBoundOptions(ctx, *optConfig, validationFrames, 100_000, backtest.DefaultOptionsFillConfig().FeePerContract)
		if valErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("observed out-of-sample %s: %v", candidate.Ticker, valErr))
			continue
		}
		valResult := validateObservedOptions(cfg.Validation, inSample, outOfSample)
		if !valResult.Passed {
			logger.Info("options/discovery: validation failed",
				slog.String("ticker", candidate.Ticker),
				slog.String("reason", valResult.Reason),
			)
			continue
		}
		result.Validated++

		winners = append(winners, sweepWinner{
			ticker:                   candidate.Ticker,
			config:                   *optConfig,
			metrics:                  inSample.Metrics,
			score:                    score,
			bars:                     bars,
			inSample:                 valResult.InSample,
			oosSample:                valResult.OutOfSample,
			calibrationPayloadSHA256: append([]string(nil), inSample.PayloadSHA256...),
			oosPayloadSHA256:         append([]string(nil), outOfSample.PayloadSHA256...),
			validated:                true,
		})
	}

	// Stage 6: Persist top winners as inactive, unscheduled research ideas.
	selected := 0
	for _, w := range winners {
		if selected >= cfg.MaxWinners {
			break
		}

		configJSON, err := json.Marshal(map[string]any{
			"options_rules": w.config,
		})
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("marshal %s: %v", w.ticker, err))
			continue
		}

		cron := cfg.ScheduleCron
		if cron == "" {
			cron = "0 */2 * * *" // every 2 hours; the market-session gate enforces Alpaca hours
		}

		strategy := domain.Strategy{
			ID:           uuid.New(),
			Name:         fmt.Sprintf("options: %s %s", w.ticker, w.config.StrategyType),
			Ticker:       w.ticker,
			MarketType:   domain.MarketTypeOptions,
			IsPaper:      true,
			Status:       domain.StrategyStatusActive,
			ScheduleCron: cron,
			Config:       json.RawMessage(configJSON),
		}

		wasCreated := false
		if !cfg.DryRun {
			createdStrategy, created, createErr := deps.CandidateRegistrar.RegisterCandidate(ctx, cfg.AccountID, cfg.ScopeID, w.config, cfg.SourceCommit, cfg.SourceTreeSHA256)
			if createErr != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("deploy %s: %v", w.ticker, createErr))
				continue
			}
			if createdStrategy == nil {
				result.Errors = append(result.Errors, fmt.Sprintf("deploy %s: candidate registrar returned no strategy", w.ticker))
				continue
			}
			strategy = *createdStrategy
			if !created {
				logger.Info("options/discovery: strategy already exists, reusing",
					slog.String("id", strategy.ID.String()),
					slog.String("ticker", strategy.Ticker),
					slog.String("name", strategy.Name),
				)
			} else {
				wasCreated = true
			}
		}

		result.Winners = append(result.Winners, OptionsDeployedStrategy{
			StrategyID:               strategy.ID,
			Ticker:                   w.ticker,
			Config:                   w.config,
			InSample:                 w.inSample,
			OutOfSample:              w.oosSample,
			Score:                    w.score,
			CalibrationPayloadSHA256: append([]string(nil), w.calibrationPayloadSHA256...),
			OOSPayloadSHA256:         append([]string(nil), w.oosPayloadSHA256...),
			EvaluationStart:          evaluationStart,
			CalibrationEnd:           calibrationEnd,
			EvaluationEnd:            evaluationEnd,
		})
		selected++
		recordOptionsDeploymentOutcome(result, cfg.DryRun, wasCreated)

		logger.Info("options/discovery: winner selected",
			slog.String("id", strategy.ID.String()),
			slog.String("ticker", w.ticker),
			slog.String("type", string(w.config.StrategyType)),
			slog.Float64("score", w.score),
		)
	}
	result.Duration = time.Since(start)
	logger.Info("options/discovery: complete",
		slog.Int("candidates", result.Candidates),
		slog.Int("scored", result.Scored),
		slog.Int("generated", result.Generated),
		slog.Int("swept", result.Swept),
		slog.Int("validated", result.Validated),
		slog.Int("deployed", result.Deployed),
		slog.Int("proposed", result.Proposed),
		slog.Int("created", result.Created),
		slog.Int("reused", result.Reused),
		slog.Duration("duration", result.Duration),
	)

	return result, nil
}

func historicalFramesBetween(frames []HistoricalOptionFrame, start, end time.Time, includeEnd bool) []HistoricalOptionFrame {
	result := make([]HistoricalOptionFrame, 0, len(frames))
	for _, frame := range frames {
		if !frame.DecisionAt.Before(start) && (frame.DecisionAt.Before(end) || includeEnd && frame.DecisionAt.Equal(end)) {
			result = append(result, frame)
		}
	}
	return result
}

func validateObservedOptions(cfg discovery.ValidationConfig, inSample, outOfSample *HistoricalOptionsEvaluation) discovery.ValidationResult {
	result := discovery.ValidationResult{InSample: inSample.Metrics, OutOfSample: outOfSample.Metrics}
	if inSample.OpenedPackages == 0 || outOfSample.OpenedPackages == 0 {
		result.Reason = "immutable calibration and out-of-sample windows must both contain executed packages"
		return result
	}
	if outOfSample.Metrics.SharpeRatio < 0 {
		result.Reason = fmt.Sprintf("OOS Sharpe negative (%.4f)", outOfSample.Metrics.SharpeRatio)
		return result
	}
	minimumRatio := cfg.MinOOSRatio
	if minimumRatio == 0 {
		minimumRatio = 0.5
	}
	if inSample.Metrics.SharpeRatio > 0 {
		result.OOSRatio = outOfSample.Metrics.SharpeRatio / inSample.Metrics.SharpeRatio
		if result.OOSRatio < minimumRatio {
			result.Reason = fmt.Sprintf("OOS ratio %.4f below minimum %.4f", result.OOSRatio, minimumRatio)
			return result
		}
	} else {
		result.OOSRatio = 1
	}
	result.Passed = true
	return result
}

func recordOptionsDeploymentOutcome(result *OptionsDiscoveryResult, dryRun, created bool) {
	if result == nil {
		return
	}
	result.Proposed++
	if dryRun {
		return
	}
	if created {
		result.Created++
		return
	}
	result.Reused++
}
