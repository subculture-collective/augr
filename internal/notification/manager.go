package notification

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/config"
)

const defaultDedupInterval = 15 * time.Minute

type llmRequestSample struct {
	occurredAt time.Time
	success    bool
}

// Manager evaluates alert rules and dispatches alerts to configured notifiers.
type Manager struct {
	mu              sync.Mutex
	rules           config.AlertRulesConfig
	notifiers       map[string]Notifier
	logger          *slog.Logger
	dedupInterval   time.Duration
	pipelineFailure int
	active          map[string]bool
	recent          map[string]time.Time
	llmSamples      map[string][]llmRequestSample
}

// NewManager returns an alert manager that uses the supplied rules and channel notifiers.
func NewManager(rules config.AlertRulesConfig, notifiers map[string]Notifier) *Manager {
	clonedNotifiers := make(map[string]Notifier, len(notifiers))
	for channel, notifier := range notifiers {
		clonedNotifiers[channel] = notifier
	}
	return &Manager{
		rules:         rules,
		notifiers:     clonedNotifiers,
		logger:        slog.Default(),
		dedupInterval: defaultDedupInterval,
		active:        map[string]bool{},
		recent:        map[string]time.Time{},
		llmSamples:    map[string][]llmRequestSample{},
	}
}

// WithLogger sets the logger used for skipped-channel diagnostics.
func (m *Manager) WithLogger(logger *slog.Logger) *Manager {
	if logger != nil {
		m.logger = logger
	}
	return m
}

// ConfiguredChannels returns the channel names that have a notifier.
func (m *Manager) ConfiguredChannels() []string {
	channels := make([]string, 0, len(m.notifiers))
	for channel := range m.notifiers {
		channels = append(channels, channel)
	}
	return normalizeChannels(channels)
}

// RecordPipelineResult tracks pipeline successes and failures and alerts on consecutive failures.
func (m *Manager) RecordPipelineResult(ctx context.Context, success bool, occurredAt time.Time) error {
	m.mu.Lock()
	if success {
		m.pipelineFailure = 0
		delete(m.active, "pipeline_failure")
		m.mu.Unlock()
		return nil
	}

	m.pipelineFailure++
	failures := m.pipelineFailure
	if failures < m.rules.PipelineFailure.Threshold || m.active["pipeline_failure"] {
		m.mu.Unlock()
		return nil
	}
	m.active["pipeline_failure"] = true
	channels := append([]string(nil), m.rules.PipelineFailure.Channels...)
	m.mu.Unlock()

	alert := Alert{
		Key:        "pipeline_failure",
		Title:      "Pipeline failure threshold reached",
		Body:       fmt.Sprintf("Pipeline execution failed %d consecutive times.", failures),
		Severity:   SeverityCritical,
		OccurredAt: normalizeOccurredAt(occurredAt),
		Metadata: map[string]string{
			"consecutive_failures": strconv.Itoa(failures),
		},
	}

	if err := m.dispatch(ctx, alert, channels); err != nil {
		m.mu.Lock()
		delete(m.active, "pipeline_failure")
		m.mu.Unlock()
		return err
	}

	return nil
}

// RecordCircuitBreakerTrip dispatches an immediate circuit-breaker alert.
func (m *Manager) RecordCircuitBreakerTrip(ctx context.Context, reason string, occurredAt time.Time) error {
	if !m.markActive("circuit_breaker") {
		return nil
	}

	alert := Alert{
		Key:        "circuit_breaker",
		Title:      "Circuit breaker tripped",
		Body:       reason,
		Severity:   SeverityCritical,
		OccurredAt: normalizeOccurredAt(occurredAt),
	}
	if err := m.dispatch(ctx, alert, m.rules.CircuitBreaker.Channels); err != nil {
		m.clearActive("circuit_breaker")
		return err
	}
	return nil
}

// RecordCircuitBreakerReset clears the circuit-breaker deduplication state.
func (m *Manager) RecordCircuitBreakerReset() {
	m.clearActive("circuit_breaker")
}

// RecordLLMRequest evaluates rolling provider health and alerts when the failure rate stays above threshold.
func (m *Manager) RecordLLMRequest(ctx context.Context, provider string, success bool, occurredAt time.Time) error {
	occurredAt = normalizeOccurredAt(occurredAt)

	m.mu.Lock()
	window := m.rules.LLMProviderDown.Window
	samples := append([]llmRequestSample(nil), m.llmSamples[provider]...)
	samples = append(samples, llmRequestSample{occurredAt: occurredAt, success: success})
	cutoff := occurredAt.Add(-window)
	filtered := samples[:0]
	for _, sample := range samples {
		if !sample.occurredAt.Before(cutoff) {
			filtered = append(filtered, sample)
		}
	}
	m.llmSamples[provider] = filtered

	total := len(filtered)
	failures := 0
	for _, sample := range filtered {
		if !sample.success {
			failures++
		}
	}

	activeKey := "llm_provider_down:" + provider
	errorRate := 0.0
	if total > 0 {
		errorRate = float64(failures) / float64(total)
	}

	if errorRate <= m.rules.LLMProviderDown.ErrorRateThreshold {
		delete(m.active, activeKey)
		m.mu.Unlock()
		return nil
	}

	if m.active[activeKey] {
		m.mu.Unlock()
		return nil
	}
	m.active[activeKey] = true
	channels := append([]string(nil), m.rules.LLMProviderDown.Channels...)
	m.mu.Unlock()

	alert := Alert{
		Key:        activeKey,
		Title:      "LLM provider error rate exceeded threshold",
		Body:       fmt.Sprintf("Provider %s exceeded the configured error-rate threshold over the last %s.", provider, window),
		Severity:   SeverityCritical,
		OccurredAt: occurredAt,
		Metadata: map[string]string{
			"provider":    provider,
			"error_rate":  fmt.Sprintf("%.2f", errorRate),
			"sample_size": strconv.Itoa(total),
		},
	}

	if err := m.dispatch(ctx, alert, channels); err != nil {
		m.clearActive(activeKey)
		return err
	}
	return nil
}

// RecordPipelineLatency alerts when a pipeline run exceeds the configured duration.
func (m *Manager) RecordPipelineLatency(ctx context.Context, latency time.Duration, occurredAt time.Time) error {
	if latency <= m.rules.HighLatency.Threshold {
		return nil
	}

	dedupKey := "high_latency"
	occurredAt = normalizeOccurredAt(occurredAt)
	if !m.acquireRecent(dedupKey, occurredAt) {
		return nil
	}

	alert := Alert{
		Key:        dedupKey,
		Title:      "Pipeline latency threshold exceeded",
		Body:       fmt.Sprintf("Pipeline latency reached %s, above the configured threshold of %s.", latency, m.rules.HighLatency.Threshold),
		Severity:   SeverityWarning,
		OccurredAt: occurredAt,
		Metadata: map[string]string{
			"latency":   latency.String(),
			"threshold": m.rules.HighLatency.Threshold.String(),
		},
	}

	if err := m.dispatch(ctx, alert, m.rules.HighLatency.Channels); err != nil {
		m.clearRecent(dedupKey)
		return err
	}
	return nil
}

// RecordKillSwitchToggle emits an immediate alert when the kill switch is toggled.
func (m *Manager) RecordKillSwitchToggle(ctx context.Context, active bool, reason string, occurredAt time.Time) error {
	state := "disabled"
	key := "kill_switch_disabled"
	severity := SeverityInfo
	if active {
		state = "enabled"
		key = "kill_switch_enabled"
		severity = SeverityCritical
	}

	occurredAt = normalizeOccurredAt(occurredAt)
	if !m.acquireRecent(key, occurredAt) {
		return nil
	}

	alert := Alert{
		Key:        key,
		Title:      "Kill switch toggled",
		Body:       fmt.Sprintf("Kill switch was %s. %s", state, reason),
		Severity:   severity,
		OccurredAt: occurredAt,
	}

	if err := m.dispatch(ctx, alert, m.rules.KillSwitch.Channels); err != nil {
		m.clearRecent(key)
		return err
	}
	return nil
}

// RecordStrategyPreparationRejected alerts operators that a scheduled strategy
// was rejected in preflight. It routes through the pipeline-failure channels
// and deduplicates per strategy and reason code for the dedup interval, so a
// strategy rejected on every scheduled run produces one alert per interval
// rather than one per run.
func (m *Manager) RecordStrategyPreparationRejected(ctx context.Context, strategyID, name, ticker, reasonCode string, occurredAt time.Time) error {
	strategyID = strings.TrimSpace(strategyID)
	name = strings.TrimSpace(name)
	ticker = strings.TrimSpace(ticker)
	reasonCode = strings.TrimSpace(reasonCode)
	if reasonCode == "" {
		reasonCode = "unspecified"
	}
	key := "preparation_rejected:" + strategyID + ":" + reasonCode
	occurredAt = normalizeOccurredAt(occurredAt)
	if !m.acquireRecent(key, occurredAt) {
		return nil
	}

	label := name
	if label == "" {
		label = strategyID
	}
	alert := Alert{
		Key:        key,
		Title:      "Strategy preparation rejected",
		Body:       fmt.Sprintf("Strategy %s (%s) was rejected in preflight: %s. The strategy will not trade until the cause is fixed.", label, ticker, reasonCode),
		Severity:   SeverityWarning,
		OccurredAt: occurredAt,
		Metadata: map[string]string{
			"strategy_id":   strategyID,
			"strategy_name": name,
			"ticker":        ticker,
			"reason_code":   reasonCode,
		},
	}
	if err := m.dispatch(ctx, alert, m.rules.PipelineFailure.Channels); err != nil {
		m.clearRecent(key)
		return err
	}
	return nil
}

// NotifyStrategyPreparationRejected is the UUID-typed form of
// RecordStrategyPreparationRejected.
func (m *Manager) NotifyStrategyPreparationRejected(ctx context.Context, strategyID uuid.UUID, ticker, reasonCode string) error {
	return m.RecordStrategyPreparationRejected(ctx, uuidString(strategyID), "", ticker, reasonCode, time.Time{})
}

// NotifyAutomationDegraded alerts once per degradation that the automation
// orchestrator disabled jobs (for example after a startup recovery failure).
// Call ClearAutomationDegraded when the orchestrator recovers.
func (m *Manager) NotifyAutomationDegraded(ctx context.Context, reason string, disabledJobs []string) error {
	const activeKey = "automation_degraded"
	if !m.markActive(activeKey) {
		return nil
	}

	jobs := normalizeChannels(disabledJobs)
	body := strings.TrimSpace(reason)
	if body == "" {
		body = "The automation orchestrator is degraded."
	}
	if len(jobs) > 0 {
		body += " Disabled jobs: " + strings.Join(jobs, ", ") + "."
	}
	alert := Alert{
		Key:        activeKey,
		Title:      "Automation orchestrator degraded",
		Body:       body,
		Severity:   SeverityCritical,
		OccurredAt: normalizeOccurredAt(time.Time{}),
		Metadata: map[string]string{
			"disabled_jobs":      strings.Join(jobs, ","),
			"disabled_job_count": strconv.Itoa(len(jobs)),
		},
	}
	if err := m.dispatch(ctx, alert, m.rules.PipelineFailure.Channels); err != nil {
		m.clearActive(activeKey)
		return err
	}
	return nil
}

// ClearAutomationDegraded re-arms the automation degraded alert.
func (m *Manager) ClearAutomationDegraded() {
	m.clearActive("automation_degraded")
}

// RecordDBConnectionState alerts once per outage and resets once the database recovers.
func (m *Manager) RecordDBConnectionState(ctx context.Context, connected bool, connErr error, occurredAt time.Time) error {
	const activeKey = "db_connection_loss"

	if connected {
		m.clearActive(activeKey)
		return nil
	}

	if !m.markActive(activeKey) {
		return nil
	}

	alert := Alert{
		Key:        activeKey,
		Title:      "Database connection lost",
		Body:       "The application could not reach the configured database.",
		Severity:   SeverityCritical,
		OccurredAt: normalizeOccurredAt(occurredAt),
	}
	if connErr != nil {
		alert.Metadata = map[string]string{"error": connErr.Error()}
	}

	if err := m.dispatch(ctx, alert, m.rules.DBConnection.Channels); err != nil {
		m.clearActive(activeKey)
		return err
	}
	return nil
}

// RecordSignal dispatches a structured signal notification to supported channels.
func (m *Manager) RecordSignal(ctx context.Context, event SignalEvent) error {
	event.OccurredAt = normalizeOccurredAt(event.OccurredAt)
	return m.dispatchSignal(ctx, event, []string{ChannelN8N, ChannelDiscord})
}

// RecordDecision dispatches a structured decision notification to supported channels.
func (m *Manager) RecordDecision(ctx context.Context, event DecisionEvent) error {
	event.OccurredAt = normalizeOccurredAt(event.OccurredAt)
	return m.dispatchDecision(ctx, event, []string{ChannelN8N, ChannelDiscord})
}

func (m *Manager) dispatchSignal(ctx context.Context, event SignalEvent, channels []string) error {
	channels = normalizeChannels(channels)
	if len(channels) == 0 {
		return nil
	}

	var errs []error
	for _, channel := range channels {
		notifier, ok := m.notifiers[channel]
		if !ok {
			continue
		}
		signalNotifier, ok := notifier.(SignalNotifier)
		if !ok {
			errs = append(errs, fmt.Errorf("%s notifier does not support signal delivery", channel))
			continue
		}
		if err := signalNotifier.NotifySignal(ctx, event); err != nil {
			errs = append(errs, fmt.Errorf("%s notifier: %w", channel, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) dispatchDecision(ctx context.Context, event DecisionEvent, channels []string) error {
	channels = normalizeChannels(channels)
	if len(channels) == 0 {
		return nil
	}

	var errs []error
	for _, channel := range channels {
		notifier, ok := m.notifiers[channel]
		if !ok {
			continue
		}
		decisionNotifier, ok := notifier.(DecisionNotifier)
		if !ok {
			errs = append(errs, fmt.Errorf("%s notifier does not support decision delivery", channel))
			continue
		}
		if err := decisionNotifier.NotifyDecision(ctx, event); err != nil {
			errs = append(errs, fmt.Errorf("%s notifier: %w", channel, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) dispatch(ctx context.Context, alert Alert, channels []string) error {
	channels = normalizeChannels(channels)
	if len(channels) == 0 {
		return nil
	}

	var errs []error
	for _, channel := range channels {
		notifier, ok := m.notifiers[channel]
		if !ok {
			// A channel routed by ALERT_*_CHANNELS but without credentials is
			// a configuration gap, not a delivery failure: skip it quietly so
			// the alert still reaches the configured channels.
			m.logger.DebugContext(ctx, "notification: channel not configured, skipping",
				slog.String("channel", channel),
				slog.String("alert", alert.Key))
			continue
		}
		if err := notifier.Notify(ctx, alert); err != nil {
			errs = append(errs, fmt.Errorf("%s notifier: %w", channel, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) markActive(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active[key] {
		return false
	}
	m.active[key] = true
	return true
}

func (m *Manager) clearActive(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.active, key)
}

func (m *Manager) acquireRecent(key string, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if last, ok := m.recent[key]; ok && now.Sub(last) < m.dedupInterval {
		return false
	}
	m.recent[key] = now
	return true
}

func (m *Manager) clearRecent(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.recent, key)
}

func normalizeOccurredAt(occurredAt time.Time) time.Time {
	if occurredAt.IsZero() {
		return time.Now().UTC()
	}
	return occurredAt.UTC()
}
