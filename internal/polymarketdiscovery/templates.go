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
	TemplateWhaleCopy:        "Follow high-performing tracked wallets buying YES or NO when recency, liquidity, and spread gates make the fill executable.",
	TemplateArbitrage:        "Exploit YES+NO mispricings or cross-market dislocations when fees and slippage still leave positive edge.",
	TemplateNewsCatalyst:     "Trade confirmed public catalysts such as court rulings, votes, or official statements that should reprice resolution odds.",
	TemplateMicrostructure:   "Enter only when YES/NO spread, orderbook depth, and liquidity support a bounded-slippage fill.",
	TemplateConvergence:      "Buy YES when the market is already near settlement odds and the remaining path to resolution is narrow.",
	TemplateVolumeDivergence: "Enter when trade flow and liquidity shift faster than price, creating a usable edge in the current YES/NO quote.",
	TemplateResolutionEdge:   "Trade literal resolution wording and source metadata when the market misreads how the market settles.",
	TemplateMeanReversion:    "Fade stale mispricings after fresh market information fails to justify the current YES/NO price.",
	TemplateCalendarEvent:    "Trade scheduled events such as debates, rulings, votes, or data releases where market pricing drifts from the setup.",
	TemplateAntiFavorite:     "Fade long-shot YES positions late in market life when new evidence lifts tail probability above the implied price.",
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
