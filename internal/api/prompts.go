package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/PatrickFanella/get-rich-quick/internal/agent"
	agentanalysts "github.com/PatrickFanella/get-rich-quick/internal/agent/analysts"
	agentdebate "github.com/PatrickFanella/get-rich-quick/internal/agent/debate"
	agentrisk "github.com/PatrickFanella/get-rich-quick/internal/agent/risk"
	agenttrader "github.com/PatrickFanella/get-rich-quick/internal/agent/trader"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// PromptPersister stores system-wide prompt overrides.
type PromptPersister interface {
	LoadPromptOverrides(ctx context.Context) (map[domain.AgentRole]string, error)
	SavePromptOverrides(ctx context.Context, overrides map[domain.AgentRole]string) error
}

// PromptDefinition describes an editable built-in prompt.
type PromptDefinition struct {
	Key           string `json:"key"`
	Label         string `json:"label"`
	Description   string `json:"description"`
	Category      string `json:"category"`
	DefaultText   string `json:"default_text"`
	OverrideText  string `json:"override_text"`
	EffectiveText string `json:"effective_text"`
	Overridden    bool   `json:"overridden"`
}

type promptRegistryEntry struct {
	Role        domain.AgentRole
	Label       string
	Description string
	Category    string
	DefaultText string
}

// PromptSettingsResponse is the payload for the prompt editor.
type PromptSettingsResponse struct {
	Prompts []PromptDefinition `json:"prompts"`
}

// PromptSettingsUpdateRequest updates system-wide prompt overrides by role key.
type PromptSettingsUpdateRequest struct {
	Overrides map[string]string `json:"overrides"`
}

// PromptSettingsService manages runtime prompt overrides.
type PromptSettingsService struct {
	mu        sync.RWMutex
	registry  []promptRegistryEntry
	overrides map[domain.AgentRole]string
	persister PromptPersister
}

func NewPromptSettingsService() *PromptSettingsService {
	return &PromptSettingsService{registry: defaultPromptRegistry(), overrides: map[domain.AgentRole]string{}}
}

func (s *PromptSettingsService) WithPersister(ctx context.Context, persister PromptPersister) *PromptSettingsService {
	s.persister = persister
	if persister == nil {
		return s
	}
	overrides, err := persister.LoadPromptOverrides(ctx)
	if err == nil {
		s.mu.Lock()
		s.overrides = sanitizePromptOverrides(overrides, s.validRoles())
		s.mu.Unlock()
	}
	return s
}

func (s *PromptSettingsService) Get(ctx context.Context) (PromptSettingsResponse, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()

	return PromptSettingsResponse{Prompts: s.definitionsLocked()}, nil
}

func (s *PromptSettingsService) Update(ctx context.Context, req PromptSettingsUpdateRequest) (PromptSettingsResponse, error) {
	validRoles := s.validRoles()
	next := make(map[domain.AgentRole]string, len(req.Overrides))
	for key, value := range req.Overrides {
		role := domain.AgentRole(strings.TrimSpace(key))
		if _, ok := validRoles[role]; !ok {
			return PromptSettingsResponse{}, fmt.Errorf("unknown prompt key %q", key)
		}
		if text := strings.TrimSpace(value); text != "" {
			next[role] = text
		}
	}

	if s.persister != nil {
		if err := s.persister.SavePromptOverrides(ctx, next); err != nil {
			return PromptSettingsResponse{}, err
		}
	}

	s.mu.Lock()
	s.overrides = next
	resp := PromptSettingsResponse{Prompts: s.definitionsLocked()}
	s.mu.Unlock()

	return resp, nil
}

func (s *PromptSettingsService) Overrides() map[agent.AgentRole]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.overrides) == 0 {
		return nil
	}
	out := make(map[agent.AgentRole]string, len(s.overrides))
	for role, prompt := range s.overrides {
		out[agent.AgentRole(role)] = prompt
	}
	return out
}

func (s *PromptSettingsService) definitionsLocked() []PromptDefinition {
	defs := make([]PromptDefinition, 0, len(s.registry))
	for _, entry := range s.registry {
		override := strings.TrimSpace(s.overrides[entry.Role])
		def := PromptDefinition{
			Key:           entry.Role.String(),
			Label:         entry.Label,
			Description:   entry.Description,
			Category:      entry.Category,
			DefaultText:   entry.DefaultText,
			OverrideText:  override,
			EffectiveText: entry.DefaultText,
			Overridden:    override != "",
		}
		if def.Overridden {
			def.EffectiveText = override
		}
		defs = append(defs, def)
	}
	return defs
}

func (s *PromptSettingsService) validRoles() map[domain.AgentRole]struct{} {
	valid := make(map[domain.AgentRole]struct{}, len(s.registry))
	for _, entry := range s.registry {
		valid[entry.Role] = struct{}{}
	}
	return valid
}

func sanitizePromptOverrides(overrides map[domain.AgentRole]string, validRoles map[domain.AgentRole]struct{}) map[domain.AgentRole]string {
	out := make(map[domain.AgentRole]string, len(overrides))
	for role, prompt := range overrides {
		if _, ok := validRoles[role]; ok {
			if text := strings.TrimSpace(prompt); text != "" {
				out[role] = text
			}
		}
	}
	return out
}

func defaultPromptRegistry() []promptRegistryEntry {
	entries := []promptRegistryEntry{
		{domain.AgentRoleMarketAnalyst, "Market analyst", "Technical analysis of OHLCV, indicators, trend, momentum, volatility, and volume.", "Analysis", agentanalysts.MarketAnalystSystemPrompt},
		{domain.AgentRoleFundamentalsAnalyst, "Fundamentals analyst", "Financial health, valuation, growth, dividends, and intrinsic value assessment.", "Analysis", agentanalysts.FundamentalsAnalystSystemPrompt},
		{domain.AgentRoleNewsAnalyst, "News analyst", "News sentiment, catalysts, macro context, and risk flags.", "Analysis", agentanalysts.NewsAnalystSystemPrompt},
		{domain.AgentRoleSocialMediaAnalyst, "Social sentiment analyst", "Retail and social-media sentiment analysis.", "Analysis", agentanalysts.SocialAnalystSystemPrompt},
		{domain.AgentRoleBullResearcher, "Bull researcher", "Builds the strongest bullish case during research debate.", "Research debate", agentdebate.BullResearcherSystemPrompt},
		{domain.AgentRoleBearResearcher, "Bear researcher", "Builds the strongest bearish case during research debate.", "Research debate", agentdebate.BearResearcherSystemPrompt},
		{domain.AgentRoleInvestJudge, "Investment judge", "Balances bull and bear arguments into an investment plan.", "Research debate", agentdebate.ResearchManagerSystemPrompt},
		{domain.AgentRoleTrader, "Trader", "Converts the investment plan into executable trading parameters.", "Trading", agenttrader.TraderSystemPrompt},
		{domain.AgentRoleAggressiveAnalyst, "Aggressive risk analyst", "Argues for return-maximizing risk in the risk debate.", "Risk debate", agentrisk.AggressiveRiskSystemPrompt},
		{domain.AgentRoleConservativeAnalyst, "Conservative risk analyst", "Argues for capital preservation in the risk debate.", "Risk debate", agentrisk.ConservativeRiskSystemPrompt},
		{domain.AgentRoleNeutralAnalyst, "Neutral risk analyst", "Provides balanced risk assessment in the risk debate.", "Risk debate", agentrisk.NeutralRiskSystemPrompt},
		{domain.AgentRoleRiskManager, "Risk manager", "Final risk judge that produces the final BUY/SELL/HOLD signal.", "Risk debate", agentrisk.RiskManagerSystemPrompt},
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Category == entries[j].Category {
			return entries[i].Label < entries[j].Label
		}
		return entries[i].Category < entries[j].Category
	})
	return entries
}

func (s *Server) handleGetPrompts(w http.ResponseWriter, r *http.Request) {
	resp, err := s.prompts.Get(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error(), ErrCodeInternal)
		return
	}
	respondJSON(w, http.StatusOK, resp)
}

func (s *Server) handleUpdatePrompts(w http.ResponseWriter, r *http.Request) {
	var req PromptSettingsUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body", ErrCodeBadRequest)
		return
	}
	resp, err := s.prompts.Update(r.Context(), req)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error(), ErrCodeValidation)
		return
	}
	respondJSON(w, http.StatusOK, resp)
}
