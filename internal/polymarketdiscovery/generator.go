package polymarketdiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/PatrickFanella/get-rich-quick/internal/llm"
)

// GeneratorConfig controls LLM strategy generation.
type GeneratorConfig struct {
	Provider   llm.Provider
	Model      string
	MaxRetries int // default 2
}

// Proposal is the LLM's strategy proposal for a single market.
type Proposal struct {
	Template     StrategyTemplate `json:"template"`
	Skip         bool             `json:"skip,omitempty"`
	SkipReason   string           `json:"skip_reason,omitempty"`
	Name         string           `json:"name"`
	Summary      string           `json:"summary"`
	Direction    string           `json:"direction"`   // "YES" or "NO"
	Conviction   float64          `json:"conviction"`  // 0..1
	TimeHorizon  string           `json:"time_horizon"` // "hours"|"days"|"weeks"
	EntryPriceMax float64         `json:"entry_price_max,omitempty"` // YES price ceiling for buys
	WatchTerms   []string         `json:"watch_terms"`
	InvalidateIf []string         `json:"invalidate_if"`
}

const generatorSystemPrompt = `You are a senior prediction-market trading strategist. Given a single Polymarket market and supporting evidence, decide whether there is a credible edge to trade and, if so, produce a structured proposal.

You MUST output a single JSON object with this schema:
{
  "template": "<one of: whale_copy, arbitrage, news_catalyst, microstructure, convergence, volume_divergence, resolution_edge, mean_reversion, calendar_event, anti_favorite>",
  "skip": <true|false>,
  "skip_reason": "<required if skip=true; short reason>",
  "name": "<5-10 word strategy name>",
  "summary": "<2-4 sentences: thesis, why it works, what evidence supports it>",
  "direction": "<YES|NO>",
  "conviction": <0..1 float>,
  "time_horizon": "<hours|days|weeks>",
  "entry_price_max": <0..1 float; max YES price you would still enter at>,
  "watch_terms": ["<keyword 1>", "<keyword 2>", "..."],
  "invalidate_if": ["<natural language condition 1>", "..."]
}

Rules:
- If no credible edge is visible, set skip=true with a one-line skip_reason. Do NOT invent edges.
- conviction must reflect evidence strength. 0.30 is "marginal", 0.70+ is "strong with clear catalyst".
- watch_terms are concrete keywords (entities, sources, ruling names) the signal layer will match against incoming news, never generic phrases like "news" or "update".
- invalidate_if entries are concrete falsifiers (e.g. "favored candidate concedes", "official source rules opposite").
- direction "NO" means buying NO shares; entry_price_max then applies to the NO side.
- Respond with ONLY the JSON object, no markdown fences.`

// GenerateProposal asks the LLM for a Proposal for one market context.
func GenerateProposal(ctx context.Context, cfg GeneratorConfig, mc MarketContext, logger *slog.Logger) (*Proposal, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Provider == nil {
		return nil, errors.New("polymarketdiscovery: nil LLM provider")
	}
	retries := cfg.MaxRetries
	if retries == 0 {
		retries = 2
	}

	userPrompt := buildUserPrompt(mc)
	messages := []llm.Message{
		{Role: "system", Content: generatorSystemPrompt},
		{Role: "user", Content: userPrompt},
	}

	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		resp, err := cfg.Provider.Complete(ctx, llm.CompletionRequest{
			Model:          cfg.Model,
			Messages:       messages,
			ResponseFormat: &llm.ResponseFormat{Type: llm.ResponseFormatJSONObject},
		})
		if err != nil {
			return nil, fmt.Errorf("polymarketdiscovery: LLM call: %w", err)
		}

		raw := stripJSONFences(resp.Content)
		var p Proposal
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			lastErr = fmt.Errorf("decode proposal: %w", err)
		} else if vErr := validateProposal(&p); vErr != nil {
			lastErr = vErr
		} else {
			logger.Info("polymarketdiscovery: proposal generated",
				slog.String("slug", mc.Market.Slug),
				slog.String("template", string(p.Template)),
				slog.Bool("skip", p.Skip),
				slog.Float64("conviction", p.Conviction),
			)
			return &p, nil
		}

		logger.Warn("polymarketdiscovery: proposal invalid, retrying",
			slog.String("slug", mc.Market.Slug),
			slog.Int("attempt", attempt+1),
			slog.Any("error", lastErr),
		)
		messages = append(messages,
			llm.Message{Role: "assistant", Content: resp.Content},
			llm.Message{Role: "user", Content: fmt.Sprintf(
				"Your previous JSON was rejected: %s. Return a corrected JSON object only.",
				lastErr.Error(),
			)},
		)
	}
	return nil, fmt.Errorf("polymarketdiscovery: failed after %d attempts: %w", retries+1, lastErr)
}

func validateProposal(p *Proposal) error {
	if p.Skip {
		if strings.TrimSpace(p.SkipReason) == "" {
			return errors.New("skip=true requires skip_reason")
		}
		return nil
	}
	if !p.Template.IsValid() {
		return fmt.Errorf("invalid template %q", p.Template)
	}
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("name required")
	}
	if strings.TrimSpace(p.Summary) == "" {
		return errors.New("summary required")
	}
	side := strings.ToUpper(strings.TrimSpace(p.Direction))
	if side != "YES" && side != "NO" {
		return fmt.Errorf("direction must be YES or NO, got %q", p.Direction)
	}
	p.Direction = side
	if p.Conviction < 0 || p.Conviction > 1 {
		return fmt.Errorf("conviction out of range: %.3f", p.Conviction)
	}
	switch strings.ToLower(strings.TrimSpace(p.TimeHorizon)) {
	case "hours", "days", "weeks":
	default:
		return fmt.Errorf("invalid time_horizon %q", p.TimeHorizon)
	}
	if p.EntryPriceMax < 0 || p.EntryPriceMax > 1 {
		return fmt.Errorf("entry_price_max out of range: %.3f", p.EntryPriceMax)
	}
	if len(p.WatchTerms) == 0 {
		return errors.New("watch_terms must not be empty")
	}
	return nil
}

func stripJSONFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// drop opening fence (optional "json" tag)
		if nl := strings.Index(s, "\n"); nl > 0 {
			s = s[nl+1:]
		}
		if end := strings.LastIndex(s, "```"); end >= 0 {
			s = s[:end]
		}
	}
	return strings.TrimSpace(s)
}

func buildUserPrompt(mc MarketContext) string {
	var sb strings.Builder
	sb.WriteString("Strategy catalog (pick one):\n")
	for _, t := range AllTemplates {
		fmt.Fprintf(&sb, "- %s: %s\n", t, TemplateDescriptions[t])
	}
	sb.WriteString("\nMarket and evidence:\n")
	sb.WriteString(mc.promptSummary())
	sb.WriteString("\nProduce the JSON proposal now.")
	return sb.String()
}
