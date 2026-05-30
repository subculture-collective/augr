package polymarketdiscovery

// StrategyTemplate enumerates the strategy archetypes the LLM may select.
//
// Each archetype maps to a documented set of entry/exit heuristics that the
// agent pipeline trader and signal evaluator can interpret from the thesis.
type StrategyTemplate string

const (
	TemplateWhaleCopy        StrategyTemplate = "whale_copy"
	TemplateArbitrage        StrategyTemplate = "arbitrage"
	TemplateNewsCatalyst     StrategyTemplate = "news_catalyst"
	TemplateMicrostructure   StrategyTemplate = "microstructure"
	TemplateConvergence      StrategyTemplate = "convergence"
	TemplateVolumeDivergence StrategyTemplate = "volume_divergence"
	TemplateResolutionEdge   StrategyTemplate = "resolution_edge"
	TemplateMeanReversion    StrategyTemplate = "mean_reversion"
	TemplateCalendarEvent    StrategyTemplate = "calendar_event"
	TemplateAntiFavorite     StrategyTemplate = "anti_favorite"
)

// AllTemplates lists every valid template the LLM may pick.
var AllTemplates = []StrategyTemplate{
	TemplateWhaleCopy,
	TemplateArbitrage,
	TemplateNewsCatalyst,
	TemplateMicrostructure,
	TemplateConvergence,
	TemplateVolumeDivergence,
	TemplateResolutionEdge,
	TemplateMeanReversion,
	TemplateCalendarEvent,
	TemplateAntiFavorite,
}

// TemplateDescriptions is the human + LLM-readable catalog of templates.
var TemplateDescriptions = map[StrategyTemplate]string{
	TemplateWhaleCopy:        "Copy entries by tracked high-win-rate wallets in this market. Enter YES/NO mirroring recent whale buys when wallet WR>=70% and trade <24h old. Exit on whale exit, resolution, or 30% adverse move.",
	TemplateArbitrage:        "Exploit YES+NO!=1 mispricings or cross-market arb. Enter only when implied probability deviation exceeds fees+slippage. Close within hours when spread reverts.",
	TemplateNewsCatalyst:     "Trade clean public catalysts (court rulings, votes, official statements) that should move resolution odds. Enter on confirmed headline, take profit on first repricing wave.",
	TemplateMicrostructure:   "Lean on orderbook imbalance or spread compression. Short holding period. Small size.",
	TemplateConvergence:      "Buy YES near resolution when probability is already 0.80-0.95 and no remaining catalyst can flip outcome. Hold to settlement.",
	TemplateVolumeDivergence: "Enter on volume z-score spike with flat price, in direction of trade-flow imbalance. Exit on breakout or fading volume.",
	TemplateResolutionEdge:   "Identify markets where literal resolution rules differ from crowd interpretation. Hold to resolution.",
	TemplateMeanReversion:    "Fade overshoots beyond k-sigma of VWAP in thin markets with no news support. Exit on revert.",
	TemplateCalendarEvent:    "Trade scheduled events (debate, ruling, vote, CPI) where market is misaligned with pre-event consensus. Exit around event time.",
	TemplateAntiFavorite:     "Fade long-tail YES<5% late in market life when new evidence raises tail above implied price. Small size, asymmetric payoff.",
}

// IsValid reports whether the template is one of the known archetypes.
func (t StrategyTemplate) IsValid() bool {
	switch t {
	case TemplateWhaleCopy, TemplateArbitrage, TemplateNewsCatalyst,
		TemplateMicrostructure, TemplateConvergence, TemplateVolumeDivergence,
		TemplateResolutionEdge, TemplateMeanReversion, TemplateCalendarEvent,
		TemplateAntiFavorite:
		return true
	}
	return false
}
