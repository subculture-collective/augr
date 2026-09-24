package automation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/agent/rules"
	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/data/polygon"
	"github.com/PatrickFanella/get-rich-quick/internal/data/rss"
	"github.com/PatrickFanella/get-rich-quick/internal/discovery"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	kalshiexecution "github.com/PatrickFanella/get-rich-quick/internal/execution/kalshi"
	polymarketexecution "github.com/PatrickFanella/get-rich-quick/internal/execution/polymarket"
	prediction "github.com/PatrickFanella/get-rich-quick/internal/execution/prediction"
	"github.com/PatrickFanella/get-rich-quick/internal/generativestrategy"
	kalshidiscovery "github.com/PatrickFanella/get-rich-quick/internal/kalshidiscovery"
	"github.com/PatrickFanella/get-rich-quick/internal/llm"
	"github.com/PatrickFanella/get-rich-quick/internal/llm/embedding"
	"github.com/PatrickFanella/get-rich-quick/internal/portfolio"
	"github.com/PatrickFanella/get-rich-quick/internal/promotion"
	"github.com/PatrickFanella/get-rich-quick/internal/regime"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	pgrepo "github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
	"github.com/PatrickFanella/get-rich-quick/internal/risk"
	"github.com/PatrickFanella/get-rich-quick/internal/runcontrol"
	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
	"github.com/PatrickFanella/get-rich-quick/internal/universe"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

// All cron expressions use Eastern time (America/New_York) so schedules
// align with US equity market hours regardless of server timezone.
var easternTime = mustLoadEastern()

func mustLoadEastern() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic("automation: load America/New_York: " + err.Error())
	}
	return loc
}

const (
	// autoDisableThreshold is the number of consecutive failures after which a
	// job is automatically disabled to prevent cascading damage.
	autoDisableThreshold         = 5
	defaultAutomationJobTimeout  = 2 * time.Hour
	jobRunPersistenceTimeout     = 10 * time.Second
	jobControlPersistenceTimeout = 5 * time.Second

	// Auto-disabled jobs re-arm after a cooldown that doubles on every repeat
	// auto-disable, bounded by maxAutoDisableCooldown.
	defaultAutoDisableCooldown = time.Hour
	maxAutoDisableCooldown     = 24 * time.Hour

	// hydrateAttempts and defaultHydrateRetryBackoff bound startup retries
	// against a slow or briefly unavailable database.
	hydrateAttempts            = 3
	defaultHydrateRetryBackoff = 500 * time.Millisecond

	// stuckGrace is how far past the job timeout a run may go before it is
	// reported as stuck. After abandonAfterTimeouts timeouts the in-memory
	// claim is released so the schedule can proceed.
	stuckGrace           = 10 * time.Minute
	abandonAfterTimeouts = 2

	defaultMissedRunCatchUpDelay = 30 * time.Second
	missedRunLookback            = 45 * 24 * time.Hour
)

// OrchestratorHealth reports startup hydration problems that left the
// orchestrator running with incomplete persisted state.
type OrchestratorHealth struct {
	Degraded bool       `json:"degraded"`
	Reason   string     `json:"reason,omitempty"`
	Since    *time.Time `json:"since,omitempty"`
}

// ErrJobControlPersistence identifies a failed durable enable/disable write.
var ErrJobControlPersistence = errors.New("automation: job control persistence failed")

const DiscoveryReadinessEvaluationErrorReason = "discovery deployment readiness evaluation failed"

// DiscoveryReadiness is the single startup evaluation shared by automation and API.
type DiscoveryReadiness struct {
	Ready                 bool      `json:"ready"`
	Reason                string    `json:"reason,omitempty"`
	Err                   error     `json:"-"`
	CapabilitiesEvaluated bool      `json:"capabilities_evaluated"`
	StockReady            bool      `json:"stock_ready"`
	StockReason           string    `json:"stock_reason,omitempty"`
	OptionsReady          bool      `json:"options_ready"`
	OptionsReason         string    `json:"options_reason,omitempty"`
	ScopeID               string    `json:"scope_id,omitempty"`
	ManifestID            string    `json:"manifest_id,omitempty"`
	ManifestSHA256        string    `json:"manifest_sha256,omitempty"`
	QualityResultID       string    `json:"quality_result_id,omitempty"`
	QualitySHA256         string    `json:"quality_sha256,omitempty"`
	ObservationCount      int       `json:"observation_count"`
	BindingCount          int       `json:"binding_count"`
	StockPayloadCount     int       `json:"stock_payload_count"`
	OptionsPayloadCount   int       `json:"options_payload_count"`
	OptionBarCount        int       `json:"option_bar_count"`
	OptionContractCount   int       `json:"option_contract_count"`
	OptionQuoteCount      int       `json:"option_quote_count"`
	OptionTradeCount      int       `json:"option_trade_count"`
	OptionSnapshotCount   int       `json:"option_snapshot_count"`
	EvaluationStart       time.Time `json:"evaluation_start,omitempty"`
	EvaluationEnd         time.Time `json:"evaluation_end,omitempty"`
	StockEffectiveStart   time.Time `json:"stock_effective_start,omitempty"`
	StockEffectiveEnd     time.Time `json:"stock_effective_end,omitempty"`
	OptionsEffectiveStart time.Time `json:"options_effective_start,omitempty"`
	OptionsEffectiveEnd   time.Time `json:"options_effective_end,omitempty"`
	DecisionCutoff        time.Time `json:"decision_cutoff,omitempty"`
}

func (o *JobOrchestrator) DiscoveryReadiness() *DiscoveryReadiness {
	if o == nil || o.deps.DiscoveryReadiness == nil {
		return nil
	}
	copyValue := *o.deps.DiscoveryReadiness
	return &copyValue
}

func (readiness *DiscoveryReadiness) StockCapabilityReady() bool {
	if readiness == nil || readiness.Err != nil {
		return false
	}
	if readiness.CapabilitiesEvaluated {
		return readiness.StockReady
	}
	return readiness.Ready
}

func (readiness *DiscoveryReadiness) OptionsCapabilityReady() bool {
	if readiness == nil || readiness.Err != nil {
		return false
	}
	if readiness.CapabilitiesEvaluated {
		return readiness.OptionsReady
	}
	return readiness.Ready
}

// UnavailableJob describes an intentionally omitted job.
type UnavailableJob struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// DegradedError reports a completed automation run that needs operator
// attention but is not a failed execution.
type DegradedError struct {
	Reason string
}

var _ error = (*DegradedError)(nil)

func (e *DegradedError) Error() string {
	return e.Reason
}

// Degradedf creates a degraded automation outcome with a human-readable reason.
func Degradedf(format string, args ...any) error {
	return &DegradedError{Reason: fmt.Sprintf(format, args...)}
}

// IsDegraded reports whether err contains a degraded automation outcome.
func IsDegraded(err error) bool {
	var degraded *DegradedError
	return errors.As(err, &degraded)
}

type AutomationJobRunRepository interface {
	Create(context.Context, *pgrepo.JobRun) error
	Complete(context.Context, *pgrepo.JobRun) error
	FailIncomplete(context.Context, time.Time, string) (int, error)
	Summaries(context.Context) ([]pgrepo.JobRunSummary, error)
}

// stuckRunMarker is optionally implemented by the job run repository to flag
// a running row whose job exceeded its timeout (*pgrepo.JobRunRepo does).
type stuckRunMarker interface {
	MarkStuck(ctx context.Context, id uuid.UUID, at time.Time, reason string) error
}

// autoDisableControlRepository is optionally implemented by the job control
// repository to persist auto-disable provenance (*pgrepo.AutomationJobControlRepo does).
type autoDisableControlRepository interface {
	SetAutoDisabled(ctx context.Context, name, reason string, until time.Time) error
}

// detailedControlLister is optionally implemented by the job control
// repository to expose auto-disable reason and cooldown expiry.
type detailedControlLister interface {
	ListDetailed(ctx context.Context) ([]pgrepo.AutomationJobControlDetail, error)
}

// StrategyTrigger triggers an immediate pipeline run for a strategy.
// The scheduler satisfies this interface.
type StrategyTrigger interface {
	TriggerStrategy(strategy domain.Strategy)
}

// TickerDiscoveryJobConfig configures the database-ledger ticker-discovery job.
type TickerDiscoveryJobConfig struct {
	Enabled    bool
	Cron       string
	MinADV     float64
	MaxTickers int
}

// OrchestratorDeps bundles external dependencies required by the orchestrator.
type OrchestratorDeps struct {
	ExecutionAccount             domain.ExecutionAccountBinding
	CanonicalAccountID           uuid.UUID
	DiscoveryReadiness           *DiscoveryReadiness
	Universe                     *universe.Universe
	Polygon                      *polygon.Client
	PolygonBulkSnapshotsEnabled  bool
	DataService                  *data.DataService
	OperationalDailyProvider     OperationalDailyProvider
	DiscoveryDataService         *data.DataService
	AlpacaReconciler             *AlpacaReconciler
	OptionsProvider              data.OptionsDataProvider
	DiscoveryOptionsProvider     data.OptionsDataProvider
	LLMProvider                  llm.Provider
	LLMQuickModel                string
	GeneratorMetrics             discovery.GeneratorMetrics
	TickerDiscovery              TickerDiscoveryJobConfig
	HistoryRefreshWatchlistLimit int
	EmbeddingProvider            embedding.Provider // optional; nil = skip embedding during triage
	// RiskEngine, when set, receives ledger-derived daily P&L, drawdown, and
	// loss-streak metrics from the portfolio allocator so the in-memory
	// circuit breaker reflects the same evidence as the durable breakers.
	RiskEngine risk.RiskEngine
	// RegimeRules, when any threshold is set, pauses new allocations for a run
	// whose recent results breach the rule set (zero value disables it).
	RegimeRules            regime.RuleConfig
	EventsProvider         data.EventsProvider
	StrategyRepo           repository.StrategyRepository
	PositionRepo           repository.PositionRepository
	OrderRepo              repository.OrderRepository
	TradeRepo              repository.TradeRepository
	OptionSettlementRepo   repository.OptionSettlementRepository
	OptionSettlementState  execution.OptionSettlementState
	OpportunityRepo        repository.OpportunityRepository
	AllocationDecisionRepo repository.AllocationDecisionRepository
	RunRepo                repository.PipelineRunRepository
	JobRunRepo             AutomationJobRunRepository
	JobControlRepo         repository.AutomationJobControlRepository
	OptionsScanRepo        *pgrepo.OptionsScanRepo
	NewsFeedRepo           *pgrepo.NewsFeedRepo
	StrategyTrigger        StrategyTrigger                        // optional; nil = no event-driven triggers
	PolymarketAccountRepo  repository.PolymarketAccountRepository // optional; nil = skip profiling job
	PolymarketReconciler   *polymarketexecution.Reconciler        // optional; nil = skip reconciliation job
	PredictionSettler      interface {
		PendingMarkets(context.Context, domain.MarketType) ([]string, error)
		SettlePreview(context.Context, domain.MarketType, string) (*prediction.SettlementPreview, error)
		PreviewMarket(context.Context, domain.MarketType, string) (int, error)
		SettleDecisions(context.Context, domain.MarketType, string, string, time.Time, []uuid.UUID) (int, error)
		SettleDecisionsWithEvidence(context.Context, domain.MarketType, string, string, time.Time, []uuid.UUID, prediction.ResolutionEvidence) (int, error)
		SettleMarket(context.Context, domain.MarketType, string, string, time.Time) (int, error)
		SettleMarketWithEvidence(context.Context, domain.MarketType, string, string, time.Time, prediction.ResolutionEvidence) (int, error)
	} // optional; settles paper event positions from provider outcomes
	KalshiReconciler            *kalshiexecution.Reconciler // optional; nil = skip live reconciliation job
	PolymarketResolvedRepo      repository.PolymarketResolvedMarketsRepository
	PolymarketWatchedRepo       repository.PolymarketWatchedMarketsRepository // optional; nil = skip discovery auto-watch
	PolymarketDiscoveryRuns     repository.PolymarketDiscoveryRunRepository   // optional; nil = skip chunked discovery job registration/execution
	PolymarketCLOBURL           string                                        // optional; defaults to Polymarket CLOB base URL
	DisablePolymarketAutomation bool                                          // disables Polymarket profile/reconcile/resolution/discovery cron jobs
	KalshiCatalog               interface {
		ListMarkets(context.Context, kalshidiscovery.ListOptions) ([]kalshidiscovery.MarketCandidate, string, error)
		GetMarket(context.Context, string) (*kalshidiscovery.MarketCandidate, error)
	}
	KalshiDiscovery           KalshiDiscoveryConfig // optional; zero values use defaults/env
	PortfolioAllocatorMode    portfolio.AllocatorMode
	PortfolioPaperProcessor   portfolio.PaperOrderProcessor
	PortfolioOptionsProcessor portfolio.PaperOptionsOrderProcessor
	PortfolioAccountBalance   PortfolioAccountBalanceSource
	PortfolioAccountSnapshot  PortfolioAccountSnapshotSource
	PortfolioRiskState        PortfolioRiskStateSource
	KalshiWatchedRepo         repository.KalshiWatchedMarketsRepository
	KalshiMarketSnapshotsRepo repository.KalshiMarketSnapshotsRepository
	KalshiDiscoveryRuns       repository.KalshiDiscoveryRunRepository // optional; nil = skip progress recording
	KalshiSettlementGateRepo  repository.KalshiSettlementGateRepository
	KalshiSettlementThreshold int
	KalshiSettlementDryRun    bool
	KalshiSettlementEnabled   bool
	KalshiMarkProvider        interface {
		LoadSnapshot(context.Context, string) (kalshiexecution.Snapshot, error)
	}
	KalshiProjectionRepo   repository.ProjectionRepository
	KalshiProjectionOutbox repository.ProjectionOutboxRepository
	KalshiMarkMaxAge       time.Duration
	ReportArtifactRepo     *pgrepo.ReportArtifactRepo          // optional; nil = skip report jobs
	BacktestConfigRepo     repository.BacktestConfigRepository // optional; needed by report jobs
	BacktestRunRepo        repository.BacktestRunRepository    // optional; needed by report jobs
	DiscoveryRunRepo       discovery.RunRepository             // required by stock discovery jobs
	OvernightBacktestRuns  repository.OvernightBacktestRunRepository
	GeneratedResearch      interface {
		RunEligible(context.Context, uuid.UUID, uuid.UUID, int, time.Time) (generativestrategy.BatchSummary, error)
	}
	GeneratedProposal interface {
		RunEligible(context.Context, uuid.UUID, uuid.UUID, int) (generativestrategy.ProposalBatchSummary, error)
	}
	GeneratedResearchPreparation interface {
		RunEligible(context.Context, uuid.UUID, uuid.UUID, int) (generativestrategy.PreparationBatchSummary, error)
	}
	GeneratedEvaluation interface {
		RunEligible(context.Context, uuid.UUID, uuid.UUID, int) (generativestrategy.EvaluationBatchSummary, error)
	}
	GeneratedRobustness interface {
		RunEligible(context.Context, uuid.UUID, uuid.UUID, int) (generativestrategy.BatchSummary, error)
	}
	GeneratedDeployment interface {
		RunEligible(context.Context, uuid.UUID, uuid.UUID, int) (generativestrategy.BatchSummary, error)
	}
	ObservedOptionsCandidates interface {
		RegisterCandidate(context.Context, uuid.UUID, uuid.UUID, rules.OptionsRulesConfig, time.Time, time.Time, string, string) (*domain.Strategy, *strategycatalog.Experiment, bool, error)
	}
	OptionsSourceCommit     string
	OptionsSourceTreeSHA256 string
	PromotionActivation     interface {
		ProjectEligibleActivations(context.Context, uuid.UUID, uuid.UUID, bool) (pgrepo.PromotionActivationBatch, error)
	}
	PromotionEvaluation interface {
		EvaluateEligiblePromotions(context.Context, uuid.UUID, uuid.UUID, promotion.Readiness) (pgrepo.PromotionEvaluationBatch, error)
	}
	PromotionAccountSource interface {
		GetByID(context.Context, uuid.UUID) (*domain.Account, error)
	}
	PromotionProjectionSource repository.ProjectionReader
	PromotionEvidenceSource   repository.ScopedCutoverEvidenceReader
	AutomaticShadowPromotion  bool
	DiscoveryScopeID          uuid.UUID
	JobTimeout                time.Duration
	// AutoDisableCooldown is the first re-arm delay after an auto-disable
	// (default 1h; doubles per repeat up to 24h).
	AutoDisableCooldown time.Duration
	// DisableMissedRunCatchUp turns off the startup pass that runs daily or
	// less frequent jobs whose latest scheduled fire was missed.
	DisableMissedRunCatchUp bool
	// MissedRunCatchUpDelay is how long after Start the catch-up pass waits
	// (default 30s).
	MissedRunCatchUpDelay time.Duration
	Logger                *slog.Logger
}

// RegisteredJob tracks a single automated job and its runtime state.
type RegisteredJob struct {
	Name                string
	Description         string
	Schedule            scheduler.ScheduleSpec
	Fn                  func(ctx context.Context) error
	DependsOn           []string // job names that must not be running
	mu                  sync.Mutex
	StartedAt           *time.Time
	LastRun             *time.Time
	LastResult          string
	LastSummary         map[string]int
	LastError           string
	LastDetail          string
	LastErrorAt         *time.Time
	RunCount            int
	ErrorCount          int
	ConsecutiveFailures int
	SettlementGate      *SettlementGateStatus
	Running             bool
	Enabled             bool
	// DisabledReason and DisabledUntil describe an auto-disable; DisabledUntil
	// is nil for explicit operator disables.
	DisabledReason   string
	DisabledUntil    *time.Time
	AutoDisableCount int
	// runSeq identifies the current claim so a wedged run that is abandoned
	// cannot clear a later claim when it finally returns.
	runSeq       uint64
	currentRunID uuid.UUID
	stuckMarked  bool
}

// JobStatus is the read-only snapshot returned by Status.
type JobStatus struct {
	Name                string                `json:"name"`
	Description         string                `json:"description"`
	Schedule            string                `json:"schedule"`
	LastRun             *time.Time            `json:"last_run,omitempty"`
	LastResult          string                `json:"last_result"`
	LastSummary         map[string]int        `json:"last_summary,omitempty"`
	LastError           string                `json:"last_error,omitempty"`
	LastDetail          string                `json:"last_detail,omitempty"`
	LastErrorAt         *time.Time            `json:"last_error_at,omitempty"`
	RunCount            int                   `json:"run_count"`
	ErrorCount          int                   `json:"error_count"`
	ConsecutiveFailures int                   `json:"consecutive_failures"`
	StuckFor            *time.Duration        `json:"stuck_for,omitempty"`
	Running             bool                  `json:"running"`
	Enabled             bool                  `json:"enabled"`
	DisabledReason      string                `json:"disabled_reason,omitempty"`
	DisabledUntil       *time.Time            `json:"disabled_until,omitempty"`
	SettlementGate      *SettlementGateStatus `json:"settlement_gate,omitempty"`
}

type SettlementGateStatus struct {
	ConsecutiveSuccesses  int        `json:"consecutive_dry_run_successes"`
	Threshold             int        `json:"threshold"`
	Eligible              bool       `json:"eligible"`
	ProjectionFingerprint string     `json:"projection_fingerprint,omitempty"`
	LastOutcome           string     `json:"last_outcome,omitempty"`
	LastError             string     `json:"last_error,omitempty"`
	LastRunAt             *time.Time `json:"last_run_at,omitempty"`
	Fetched               int        `json:"fetched"`
	Resolved              int        `json:"resolved"`
	WouldSettleMarkets    int        `json:"would_settle_markets"`
	WouldSettleDecisions  int        `json:"would_settle_decisions"`
}

// AutomationJobMetrics is implemented by *metrics.Metrics.
// It is defined here as an interface to avoid an import cycle.
type AutomationJobMetrics interface {
	RecordAutomationJobError(jobName string)
	RecordAlpacaReconcileRun(result string)
	RecordKalshiReconcileRun(result string)
	RecordKalshiSettlementDryRun(result string)
	RecordKalshiSettlementOutcome(result string)
	RecordKalshiSettlementTransition(from, to string)
}

// ReportWorkerMetrics captures report worker success/error emission.
type ReportWorkerMetrics interface {
	RecordReportWorkerSuccess(strategyID string)
	RecordReportWorkerError(strategyID string)
}

// JobOrchestrator is the central registry and cron runner for all automated jobs.
type JobOrchestrator struct {
	jobs                map[string]*RegisteredJob
	cron                *cron.Cron
	deps                OrchestratorDeps
	logger              *slog.Logger
	rssAggregator       *rss.Aggregator
	metrics             AutomationJobMetrics
	reportMetrics       ReportWorkerMetrics
	reportWorker        *ReportWorker
	kalshiGateUnhealthy bool
	now                 func() time.Time
	runs                *runcontrol.Group
	refreshedTickersMu  sync.RWMutex
	refreshedTickers    []string
	unavailableJobs     []UnavailableJob
	hydrateRetryBackoff time.Duration
	healthMu            sync.Mutex
	health              OrchestratorHealth
}

// NewJobOrchestrator constructs a new orchestrator.
func NewJobOrchestrator(deps OrchestratorDeps) *JobOrchestrator {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if deps.HistoryRefreshWatchlistLimit <= 0 {
		deps.HistoryRefreshWatchlistLimit = defaultHistoryRefreshWatchlistLimit
	}
	o := &JobOrchestrator{
		jobs:   make(map[string]*RegisteredJob),
		cron:   cron.New(cron.WithLocation(easternTime)),
		deps:   deps,
		logger: logger,
		now:    time.Now,
		runs:   runcontrol.NewGroup(),

		hydrateRetryBackoff: defaultHydrateRetryBackoff,
	}
	if deps.DiscoveryReadiness != nil && !o.stockDiscoveryReady() {
		reason := stockDiscoveryUnavailableReason(deps.DiscoveryReadiness)
		for _, name := range stockDiscoveryDeploymentJobNames {
			o.unavailableJobs = append(o.unavailableJobs, UnavailableJob{Name: name, Reason: reason})
		}
	}
	if deps.DiscoveryReadiness != nil && !o.optionsDiscoveryReady() {
		o.unavailableJobs = append(o.unavailableJobs, UnavailableJob{Name: "options_discovery", Reason: optionsDiscoveryUnavailableReason(deps.DiscoveryReadiness)})
	}
	return o
}

func discoveryReadinessUnavailableReason(readiness *DiscoveryReadiness) string {
	if readiness == nil || readiness.Ready || readiness.Err == nil {
		return DiscoveryReadinessEvaluationErrorReason
	}
	var lock repository.ImmutableBindingLock
	if errors.As(readiness.Err, &lock) && strings.TrimSpace(lock.Reason()) != "" {
		return lock.Reason()
	}
	return DiscoveryReadinessEvaluationErrorReason
}

var stockDiscoveryDeploymentJobNames = [...]string{
	"discovery_run", "overnight_backtest", "overnight_generate", "ticker_discovery",
}

var discoveryDeploymentJobNames = [...]string{
	"discovery_run", "options_discovery", "overnight_backtest", "overnight_generate", "ticker_discovery",
}

func (o *JobOrchestrator) discoveryDeploymentReady() bool {
	return o.stockDiscoveryReady()
}

func (o *JobOrchestrator) discoveryDataService() *data.DataService {
	if o.deps.DiscoveryDataService != nil {
		return o.deps.DiscoveryDataService
	}
	// Compatibility for callers that predate capability-specific readiness.
	// Production startup always sets CapabilitiesEvaluated and must supply the
	// manifest-bound service explicitly.
	if o.deps.DiscoveryReadiness != nil && !o.deps.DiscoveryReadiness.CapabilitiesEvaluated {
		return o.deps.DataService
	}
	return nil
}

func (o *JobOrchestrator) discoveryOptionsProvider() data.OptionsDataProvider {
	if o.deps.DiscoveryOptionsProvider != nil {
		return o.deps.DiscoveryOptionsProvider
	}
	if o.deps.DiscoveryReadiness != nil && !o.deps.DiscoveryReadiness.CapabilitiesEvaluated {
		return o.deps.OptionsProvider
	}
	return nil
}

func (o *JobOrchestrator) stockDiscoveryReady() bool {
	return o.deps.DiscoveryReadiness.StockCapabilityReady()
}

func (o *JobOrchestrator) optionsDiscoveryReady() bool {
	return o.deps.DiscoveryReadiness.OptionsCapabilityReady()
}

func stockDiscoveryUnavailableReason(readiness *DiscoveryReadiness) string {
	if readiness != nil && readiness.CapabilitiesEvaluated && strings.TrimSpace(readiness.StockReason) != "" {
		return readiness.StockReason
	}
	return discoveryReadinessUnavailableReason(readiness)
}

func optionsDiscoveryUnavailableReason(readiness *DiscoveryReadiness) string {
	if readiness != nil && readiness.CapabilitiesEvaluated && strings.TrimSpace(readiness.OptionsReason) != "" {
		return readiness.OptionsReason
	}
	return discoveryReadinessUnavailableReason(readiness)
}

// UnavailableJobs returns sorted diagnostics for jobs omitted at startup.
func (o *JobOrchestrator) UnavailableJobs() []UnavailableJob {
	jobs := make([]UnavailableJob, len(o.unavailableJobs))
	copy(jobs, o.unavailableJobs)
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].Name < jobs[j].Name })
	return jobs
}

func (o *JobOrchestrator) setRefreshedTickers(tickers []string) {
	o.refreshedTickersMu.Lock()
	o.refreshedTickers = append([]string(nil), tickers...)
	o.refreshedTickersMu.Unlock()
}

func (o *JobOrchestrator) getRefreshedTickers() []string {
	o.refreshedTickersMu.RLock()
	tickers := append([]string(nil), o.refreshedTickers...)
	o.refreshedTickersMu.RUnlock()
	return tickers
}

func (o *JobOrchestrator) currentTime() time.Time {
	if o.now != nil {
		return o.now()
	}
	return time.Now()
}

func (o *JobOrchestrator) jobContextFrom(parent context.Context) (context.Context, context.CancelFunc) {
	timeout := o.deps.JobTimeout
	if timeout <= 0 {
		timeout = defaultAutomationJobTimeout
	}
	return context.WithTimeout(parent, timeout)
}

// invokeAutomationJob contains a job-local panic so one defective automation
// cannot terminate the scheduler process. Panic values may contain provider or
// model data, so only their type is returned to the durable failure path.
func invokeAutomationJob(ctx context.Context, fn func(context.Context) error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("automation: job panicked (%T)", recovered)
		}
	}()
	if fn == nil {
		return fmt.Errorf("automation: job function is nil")
	}
	return fn(ctx)
}

// WithJobMetrics attaches a metrics sink to the orchestrator.
// Call before Start(). Safe to call with nil (disables metrics).
func (o *JobOrchestrator) WithJobMetrics(m AutomationJobMetrics) {
	o.metrics = m
}

// WithReportMetrics attaches report-worker-specific metrics.
// Call before Start(). Safe to call with nil.
func (o *JobOrchestrator) WithReportMetrics(m ReportWorkerMetrics) {
	o.reportMetrics = m
}

// SetConsecutiveFailures sets the ConsecutiveFailures counter on a job.
// Primarily for testing and operational resets.
func (o *JobOrchestrator) SetConsecutiveFailures(name string, n int) {
	if job, ok := o.jobs[name]; ok {
		job.mu.Lock()
		job.ConsecutiveFailures = n
		job.mu.Unlock()
	}
}

func (o *JobOrchestrator) SetLastSummary(name string, summary map[string]int) {
	if job, ok := o.jobs[name]; ok {
		job.mu.Lock()
		job.LastSummary = cloneSummary(summary)
		job.mu.Unlock()
	}
}

// Register adds a job to the registry.
func (o *JobOrchestrator) Register(name, description string, spec scheduler.ScheduleSpec, fn func(ctx context.Context) error, dependsOn ...string) {
	o.jobs[name] = &RegisteredJob{
		Name:        name,
		Description: description,
		Schedule:    spec,
		Fn:          fn,
		DependsOn:   dependsOn,
		Enabled:     true,
	}
}

// RegisteredJobKeys returns a sorted copy of the registered job inventory.
// OVR-604 uses it to fail closed when a financial job lacks an explicit
// distributed occurrence/effect classification.
func (o *JobOrchestrator) RegisteredJobKeys() []string {
	keys := make([]string, 0, len(o.jobs))
	for key := range o.jobs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// RegisterAll registers all automated jobs from every job group.
func (o *JobOrchestrator) RegisterAll() {
	if len(o.unavailableJobs) == 0 {
		if !o.stockDiscoveryReady() {
			for _, name := range stockDiscoveryDeploymentJobNames {
				o.unavailableJobs = append(o.unavailableJobs, UnavailableJob{Name: name, Reason: stockDiscoveryUnavailableReason(o.deps.DiscoveryReadiness)})
			}
		}
		if !o.optionsDiscoveryReady() {
			o.unavailableJobs = append(o.unavailableJobs, UnavailableJob{Name: "options_discovery", Reason: optionsDiscoveryUnavailableReason(o.deps.DiscoveryReadiness)})
		}
	}
	o.registerBrokerReconciliationJobs()
	o.registerMarketJobs()
	o.registerPreMarketJobs()
	o.registerTickerDiscoveryJob()
	o.registerPostMarketJobs()
	o.registerOptionsLifecycleJobs()
	o.registerEventJobs()
	o.registerOvernightJobs()
	o.registerWeeklyJobs()
	o.registerNewsJobs()
	if !o.deps.DisablePolymarketAutomation {
		o.registerPolymarketProfileJob()
		o.registerPolymarketReconciliationJobs()
		o.registerPolymarketResolutionsJob()
		o.registerPolymarketDiscoveryJob()
	}
	o.registerKalshiDiscoveryJob()
	o.registerKalshiMarkingJob()
	o.registerProjectionRefreshJob()
	o.registerKalshiSettlementJob()
	o.registerKalshiReconciliationJob()
	o.registerReportJobs()
	o.registerPortfolioAllocatorJobs()
	o.registerGeneratedProposalJob()
	o.registerGeneratedResearchPreparationJob()
	o.registerGeneratedResearchJob()
	o.registerGeneratedEvaluationJob()
	o.registerGeneratedRobustnessJob()
	o.registerGeneratedDeploymentJob()
	o.registerPromotionEvaluationJob()
	o.registerPromotionActivationJob()
}

func (o *JobOrchestrator) registerPromotionActivationJob() {
	if !o.deps.AutomaticShadowPromotion {
		return
	}
	if o.deps.PromotionActivation == nil || o.deps.CanonicalAccountID == uuid.Nil || o.deps.DiscoveryScopeID == uuid.Nil {
		o.unavailableJobs = append(o.unavailableJobs, UnavailableJob{Name: "promotion_activation", Reason: "automatic promotion requires canonical account, configured scope, and projector"})
		return
	}
	o.Register("promotion_activation", "Project approved promotion heads into paper shadow schedules", scheduler.ScheduleSpec{
		Type: scheduler.ScheduleTypeCron, Cron: "*/5 * * * *",
	}, func(ctx context.Context) error {
		summary, err := o.deps.PromotionActivation.ProjectEligibleActivations(ctx, o.deps.CanonicalAccountID, o.deps.DiscoveryScopeID, true)
		o.SetLastSummary("promotion_activation", map[string]int{
			"eligible": summary.Eligible, "activated": summary.Activated, "suspended": summary.Suspended, "noop": summary.Noop,
		})
		return err
	})
}

// Start starts the cron engine with all registered jobs.
// It hydrates in-memory counters from the database first.
func (o *JobOrchestrator) Start() error {
	o.hydrateFromDB()

	for _, job := range o.jobs {
		j := job // capture for closure
		_, err := o.cron.AddFunc(j.Schedule.Cron, func() {
			o.wrapAndRun(j)
		})
		if err != nil {
			return fmt.Errorf("automation: failed to schedule job %q: %w", j.Name, err)
		}
		o.logger.Info("automation: scheduled job",
			slog.String("name", j.Name),
			slog.String("cron", j.Schedule.Cron),
			slog.String("type", string(j.Schedule.Type)),
		)
	}
	o.cron.Start()
	o.logger.Info("automation: orchestrator started", slog.Int("jobs", len(o.jobs)))
	if !o.deps.DisableMissedRunCatchUp {
		go o.catchUpMissedRuns()
	}
	return nil
}

// Health reports whether startup hydration left the orchestrator degraded.
func (o *JobOrchestrator) Health() OrchestratorHealth {
	o.healthMu.Lock()
	defer o.healthMu.Unlock()
	h := o.health
	h.Since = cloneTime(o.health.Since)
	return h
}

func (o *JobOrchestrator) markDegraded(reason string) {
	at := o.currentTime()
	o.healthMu.Lock()
	if o.health.Degraded {
		o.health.Reason = o.health.Reason + "; " + reason
	} else {
		o.health = OrchestratorHealth{Degraded: true, Reason: reason, Since: &at}
	}
	o.healthMu.Unlock()
}

// Stop stops all jobs and the cron engine.
func (o *JobOrchestrator) Stop() {
	o.runs.Stop(runcontrol.Shutdown)
	ctx := o.cron.Stop()
	<-ctx.Done()
	o.runs.Wait()
	o.logger.Info("automation: orchestrator stopped")
}

// Status returns status for all registered jobs, sorted by name.
func (o *JobOrchestrator) Status() []JobStatus {
	statuses := make([]JobStatus, 0, len(o.jobs))
	for _, job := range o.jobs {
		job.mu.Lock()
		var stuckFor *time.Duration
		if job.Running && job.StartedAt != nil {
			d := time.Since(*job.StartedAt)
			stuckFor = &d
		}
		s := JobStatus{
			Name:                job.Name,
			Description:         job.Description,
			Schedule:            job.Schedule.Describe(),
			LastRun:             job.LastRun,
			LastResult:          job.LastResult,
			LastSummary:         cloneSummary(job.LastSummary),
			LastError:           job.LastError,
			LastDetail:          job.LastDetail,
			LastErrorAt:         job.LastErrorAt,
			RunCount:            job.RunCount,
			ErrorCount:          job.ErrorCount,
			ConsecutiveFailures: job.ConsecutiveFailures,
			StuckFor:            stuckFor,
			Running:             job.Running,
			Enabled:             job.Enabled,
			DisabledReason:      job.DisabledReason,
			DisabledUntil:       cloneTime(job.DisabledUntil),
			SettlementGate:      cloneSettlementGateStatus(job.SettlementGate),
		}
		job.mu.Unlock()
		statuses = append(statuses, s)
	}
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Name < statuses[j].Name
	})
	return statuses
}

// RunJob triggers a specific job by name immediately while retaining the
// schedule's market-session, weekday, and holiday safety gates. The cron
// minute itself is intentionally not checked by ScheduleSpec.ShouldFire, so
// operators can rerun a job anywhere inside its authorized session.
func (o *JobOrchestrator) RunJob(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	job, ok := o.jobs[name]
	if !ok {
		return fmt.Errorf("automation: unknown job %q", name)
	}
	job.mu.Lock()
	enabled := job.Enabled
	schedule := job.Schedule
	job.mu.Unlock()
	if !enabled {
		return fmt.Errorf("automation: job %q is disabled", name)
	}
	now := o.currentTime()
	if !schedule.ShouldFire(now) {
		return fmt.Errorf("automation: job %q is outside configured session (%s)", name, schedule.Describe())
	}
	startedAt := now
	runCtx, lease, err := o.runs.Admit(context.WithoutCancel(ctx))
	if err != nil {
		return err
	}
	if err := claimManualJob(job, startedAt); err != nil {
		lease.Done()
		return err
	}
	o.logger.Info("automation: manual trigger", slog.String("job", name))
	go func() {
		defer lease.Done()
		o.runClaimedDirect(runCtx, job, startedAt)
	}()
	return nil
}

// runDirect runs a job immediately without checking ShouldFire (for manual triggers).
func (o *JobOrchestrator) runDirect(job *RegisteredJob) {
	ctx, lease, err := o.runs.Admit(context.Background())
	if err != nil {
		return
	}
	defer lease.Done()
	startedAt := o.currentTime()
	if err := claimManualJob(job, startedAt); err != nil {
		o.logger.Info("automation: manual run not admitted", slog.String("job", job.Name), slog.Any("error", err))
		return
	}
	o.runClaimedDirect(ctx, job, startedAt)
}

func claimManualJob(job *RegisteredJob, startedAt time.Time) error {
	job.mu.Lock()
	defer job.mu.Unlock()
	if !job.Enabled {
		if job.DisabledUntil != nil {
			return fmt.Errorf("automation: job %q is auto-disabled until %s (%s)", job.Name, job.DisabledUntil.UTC().Format(time.RFC3339), job.DisabledReason)
		}
		return fmt.Errorf("automation: job %q is disabled", job.Name)
	}
	if job.Running {
		return fmt.Errorf("automation: job %q is already running", job.Name)
	}
	job.claimLocked(startedAt)
	return nil
}

// claimLocked marks the job running and returns the claim sequence. Caller
// holds job.mu.
func (job *RegisteredJob) claimLocked(startedAt time.Time) uint64 {
	job.runSeq++
	job.Running = true
	job.StartedAt = &startedAt
	job.currentRunID = uuid.Nil
	job.stuckMarked = false
	return job.runSeq
}

// releaseClaim clears the running state only if seq is still the current
// claim; an abandoned wedged run must not clear a newer claim.
func (job *RegisteredJob) releaseClaim(seq uint64) {
	job.mu.Lock()
	defer job.mu.Unlock()
	if job.runSeq != seq {
		return
	}
	job.Running = false
	job.StartedAt = nil
	job.currentRunID = uuid.Nil
}

func (job *RegisteredJob) currentSeq() uint64 {
	job.mu.Lock()
	defer job.mu.Unlock()
	return job.runSeq
}

func (o *JobOrchestrator) runClaimedDirect(parent context.Context, job *RegisteredJob, startedAt time.Time) {
	seq := job.currentSeq()
	// Require every dependency to have completed successfully in today's
	// Eastern automation cycle, not merely to be idle at this instant.
	if dep, reason := o.dependencyBlocker(job, startedAt); dep != "" {
		o.recordDependencySkip(job, startedAt, dep, reason)
		return
	}

	defer job.releaseClaim(seq)
	run, beginErr := o.beginRun(job, startedAt)
	if beginErr != nil {
		now := o.currentTime()
		_ = o.applyRunPersistenceFailure(job, now, beginErr)
		o.logger.Error("automation: failed to persist running job", slog.String("job", job.Name), slog.Any("error", beginErr))
		return
	}
	o.setCurrentRun(job, seq, run)

	o.logger.Info("automation: job starting", slog.String("job", job.Name))
	start := time.Now()
	ctx, cancel := o.jobContextFrom(parent)
	defer cancel()
	if job.Name == "current_data_refresh" {
		o.setRefreshedTickers(nil)
	}
	err := invokeAutomationJob(ctx, job.Fn)
	elapsed := time.Since(start)
	degraded := IsDegraded(err)

	job.mu.Lock()
	completedAt := o.currentTime()
	job.LastRun = &completedAt
	job.RunCount++
	switch {
	case degraded:
		job.LastResult = "degraded"
		job.LastError = ""
		job.LastDetail = err.Error()
		job.LastErrorAt = nil
		job.ConsecutiveFailures = 0
		o.logger.Warn("automation: job degraded", slog.String("job", job.Name), slog.Duration("elapsed", elapsed), slog.String("reason", err.Error()))
	case err != nil:
		job.ErrorCount++
		job.LastResult = "failed"
		job.LastError = err.Error()
		job.LastDetail = ""
		job.LastErrorAt = &completedAt
		job.ConsecutiveFailures++
		o.logger.Error("automation: job failed", slog.String("job", job.Name), slog.Duration("elapsed", elapsed), slog.Any("error", err))
		if o.metrics != nil {
			o.metrics.RecordAutomationJobError(job.Name)
		}
		if job.ConsecutiveFailures >= autoDisableThreshold {
			o.autoDisableLocked(job, completedAt, fmt.Sprintf("%d consecutive failures; last: %s", job.ConsecutiveFailures, err.Error()))
		}
	default:
		job.LastResult = "success"
		job.LastError = ""
		job.LastDetail = ""
		job.ConsecutiveFailures = 0
		o.logger.Info("automation: job completed", slog.String("job", job.Name), slog.Duration("elapsed", elapsed))
	}
	job.mu.Unlock()

	if persistErr := o.completeRun(run, job, completedAt, elapsed, err); persistErr != nil {
		o.logger.Error("automation: failed to persist job run", slog.String("job", job.Name), slog.Any("error", persistErr))
		if err == nil || degraded {
			_ = o.applyRunPersistenceFailure(job, completedAt, persistErr)
		}
	}
}

// SetEnabled enables or disables a job.
func (o *JobOrchestrator) SetEnabled(name string, enabled bool) error {
	return o.SetEnabledBy(context.Background(), name, enabled, "system")
}

// SetEnabledBy durably records an operator override before changing the
// in-memory scheduler state. If persistence fails, the current state is kept.
func (o *JobOrchestrator) SetEnabledBy(ctx context.Context, name string, enabled bool, actor string) error {
	job, ok := o.jobs[name]
	if !ok {
		return fmt.Errorf("automation: unknown job %q", name)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = "unknown"
	}
	// Serialize persistence and memory mutation with scheduler admission. This
	// prevents a run from starting while a disable is being committed and keeps
	// concurrent control requests in the same order in PostgreSQL and memory.
	job.mu.Lock()
	previous := job.Enabled
	if o.deps.JobControlRepo != nil {
		persistCtx, cancel := context.WithTimeout(ctx, jobControlPersistenceTimeout)
		err := o.deps.JobControlRepo.SetEnabled(persistCtx, name, enabled, actor)
		cancel()
		if err != nil {
			job.mu.Unlock()
			return fmt.Errorf("%w for %q: %v", ErrJobControlPersistence, name, err)
		}
	}
	job.Enabled = enabled
	job.DisabledReason = ""
	job.DisabledUntil = nil
	if enabled {
		job.AutoDisableCount = 0
	}
	if name == "kalshi_settlement" && o.deps.KalshiSettlementGateRepo != nil {
		gateCtx, gateCancel := context.WithTimeout(ctx, jobControlPersistenceTimeout)
		state, err := o.deps.KalshiSettlementGateRepo.Get(gateCtx, name)
		gateCancel()
		if err == nil {
			job.SettlementGate = settlementGateStatusFromState(state)
		}
	}
	job.mu.Unlock()
	if name == "kalshi_settlement" && o.metrics != nil && previous != enabled {
		o.metrics.RecordKalshiSettlementTransition(fmt.Sprintf("%t", previous), fmt.Sprintf("%t", enabled))
	}
	o.logger.Info("automation: job enabled state changed",
		slog.String("job", name),
		slog.Bool("enabled", enabled),
		slog.String("actor", actor),
	)
	return nil
}

func settlementGateStatusFromState(state *domain.KalshiSettlementGateState) *SettlementGateStatus {
	if state == nil {
		return nil
	}
	return &SettlementGateStatus{
		ConsecutiveSuccesses:  state.ConsecutiveSuccesses,
		Threshold:             state.Threshold,
		Eligible:              state.Eligible,
		ProjectionFingerprint: state.ProjectionFingerprint,
		LastOutcome:           state.LastOutcome,
		LastError:             state.LastError,
		LastRunAt:             state.LastRunAt,
		Fetched:               state.Fetched,
		Resolved:              state.Resolved,
		WouldSettleMarkets:    state.WouldSettleMarkets,
		WouldSettleDecisions:  state.WouldSettleDecisions,
	}
}

// wrapAndRun is the common wrapper that checks preconditions and runs the job.
func (o *JobOrchestrator) wrapAndRun(job *RegisteredJob) {
	parent, lease, err := o.runs.Admit(context.Background())
	if err != nil {
		return
	}
	defer lease.Done()
	now := o.currentTime()

	job.mu.Lock()
	if !job.Enabled {
		if job.DisabledUntil == nil || now.Before(*job.DisabledUntil) {
			job.mu.Unlock()
			return
		}
		o.rearmLocked(job, now)
	}
	if !job.Schedule.ShouldFire(now) {
		job.mu.Unlock()
		return
	}
	if job.Running {
		if !o.handleOverlapLocked(job, now) {
			job.mu.Unlock()
			return
		}
	}
	startedAt := now
	seq := job.claimLocked(startedAt)
	job.mu.Unlock()

	if dep, reason := o.dependencyBlocker(job, startedAt); dep != "" {
		o.recordDependencySkip(job, startedAt, dep, reason)
		return
	}

	defer job.releaseClaim(seq)
	run, beginErr := o.beginRun(job, startedAt)
	if beginErr != nil {
		_ = o.applyRunPersistenceFailure(job, o.currentTime(), beginErr)
		o.logger.Error("automation: failed to persist running job", slog.String("job", job.Name), slog.Any("error", beginErr))
		return
	}
	o.setCurrentRun(job, seq, run)

	o.logger.Info("automation: job starting", slog.String("job", job.Name))
	start := time.Now()

	ctx, cancel := o.jobContextFrom(parent)
	defer cancel()
	if job.Name == "current_data_refresh" {
		o.setRefreshedTickers(nil)
	}
	err = invokeAutomationJob(ctx, job.Fn)

	elapsed := time.Since(start)
	completedAt := o.currentTime()
	degraded := IsDegraded(err)

	job.mu.Lock()
	job.LastRun = &completedAt
	job.RunCount++
	switch {
	case degraded:
		job.LastError = ""
		job.LastDetail = err.Error()
		job.LastErrorAt = nil
		job.ConsecutiveFailures = 0
		job.LastResult = fmt.Sprintf("degraded after %s", elapsed.Truncate(time.Millisecond))
	case err != nil:
		job.ErrorCount++
		job.LastError = err.Error()
		job.LastDetail = ""
		job.LastErrorAt = &completedAt
		job.ConsecutiveFailures++
		job.LastResult = fmt.Sprintf("error after %s", elapsed.Truncate(time.Millisecond))
		if o.metrics != nil {
			o.metrics.RecordAutomationJobError(job.Name)
		}
		if job.ConsecutiveFailures >= autoDisableThreshold {
			o.autoDisableLocked(job, completedAt, fmt.Sprintf("%d consecutive failures; last: %s", job.ConsecutiveFailures, err.Error()))
		}
	default:
		job.LastError = ""
		job.LastDetail = ""
		job.ConsecutiveFailures = 0
		job.LastResult = fmt.Sprintf("ok in %s", elapsed.Truncate(time.Millisecond))
	}
	job.mu.Unlock()

	if persistErr := o.completeRun(run, job, completedAt, elapsed, err); persistErr != nil {
		o.logger.Error("automation: failed to persist job run", slog.String("job", job.Name), slog.Any("error", persistErr))
		if err == nil || degraded {
			err = o.applyRunPersistenceFailure(job, completedAt, persistErr)
			degraded = false
		}
	}

	switch {
	case degraded:
		o.logger.Warn("automation: job degraded",
			slog.String("job", job.Name),
			slog.Duration("elapsed", elapsed),
			slog.String("reason", err.Error()),
		)
	case err != nil:
		o.logger.Error("automation: job failed",
			slog.String("job", job.Name),
			slog.Duration("elapsed", elapsed),
			slog.Any("error", err),
		)
	default:
		o.logger.Info("automation: job completed",
			slog.String("job", job.Name),
			slog.Duration("elapsed", elapsed),
		)
	}
}

func (o *JobOrchestrator) dependencyBlocker(job *RegisteredJob, now time.Time) (string, string) {
	for _, dep := range job.DependsOn {
		depJob, ok := o.jobs[dep]
		if !ok {
			return dep, "not registered"
		}
		depJob.mu.Lock()
		running := depJob.Running
		enabled := depJob.Enabled
		lastRun := depJob.LastRun
		lastResult := depJob.LastResult
		depJob.mu.Unlock()

		switch {
		case !enabled:
			return dep, "disabled"
		case running:
			return dep, "still running"
		case lastRun == nil:
			return dep, "has not completed"
		case !sameMarketDate(lastRun.In(easternTime), now.In(easternTime)):
			return dep, "latest run is from a prior automation day"
		case !successfulJobResult(lastResult):
			return dep, "latest run was not successful"
		case dep == "current_data_refresh" && len(o.getRefreshedTickers()) == 0:
			return dep, "fresh ticker payload unavailable"
		case marketPipelineCycleStart(job.Name, now).After(*lastRun):
			return dep, "latest successful run is from a prior hourly cycle"
		}
	}
	return "", ""
}

func marketPipelineCycleStart(jobName string, now time.Time) time.Time {
	nowET := now.In(easternTime)
	hour := nowET.Truncate(time.Hour)
	switch jobName {
	case "hot_scan":
		opening := time.Date(nowET.Year(), nowET.Month(), nowET.Day(), 9, 45, 0, 0, easternTime)
		if nowET.Before(opening) {
			return opening
		}
		if nowET.Minute() >= 45 {
			return hour.Add(45 * time.Minute)
		}
		return hour.Add(-15 * time.Minute)
	case "deep_scan":
		return hour
	default:
		return time.Time{}
	}
}

func successfulJobResult(result string) bool {
	normalized := strings.ToLower(strings.TrimSpace(result))
	return normalized == "ok" || normalized == "success" || normalized == "degraded" || strings.HasPrefix(normalized, "ok in ") || strings.HasPrefix(normalized, "degraded after ")
}

func (o *JobOrchestrator) recordDependencySkip(job *RegisteredJob, at time.Time, dep, reason string) {
	message := fmt.Sprintf("dependency %s %s", dep, reason)
	o.logger.Warn("automation: skipping job, dependency unavailable",
		slog.String("job", job.Name),
		slog.String("blocked_by", dep),
		slog.String("reason", reason),
	)

	job.mu.Lock()
	job.Running = false
	job.StartedAt = nil
	job.LastRun = &at
	job.LastResult = "skipped: " + message
	job.LastError = ""
	job.LastDetail = message
	job.LastSummary = map[string]int{"dependency_blocked": 1}
	job.RunCount++
	lastErrorAt := job.LastErrorAt
	consecutiveFailures := job.ConsecutiveFailures
	job.mu.Unlock()

	if o.deps.JobRunRepo == nil {
		return
	}
	completed := at
	run := &pgrepo.JobRun{
		JobName:             job.Name,
		Status:              "skipped",
		StartedAt:           at.UTC(),
		CompletedAt:         &completed,
		Result:              map[string]int{"dependency_blocked": 1},
		Detail:              message,
		LastErrorAt:         lastErrorAt,
		ConsecutiveFailures: consecutiveFailures,
	}
	persistCtx, cancel := context.WithTimeout(context.Background(), jobRunPersistenceTimeout)
	defer cancel()
	if err := o.deps.JobRunRepo.Create(persistCtx, run); err != nil {
		o.logger.Error("automation: failed to persist dependency skip",
			slog.String("job", job.Name),
			slog.Any("error", err),
		)
		_ = o.applyRunPersistenceFailure(job, at, err)
	}
}

func (o *JobOrchestrator) beginRun(job *RegisteredJob, start time.Time) (*pgrepo.JobRun, error) {
	if o.deps.JobRunRepo == nil {
		return nil, nil
	}
	job.mu.Lock()
	lastErrorAt := job.LastErrorAt
	consecutiveFailures := job.ConsecutiveFailures
	job.mu.Unlock()
	run := &pgrepo.JobRun{
		JobName:             job.Name,
		Status:              "running",
		StartedAt:           start.UTC(),
		LastErrorAt:         lastErrorAt,
		ConsecutiveFailures: consecutiveFailures,
	}
	persistCtx, cancel := context.WithTimeout(context.Background(), jobRunPersistenceTimeout)
	defer cancel()
	if err := o.deps.JobRunRepo.Create(persistCtx, run); err != nil {
		return nil, err
	}
	return run, nil
}

func (o *JobOrchestrator) applyRunPersistenceFailure(job *RegisteredJob, at time.Time, persistErr error) error {
	err := fmt.Errorf("automation: persist run state: %w", persistErr)
	job.mu.Lock()
	job.ErrorCount++
	job.LastError = err.Error()
	job.LastDetail = ""
	job.LastErrorAt = &at
	job.ConsecutiveFailures++
	job.LastResult = "failed: run persistence"
	if job.ConsecutiveFailures >= autoDisableThreshold {
		o.autoDisableLocked(job, at, fmt.Sprintf("%d consecutive failures; last: run persistence: %s", job.ConsecutiveFailures, persistErr.Error()))
	}
	job.mu.Unlock()
	if o.metrics != nil {
		o.metrics.RecordAutomationJobError(job.Name)
	}
	return err
}

// autoDisableLocked disables a job after repeated failures, records the
// reason and cooldown expiry, and persists the decision with
// updated_by='auto-disable'. Caller holds job.mu.
func (o *JobOrchestrator) autoDisableLocked(job *RegisteredJob, at time.Time, reason string) {
	job.AutoDisableCount++
	cooldown := o.autoDisableCooldown(job.AutoDisableCount)
	until := at.Add(cooldown)
	job.Enabled = false
	job.DisabledReason = reason
	job.DisabledUntil = &until
	o.logger.Error("automation: auto-disabled job after consecutive failures",
		slog.String("job", job.Name),
		slog.Int("consecutive_failures", job.ConsecutiveFailures),
		slog.Int("auto_disable_count", job.AutoDisableCount),
		slog.Duration("cooldown", cooldown),
		slog.Time("rearm_at", until.UTC()),
		slog.String("reason", reason),
	)
	if o.deps.JobControlRepo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), jobControlPersistenceTimeout)
	defer cancel()
	var err error
	if repo, ok := o.deps.JobControlRepo.(autoDisableControlRepository); ok {
		err = repo.SetAutoDisabled(ctx, job.Name, reason, until)
	} else {
		err = o.deps.JobControlRepo.SetEnabled(ctx, job.Name, false, pgrepo.AutoDisableActor)
	}
	if err != nil {
		o.logger.Error("automation: failed to persist auto-disable", slog.String("job", job.Name), slog.Any("error", err))
	}
}

func (o *JobOrchestrator) autoDisableCooldown(count int) time.Duration {
	base := o.deps.AutoDisableCooldown
	if base <= 0 {
		base = defaultAutoDisableCooldown
	}
	cooldown := base
	for i := 1; i < count && cooldown < maxAutoDisableCooldown; i++ {
		cooldown *= 2
	}
	if cooldown > maxAutoDisableCooldown {
		cooldown = maxAutoDisableCooldown
	}
	return cooldown
}

// rearmLocked re-enables an auto-disabled job whose cooldown expired and
// persists the change with updated_by='auto-rearm'. Caller holds job.mu.
func (o *JobOrchestrator) rearmLocked(job *RegisteredJob, now time.Time) {
	o.logger.Warn("automation: re-arming auto-disabled job after cooldown",
		slog.String("job", job.Name),
		slog.Int("auto_disable_count", job.AutoDisableCount),
		slog.String("reason", job.DisabledReason),
		slog.Time("now", now.UTC()),
	)
	job.Enabled = true
	job.ConsecutiveFailures = 0
	job.DisabledReason = ""
	job.DisabledUntil = nil
	if o.deps.JobControlRepo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), jobControlPersistenceTimeout)
	defer cancel()
	if err := o.deps.JobControlRepo.SetEnabled(ctx, job.Name, true, pgrepo.AutoRearmActor); err != nil {
		o.logger.Error("automation: failed to persist auto re-arm", slog.String("job", job.Name), slog.Any("error", err))
	}
}

// handleOverlapLocked is called when a tick finds the job still running. It
// reports a run that exceeded its timeout by more than stuckGrace and, after
// abandonAfterTimeouts timeouts, releases the in-memory claim so the schedule
// can proceed. It returns true when the caller may claim a new run. Caller
// holds job.mu.
func (o *JobOrchestrator) handleOverlapLocked(job *RegisteredJob, now time.Time) bool {
	timeout := o.deps.JobTimeout
	if timeout <= 0 {
		timeout = defaultAutomationJobTimeout
	}
	var stuckFor time.Duration
	if job.StartedAt != nil {
		stuckFor = now.Sub(*job.StartedAt)
	}
	if stuckFor <= timeout+stuckGrace {
		o.logger.Warn("automation: skipping overlapping run", slog.String("job", job.Name), slog.Duration("running_for", stuckFor))
		return false
	}
	reason := fmt.Sprintf("job ignored its %s timeout; running for %s", timeout, stuckFor.Truncate(time.Second))
	if !job.stuckMarked {
		job.stuckMarked = true
		o.logger.Error("automation: job stuck past timeout",
			slog.String("job", job.Name),
			slog.Duration("stuck_for", stuckFor),
			slog.Duration("timeout", timeout),
		)
		if marker, ok := o.deps.JobRunRepo.(stuckRunMarker); ok && job.currentRunID != uuid.Nil {
			ctx, cancel := context.WithTimeout(context.Background(), jobRunPersistenceTimeout)
			err := marker.MarkStuck(ctx, job.currentRunID, now.UTC(), reason)
			cancel()
			if err != nil {
				o.logger.Error("automation: failed to mark job run stuck", slog.String("job", job.Name), slog.Any("error", err))
			}
		}
	}
	if stuckFor < time.Duration(abandonAfterTimeouts)*timeout {
		o.logger.Error("automation: skipping overlapping run of stuck job", slog.String("job", job.Name), slog.Duration("stuck_for", stuckFor))
		return false
	}
	o.logger.Error("automation: abandoning wedged run so the schedule can proceed",
		slog.String("job", job.Name),
		slog.Duration("stuck_for", stuckFor),
	)
	job.Running = false
	job.StartedAt = nil
	job.currentRunID = uuid.Nil
	job.LastError = reason
	job.LastErrorAt = &now
	job.ErrorCount++
	return true
}

func (o *JobOrchestrator) setCurrentRun(job *RegisteredJob, seq uint64, run *pgrepo.JobRun) {
	if run == nil {
		return
	}
	job.mu.Lock()
	if job.runSeq == seq {
		job.currentRunID = run.ID
	}
	job.mu.Unlock()
}

func (o *JobOrchestrator) completeRun(run *pgrepo.JobRun, job *RegisteredJob, completedAt time.Time, elapsed time.Duration, jobErr error) error {
	if o.deps.JobRunRepo == nil || run == nil {
		return nil
	}

	status := "ok"
	var errMsg string
	var detail string
	if IsDegraded(jobErr) {
		status = "degraded"
		detail = jobErr.Error()
	} else if jobErr != nil {
		status = "error"
		errMsg = jobErr.Error()
	}

	var lastErrorAt *time.Time
	var consecutiveFailures int
	var result map[string]int
	if job != nil {
		job.mu.Lock()
		lastErrorAt = job.LastErrorAt
		consecutiveFailures = job.ConsecutiveFailures
		result = cloneSummary(job.LastSummary)
		job.mu.Unlock()
	}

	run.Status = status
	completedAt = completedAt.UTC()
	run.CompletedAt = &completedAt
	run.DurationNs = elapsed.Nanoseconds()
	run.Result = result
	if job != nil && job.Name == "current_data_refresh" && result["closing_mode"] != 1 && (status == "ok" || status == "degraded") {
		run.Tickers = append([]string{}, o.getRefreshedTickers()...)
	}
	run.Error = errMsg
	run.Detail = detail
	if status == "degraded" {
		run.LastErrorAt = nil
		run.ConsecutiveFailures = 0
	} else {
		run.LastErrorAt = lastErrorAt
		run.ConsecutiveFailures = consecutiveFailures
	}

	persistCtx, cancel := context.WithTimeout(context.Background(), jobRunPersistenceTimeout)
	defer cancel()
	return o.deps.JobRunRepo.Complete(persistCtx, run)
}

// hydrateFromDB loads historical run stats from the database to restore
// counters after a server restart. Persistence reads are retried; if they
// still fail the orchestrator is marked degraded and keeps its jobs enabled
// (run history is read-only state). Enablement comes only from
// automation_job_controls, never from a run row's failure counter.
func (o *JobOrchestrator) hydrateFromDB() {
	if o.deps.JobRunRepo != nil {
		recoveryAt := time.Now().UTC()
		const recoveryReason = "automation process restarted before the job persisted a terminal outcome"
		var recovered int
		recoveryErr := o.withHydrationRetry("recover incomplete job runs", jobRunPersistenceTimeout, func(ctx context.Context) error {
			n, err := o.deps.JobRunRepo.FailIncomplete(ctx, recoveryAt, recoveryReason)
			recovered = n
			return err
		})
		if recoveryErr == nil && recovered > 0 {
			o.logger.Warn("automation: recovered incomplete job runs", slog.Int("runs", recovered))
		}
	}
	if o.deps.KalshiSettlementGateRepo != nil {
		if state, err := o.deps.KalshiSettlementGateRepo.Get(context.Background(), "kalshi_settlement"); err == nil {
			if job, ok := o.jobs["kalshi_settlement"]; ok {
				job.mu.Lock()
				job.SettlementGate = &SettlementGateStatus{ConsecutiveSuccesses: state.ConsecutiveSuccesses, Threshold: state.Threshold, Eligible: state.Eligible, ProjectionFingerprint: state.ProjectionFingerprint, LastOutcome: state.LastOutcome, LastError: state.LastError, LastRunAt: state.LastRunAt, Fetched: state.Fetched, Resolved: state.Resolved, WouldSettleMarkets: state.WouldSettleMarkets, WouldSettleDecisions: state.WouldSettleDecisions}
				job.mu.Unlock()
				o.kalshiGateUnhealthy = false
			}
		} else if !errors.Is(err, repository.ErrNotFound) {
			o.logger.Warn("automation: failed to hydrate kalshi settlement gate", slog.Any("error", err))
			o.kalshiGateUnhealthy = true
		}
	}

	if o.deps.JobRunRepo != nil {
		var summaries []pgrepo.JobRunSummary
		err := o.withHydrationRetry("hydrate job stats", jobRunPersistenceTimeout, func(ctx context.Context) error {
			loaded, err := o.deps.JobRunRepo.Summaries(ctx)
			summaries = loaded
			return err
		})
		if err == nil {
			o.applyRunSummaries(summaries)
		}
	}

	// Explicit durable controls are the only source of enablement.
	o.hydrateJobControls()
}

// withHydrationRetry runs fn up to hydrateAttempts times with linear backoff.
// A final failure marks the orchestrator degraded and is logged at ERROR.
func (o *JobOrchestrator) withHydrationRetry(op string, timeout time.Duration, fn func(context.Context) error) error {
	var err error
	for attempt := 1; attempt <= hydrateAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		err = fn(ctx)
		cancel()
		if err == nil {
			return nil
		}
		if attempt < hydrateAttempts {
			o.logger.Warn("automation: hydration step failed; retrying",
				slog.String("step", op),
				slog.Int("attempt", attempt),
				slog.Any("error", err),
			)
			time.Sleep(o.hydrateRetryBackoff * time.Duration(attempt))
		}
	}
	reason := fmt.Sprintf("%s: %v", op, err)
	o.markDegraded(reason)
	o.logger.Error("automation: hydration step failed after retries; continuing degraded",
		slog.String("step", op),
		slog.Int("attempts", hydrateAttempts),
		slog.Any("error", err),
	)
	return err
}

func (o *JobOrchestrator) applyRunSummaries(summaries []pgrepo.JobRunSummary) {
	for _, s := range summaries {
		job, ok := o.jobs[s.JobName]
		if !ok {
			continue
		}
		dependencySkipped := isDependencySkippedOutcome(s.LastResult, s.LastDetail)
		job.mu.Lock()
		job.LastRun = s.LastRun
		job.LastResult = s.LastResult
		job.LastError = s.LastError
		job.LastDetail = s.LastDetail
		job.LastSummary = cloneSummary(s.LastSummary)
		if dependencySkipped && strings.EqualFold(strings.TrimSpace(s.LastResult), "skipped") && s.LastDetail != "" {
			job.LastResult = "skipped: " + s.LastDetail
			job.LastError = ""
		}
		job.LastErrorAt = s.LastErrorAt
		job.RunCount = s.RunCount
		job.ErrorCount = s.ErrorCount
		job.ConsecutiveFailures = s.ConsecutiveFailures
		if strings.EqualFold(strings.TrimSpace(s.LastResult), "degraded") {
			job.LastError = ""
			job.LastErrorAt = nil
			job.ConsecutiveFailures = 0
		}
		job.mu.Unlock()
		if s.JobName == "current_data_refresh" && s.LastSummary["closing_mode"] != 1 {
			o.setRefreshedTickers(s.LastTickers)
		}
	}

	o.logger.Info("automation: hydrated job stats from DB", slog.Int("jobs", len(summaries)))
}

func (o *JobOrchestrator) hydrateJobControls() {
	if o.deps.JobControlRepo == nil {
		return
	}
	var controls []pgrepo.AutomationJobControlDetail
	err := o.withHydrationRetry("hydrate durable job controls", jobControlPersistenceTimeout, func(ctx context.Context) error {
		if lister, ok := o.deps.JobControlRepo.(detailedControlLister); ok {
			loaded, err := lister.ListDetailed(ctx)
			controls = loaded
			return err
		}
		loaded, err := o.deps.JobControlRepo.List(ctx)
		if err != nil {
			return err
		}
		controls = controls[:0]
		for _, control := range loaded {
			controls = append(controls, pgrepo.AutomationJobControlDetail{AutomationJobControl: control})
		}
		return nil
	})
	if err != nil {
		// Fail open: without the control rows we cannot tell which jobs an
		// operator disabled, and disabling everything silently has proven
		// worse than running. Health() reports the degradation.
		return
	}
	for _, control := range controls {
		job, ok := o.jobs[control.JobName]
		if !ok {
			continue
		}
		job.mu.Lock()
		job.Enabled = control.Enabled
		job.DisabledReason = ""
		job.DisabledUntil = nil
		if !control.Enabled {
			job.DisabledReason = control.Reason
			if control.UpdatedBy == pgrepo.AutoDisableActor {
				job.DisabledUntil = cloneTime(control.AutoDisabledUntil)
				if job.AutoDisableCount == 0 {
					job.AutoDisableCount = 1
				}
			}
		}
		job.mu.Unlock()
	}
}

// catchUpMissedRuns runs, once and sequentially, every enabled daily-or-less-
// frequent job whose most recent scheduled fire happened after its last run,
// provided the schedule's session gate allows running now.
func (o *JobOrchestrator) catchUpMissedRuns() {
	ctx, lease, err := o.runs.Admit(context.Background())
	if err != nil {
		return
	}
	defer lease.Done()
	delay := o.deps.MissedRunCatchUpDelay
	if delay <= 0 {
		delay = defaultMissedRunCatchUpDelay
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(delay):
	}
	for _, name := range o.RegisteredJobKeys() {
		if ctx.Err() != nil {
			return
		}
		job := o.jobs[name]
		now := o.currentTime()
		missedAt, missed := o.missedScheduledRun(job, now)
		if !missed {
			continue
		}
		if err := claimManualJob(job, now); err != nil {
			o.logger.Info("automation: missed-run catch-up not admitted", slog.String("job", name), slog.Any("error", err))
			continue
		}
		o.logger.Warn("automation: running missed scheduled job at startup",
			slog.String("job", name),
			slog.Time("missed_fire_at", missedAt.UTC()),
		)
		o.runClaimedDirect(ctx, job, now)
	}
}

// missedScheduledRun reports whether the job's latest scheduled fire time is
// newer than its last run and the job may run now.
func (o *JobOrchestrator) missedScheduledRun(job *RegisteredJob, now time.Time) (time.Time, bool) {
	if !isDailyOrLessFrequent(job.Schedule.Cron) {
		return time.Time{}, false
	}
	job.mu.Lock()
	enabled, running, lastRun, lastResult := job.Enabled, job.Running, cloneTime(job.LastRun), job.LastResult
	job.mu.Unlock()
	if !enabled || running || !job.Schedule.ShouldFire(now) {
		return time.Time{}, false
	}
	prev, ok := previousScheduledFire(job.Schedule.Cron, now)
	if !ok {
		return time.Time{}, false
	}
	if lastRun != nil && successfulJobResult(lastResult) && !lastRun.Before(prev) {
		return time.Time{}, false
	}
	return prev, true
}

// isDailyOrLessFrequent reports whether a five-field cron spec fires at most
// once per day (fixed minute and hour).
func isDailyOrLessFrequent(spec string) bool {
	fields := strings.Fields(stripCronTZ(spec))
	if len(fields) != 5 {
		return false
	}
	return isCronInteger(fields[0]) && isCronInteger(fields[1])
}

func isCronInteger(field string) bool {
	if field == "" {
		return false
	}
	for _, r := range field {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func stripCronTZ(spec string) string {
	spec = strings.TrimSpace(spec)
	if strings.HasPrefix(spec, "CRON_TZ=") || strings.HasPrefix(spec, "TZ=") {
		if i := strings.IndexAny(spec, " \t"); i >= 0 {
			return strings.TrimSpace(spec[i:])
		}
	}
	return spec
}

// previousScheduledFire returns the most recent fire time at or before now for
// a standard cron spec evaluated in Eastern time.
func previousScheduledFire(spec string, now time.Time) (time.Time, bool) {
	schedule, err := cron.ParseStandard(spec)
	if err != nil {
		return time.Time{}, false
	}
	cursor := now.In(easternTime).Add(-missedRunLookback)
	var prev time.Time
	for i := 0; i < 400; i++ {
		next := schedule.Next(cursor)
		if next.IsZero() || next.After(now) {
			break
		}
		prev = next
		cursor = next
	}
	return prev, !prev.IsZero()
}

func cloneTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	clone := *t
	return &clone
}

func isDependencySkippedOutcome(result, detail string) bool {
	normalizedResult := strings.ToLower(strings.TrimSpace(result))
	if strings.HasPrefix(normalizedResult, "skipped: dependency ") {
		return true
	}
	return normalizedResult == "skipped" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(detail)), "dependency ")
}

func cloneSummary(summary map[string]int) map[string]int {
	if len(summary) == 0 {
		return nil
	}
	cloned := make(map[string]int, len(summary))
	for key, value := range summary {
		cloned[key] = value
	}
	return cloned
}

func cloneSettlementGateStatus(s *SettlementGateStatus) *SettlementGateStatus {
	if s == nil {
		return nil
	}
	clone := *s
	return &clone
}
