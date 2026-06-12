import type { AgentRole, StrategyConfigWire, StrategyLLMProvider } from '@/lib/api/types'

export type { StrategyConfigWire, StrategyLLMProvider }

export interface StrategyConfigForm {
  scheduleCron: string
  llm: {
    provider: '' | StrategyLLMProvider
    deepThinkModel: string
    quickThinkModel: string
  }
  pipeline: {
    debateRounds: string
    analysisTimeoutSeconds: string
    debateTimeoutSeconds: string
  }
  risk: {
    positionSizePct: string
    stopLossMultiplier: string
    takeProfitMultiplier: string
    minConfidence: string
    useKellySizing: boolean
  }
  analysts: {
    mode: 'default' | 'custom'
    selected: AgentRole[]
  }
  promptOverridesJson: string
}

export interface StrategyConfigViewModel {
  scheduleCron: string
  llm: StrategyConfigForm['llm']
  pipeline: StrategyConfigForm['pipeline']
  risk: StrategyConfigForm['risk'] & { hints: string[] }
  analysts: StrategyConfigForm['analysts'] & { labels: string[] }
  promptOverridesJson: string
  promptOverrideCount: number
  rulesEngine: StrategyRulesEngineViewModel | null
}

export interface StrategyRulesRow {
  group: string
  field: string
  operator: string
  value: string
  explanation: string
}

export interface StrategyRulesDetail {
  label: string
  text: string
}

export interface StrategyRulesEngineViewModel {
  name: string | null
  description: string | null
  summary: string
  rows: StrategyRulesRow[]
  riskHints: string[]
  details: StrategyRulesDetail[]
}

export type StrategyConfigSubmitResult =
  | { ok: true; config?: StrategyConfigWire; scheduleCron?: string }
  | { ok: false; fieldErrors: Record<string, string> }

export const defaultAnalysts: AgentRole[] = [
  'market_analyst',
  'fundamentals_analyst',
  'news_analyst',
  'social_media_analyst',
]

export const llmProviderOptions: StrategyLLMProvider[] = [
  'openai',
  'anthropic',
  'google',
  'openrouter',
  'xai',
  'ollama',
]

function isDefaultSelection(selected: AgentRole[]): boolean {
  return (
    selected.length === defaultAnalysts.length &&
    defaultAnalysts.every((role) => selected.includes(role))
  )
}

function emptyForm(scheduleCron = ''): StrategyConfigForm {
  return {
    scheduleCron: normalizeCronInput(scheduleCron),
    llm: { provider: '', deepThinkModel: '', quickThinkModel: '' },
    pipeline: { debateRounds: '', analysisTimeoutSeconds: '', debateTimeoutSeconds: '' },
    risk: {
      positionSizePct: '',
      stopLossMultiplier: '',
      takeProfitMultiplier: '',
      minConfidence: '',
      useKellySizing: false,
    },
    analysts: { mode: 'default', selected: [...defaultAnalysts] },
    promptOverridesJson: '',
  }
}

export function normalizeCronInput(value: string | null | undefined): string {
  return value?.trim() ?? ''
}

export function serializeCronInput(value: string): string | undefined {
  const trimmed = value.trim()
  return trimmed ? trimmed : undefined
}

export function formatAgentRoleLabel(role: AgentRole): string {
  return role.replace(/_/g, ' ')
}

function buildRiskHints(risk: NonNullable<StrategyConfigWire['risk_config']> | undefined): string[] {
  if (!risk) return []

  const hints: string[] = []

  if (risk.position_size_pct != null) {
    hints.push(`Position size capped at ${risk.position_size_pct}%`)
  }
  if (risk.use_kelly_sizing) {
    hints.push('Kelly sizing enabled')
  }
  if (risk.stop_loss_multiplier != null) {
    hints.push(`Stop loss uses ATR × ${risk.stop_loss_multiplier}`)
  }
  if (risk.take_profit_multiplier != null) {
    hints.push(`Take profit uses ATR × ${risk.take_profit_multiplier}`)
  }
  if (risk.min_confidence != null) {
    hints.push(`Minimum confidence ${Math.round(risk.min_confidence * 100)}%`)
  }

  return hints
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value != null && typeof value === 'object' && !Array.isArray(value)
}

function toDisplayValue(value: unknown): string {
  if (value == null) return '—'
  if (Array.isArray(value)) return value.map((item) => toDisplayValue(item)).join(', ')
  if (typeof value === 'object') {
    try {
      return JSON.stringify(value)
    } catch {
      return String(value)
    }
  }

  return String(value)
}

function formatMaybeNumber(value: unknown, fractionDigits = 2): string {
  const num = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(num) ? num.toFixed(fractionDigits) : toDisplayValue(value)
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return value != null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null
}

function getRulesEngineSource(raw: StrategyConfigWire | null | undefined): Record<string, unknown> | null {
  return asRecord(raw?.rules_engine) ?? asRecord(raw?.options_rules)
}

function collectRuleRows(rulesEngine: Record<string, unknown> | null): StrategyRulesRow[] {
  const rows: StrategyRulesRow[] = []

  for (const [group, label] of [
    ['entry', 'Entry'],
    ['exit', 'Exit'],
  ] as const) {
    const section = rulesEngine && isRecord(rulesEngine[group]) ? rulesEngine[group] : null
    const conditions = Array.isArray(section?.conditions) ? section.conditions : []

    for (const condition of conditions) {
      if (!isRecord(condition)) continue

      const field = toDisplayValue(condition.field)
      const operator = toDisplayValue(condition.op ?? condition.operator)
      const valueSource = condition.value ?? condition.ref ?? condition.reference
      const explanation =
        typeof condition.explanation === 'string' && condition.explanation.trim().length > 0
          ? condition.explanation
          : typeof condition.description === 'string' && condition.description.trim().length > 0
            ? condition.description
            : `${label} condition`

      rows.push({
        group: label,
        field,
        operator,
        value: toDisplayValue(valueSource),
        explanation,
      })
    }
  }

  return rows
}

function summarizeRules(rulesEngine: Record<string, unknown> | null, rows: StrategyRulesRow[]) {
  if (!rulesEngine) return 'No rules engine configuration is available.'

  const entryCount = rows.filter((row) => row.group === 'Entry').length
  const exitCount = rows.filter((row) => row.group === 'Exit').length
  const hints: string[] = []

  if (isRecord(rulesEngine.position_sizing)) hints.push('position sizing')
  if (isRecord(rulesEngine.stop_loss)) hints.push('stop loss')
  if (isRecord(rulesEngine.take_profit)) hints.push('take profit')
  if (isRecord(rulesEngine.filters)) hints.push('filters')

  const hintText = hints.length > 0 ? ` Risk controls: ${hints.join(', ')}.` : ''

  return `Entry rules: ${entryCount}; exit rules: ${exitCount}.${hintText}`
}

function summarizeRiskHints(rulesEngine: Record<string, unknown> | null) {
  if (!rulesEngine) return []

  const hints: string[] = []

  if (isRecord(rulesEngine.position_sizing)) {
    const positionSizing = rulesEngine.position_sizing
    const parts = [
      typeof positionSizing.method === 'string' ? positionSizing.method : null,
      positionSizing.fraction_pct != null ? `${toDisplayValue(positionSizing.fraction_pct)}%` : null,
      positionSizing.risk_per_trade_pct != null
        ? `${formatMaybeNumber(Number(positionSizing.risk_per_trade_pct) * 100, 1)}% risk/trade`
        : null,
      positionSizing.atr_multiplier != null ? `ATR × ${formatMaybeNumber(positionSizing.atr_multiplier, 2)}` : null,
    ].filter(Boolean)
    if (parts.length > 0) hints.push(`Position sizing: ${parts.join(' • ')}`)
  }

  if (isRecord(rulesEngine.stop_loss)) {
    const stopLoss = rulesEngine.stop_loss
    const parts = [
      typeof stopLoss.method === 'string' ? stopLoss.method : null,
      stopLoss.pct != null ? `${toDisplayValue(stopLoss.pct)}%` : null,
      stopLoss.atr_multiplier != null ? `ATR × ${formatMaybeNumber(stopLoss.atr_multiplier, 2)}` : null,
    ].filter(Boolean)
    if (parts.length > 0) hints.push(`Stop loss: ${parts.join(' • ')}`)
  }

  if (isRecord(rulesEngine.take_profit)) {
    const takeProfit = rulesEngine.take_profit
    const parts = [
      typeof takeProfit.method === 'string' ? takeProfit.method : null,
      takeProfit.ratio != null ? `R:R ${formatMaybeNumber(takeProfit.ratio, 2)}` : null,
      takeProfit.atr_multiplier != null ? `ATR × ${formatMaybeNumber(takeProfit.atr_multiplier, 2)}` : null,
    ].filter(Boolean)
    if (parts.length > 0) hints.push(`Take profit: ${parts.join(' • ')}`)
  }

  if (isRecord(rulesEngine.filters)) {
    const filters = rulesEngine.filters
    const parts = [
      filters.min_volume != null ? `min volume ${toDisplayValue(filters.min_volume)}` : null,
      filters.min_atr != null ? `min ATR ${toDisplayValue(filters.min_atr)}` : null,
    ].filter(Boolean)
    if (parts.length > 0) hints.push(`Filters: ${parts.join(' • ')}`)
  }

  return hints
}

function buildRulesEngineDetails(rulesEngine: Record<string, unknown> | null): StrategyRulesDetail[] {
  if (!rulesEngine) return []

  const details: StrategyRulesDetail[] = []

  if (isRecord(rulesEngine.position_sizing)) {
    const positionSizing = rulesEngine.position_sizing
    const parts = [
      typeof positionSizing.method === 'string' ? positionSizing.method : null,
      positionSizing.fraction_pct != null ? `${toDisplayValue(positionSizing.fraction_pct)}%` : null,
      positionSizing.risk_per_trade_pct != null
        ? `${formatMaybeNumber(Number(positionSizing.risk_per_trade_pct) * 100, 1)}% risk/trade`
        : null,
      positionSizing.atr_multiplier != null ? `ATR × ${formatMaybeNumber(positionSizing.atr_multiplier, 2)}` : null,
    ].filter(Boolean)
    details.push({ label: 'Position sizing', text: parts.length > 0 ? parts.join(' • ') : '—' })
  }

  if (isRecord(rulesEngine.stop_loss)) {
    const stopLoss = rulesEngine.stop_loss
    const parts = [
      typeof stopLoss.method === 'string' ? stopLoss.method : null,
      stopLoss.pct != null ? `${toDisplayValue(stopLoss.pct)}%` : null,
      stopLoss.atr_multiplier != null ? `ATR × ${formatMaybeNumber(stopLoss.atr_multiplier, 2)}` : null,
    ].filter(Boolean)
    details.push({ label: 'Stop loss', text: parts.length > 0 ? parts.join(' • ') : '—' })
  }

  if (isRecord(rulesEngine.take_profit)) {
    const takeProfit = rulesEngine.take_profit
    const parts = [
      typeof takeProfit.method === 'string' ? takeProfit.method : null,
      takeProfit.ratio != null ? `R:R ${formatMaybeNumber(takeProfit.ratio, 2)}` : null,
      takeProfit.atr_multiplier != null ? `ATR × ${formatMaybeNumber(takeProfit.atr_multiplier, 2)}` : null,
    ].filter(Boolean)
    details.push({ label: 'Take profit', text: parts.length > 0 ? parts.join(' • ') : '—' })
  }

  if (isRecord(rulesEngine.filters)) {
    const filters = rulesEngine.filters
    const text = [
      filters.min_volume != null ? `Min volume ${toDisplayValue(filters.min_volume)}` : 'No volume floor',
      filters.min_atr != null ? `Min ATR ${toDisplayValue(filters.min_atr)}` : null,
    ]
      .filter(Boolean)
      .join(' • ')
    details.push({ label: 'Filters', text })
  }

  return details
}

function buildRulesEngineView(raw: StrategyConfigWire | null | undefined): StrategyRulesEngineViewModel | null {
  const source = getRulesEngineSource(raw)
  if (!source) return null

  const rows = collectRuleRows(source)

  return {
    name: typeof source.name === 'string' ? source.name : null,
    description: typeof source.description === 'string' ? source.description : null,
    summary: summarizeRules(source, rows),
    rows,
    riskHints: summarizeRiskHints(source),
    details: buildRulesEngineDetails(source),
  }
}

function parsePromptOverrides(raw: StrategyConfigWire['prompt_overrides']): string {
  return raw && Object.keys(raw).length > 0 ? JSON.stringify(raw, null, 2) : ''
}

export const strategyConfigBoundary = {
  load(raw: StrategyConfigWire | null | undefined, scheduleCron = ''): StrategyConfigForm {
    if (!raw) return emptyForm(scheduleCron)

    const llm = raw.llm_config ?? {}
    const pipeline = raw.pipeline_config ?? {}
    const risk = raw.risk_config ?? {}
    const rawAnalysts = raw.analyst_selection
    const selected = rawAnalysts && rawAnalysts.length > 0 ? [...rawAnalysts] : [...defaultAnalysts]

    return {
      scheduleCron: normalizeCronInput(scheduleCron),
      llm: {
        provider: (llm.provider ?? '') as '' | StrategyLLMProvider,
        deepThinkModel: llm.deep_think_model ?? '',
        quickThinkModel: llm.quick_think_model ?? '',
      },
      pipeline: {
        debateRounds: pipeline.debate_rounds != null ? String(pipeline.debate_rounds) : '',
        analysisTimeoutSeconds:
          pipeline.analysis_timeout_seconds != null ? String(pipeline.analysis_timeout_seconds) : '',
        debateTimeoutSeconds:
          pipeline.debate_timeout_seconds != null ? String(pipeline.debate_timeout_seconds) : '',
      },
      risk: {
        positionSizePct: risk.position_size_pct != null ? String(risk.position_size_pct / 100) : '',
        stopLossMultiplier:
          risk.stop_loss_multiplier != null ? String(risk.stop_loss_multiplier) : '',
        takeProfitMultiplier:
          risk.take_profit_multiplier != null ? String(risk.take_profit_multiplier) : '',
        minConfidence: risk.min_confidence != null ? String(risk.min_confidence) : '',
        useKellySizing: risk.use_kelly_sizing ?? false,
      },
      analysts: {
        mode: isDefaultSelection(selected) ? 'default' : 'custom',
        selected,
      },
      promptOverridesJson: parsePromptOverrides(raw.prompt_overrides),
    }
  },

  view(raw: StrategyConfigWire | null | undefined, scheduleCron?: string | null): StrategyConfigViewModel {
    const form = strategyConfigBoundary.load(raw, scheduleCron ?? '')
    return {
      scheduleCron: normalizeCronInput(scheduleCron),
      llm: form.llm,
      pipeline: form.pipeline,
      risk: { ...form.risk, hints: buildRiskHints(raw?.risk_config) },
      analysts: {
        ...form.analysts,
        labels: form.analysts.selected.map(formatAgentRoleLabel),
      },
      promptOverridesJson: form.promptOverridesJson,
      promptOverrideCount: raw?.prompt_overrides ? Object.keys(raw.prompt_overrides).length : 0,
      rulesEngine: buildRulesEngineView(raw),
    }
  },

  normalizeCronInput,

  submit(form: StrategyConfigForm, rawConfig?: StrategyConfigWire | null): StrategyConfigSubmitResult {
    const fieldErrors: Record<string, string> = {}
    const config: StrategyConfigWire = { ...(asRecord(rawConfig) ?? {}) }

    const llm = { ...(asRecord(rawConfig?.llm_config) ?? {}) }
    const pipeline = { ...(asRecord(rawConfig?.pipeline_config) ?? {}) }
    const rawRisk = asRecord(rawConfig?.risk_config)
    const risk = { ...(rawRisk ?? {}) }

    // Preserve flexible JSON sections while updating modeled fields.
    delete config.analyst_selection

    // LLM config
    if (form.llm.provider) llm.provider = form.llm.provider as StrategyLLMProvider
    else delete llm.provider
    if (form.llm.deepThinkModel) llm.deep_think_model = form.llm.deepThinkModel
    else delete llm.deep_think_model
    if (form.llm.quickThinkModel) llm.quick_think_model = form.llm.quickThinkModel
    else delete llm.quick_think_model
    if (Object.keys(llm).length > 0) config.llm_config = llm
    else delete config.llm_config

    // Pipeline config
    if (form.pipeline.debateRounds.trim()) {
      const v = Number(form.pipeline.debateRounds)
      if (!Number.isFinite(v) || v < 1 || v > 10) {
        fieldErrors['pipeline.debateRounds'] = 'Debate rounds must be between 1 and 10'
      } else {
        pipeline.debate_rounds = v
      }
    } else {
      delete pipeline.debate_rounds
    }
    if (form.pipeline.analysisTimeoutSeconds.trim()) {
      const v = Number(form.pipeline.analysisTimeoutSeconds)
      if (!Number.isFinite(v) || v <= 0) {
        fieldErrors['pipeline.analysisTimeoutSeconds'] = 'Analysis timeout must be greater than 0'
      } else {
        pipeline.analysis_timeout_seconds = v
      }
    } else {
      delete pipeline.analysis_timeout_seconds
    }
    if (form.pipeline.debateTimeoutSeconds.trim()) {
      const v = Number(form.pipeline.debateTimeoutSeconds)
      if (!Number.isFinite(v) || v <= 0) {
        fieldErrors['pipeline.debateTimeoutSeconds'] = 'Debate timeout must be greater than 0'
      } else {
        pipeline.debate_timeout_seconds = v
      }
    } else {
      delete pipeline.debate_timeout_seconds
    }
    if (Object.keys(pipeline).length > 0) config.pipeline_config = pipeline
    else delete config.pipeline_config

    // Risk config
    if (form.risk.positionSizePct.trim()) {
      const v = Number(form.risk.positionSizePct)
      if (!Number.isFinite(v) || v < 0.01 || v > 1) {
        fieldErrors['risk.positionSizePct'] = 'Max position size % must be between 0.01 and 1.00'
      } else {
        risk.position_size_pct = v * 100
      }
    } else {
      delete risk.position_size_pct
    }
    if (form.risk.useKellySizing || Object.prototype.hasOwnProperty.call(rawRisk ?? {}, 'use_kelly_sizing')) {
      risk.use_kelly_sizing = form.risk.useKellySizing
    }
    if (form.risk.stopLossMultiplier.trim()) {
      const v = Number(form.risk.stopLossMultiplier)
      if (!Number.isFinite(v) || v <= 0) {
        fieldErrors['risk.stopLossMultiplier'] = 'Stop loss ATR multiplier must be greater than 0'
      } else {
        risk.stop_loss_multiplier = v
      }
    } else {
      delete risk.stop_loss_multiplier
    }
    if (form.risk.takeProfitMultiplier.trim()) {
      const v = Number(form.risk.takeProfitMultiplier)
      if (!Number.isFinite(v) || v <= 0) {
        fieldErrors['risk.takeProfitMultiplier'] =
          'Take profit ATR multiplier must be greater than 0'
      } else {
        risk.take_profit_multiplier = v
      }
    } else {
      delete risk.take_profit_multiplier
    }
    if (form.risk.minConfidence.trim()) {
      const v = Number(form.risk.minConfidence)
      if (!Number.isFinite(v) || v < 0 || v > 1) {
        fieldErrors['risk.minConfidence'] = 'Min confidence threshold must be between 0 and 1'
      } else {
        risk.min_confidence = v
      }
    } else {
      delete risk.min_confidence
    }
    if (Object.keys(risk).length > 0) config.risk_config = risk
    else delete config.risk_config

    // Analysts
    const selected = form.analysts.mode === 'default' ? defaultAnalysts : form.analysts.selected
    if (selected.length === 0) {
      fieldErrors['analysts.selected'] = 'Select at least one analyst'
    } else {
      config.analyst_selection = selected
    }

    // Prompt overrides
    const json = form.promptOverridesJson.trim()
    if (json) {
      try {
        const parsed = JSON.parse(json) as unknown
        if (parsed == null || typeof parsed !== 'object' || Array.isArray(parsed)) {
          fieldErrors['promptOverridesJson'] = 'Prompt overrides must be a JSON object'
        } else {
          const entries = Object.entries(parsed as Record<string, unknown>)
          if (entries.some(([, value]) => typeof value !== 'string')) {
            fieldErrors['promptOverridesJson'] = 'Prompt overrides must map roles to strings'
          } else {
            config.prompt_overrides = Object.fromEntries(entries) as Record<string, string>
          }
        }
      } catch {
        fieldErrors['promptOverridesJson'] = 'Prompt overrides must be valid JSON'
      }
    } else {
      delete config.prompt_overrides
    }

    if (Object.keys(fieldErrors).length > 0) return { ok: false, fieldErrors }
    return {
      ok: true,
      config: Object.keys(config).length > 0 ? config : undefined,
      scheduleCron: serializeCronInput(form.scheduleCron),
    }
  },

  serializeCronInput,
  formatAgentRoleLabel,
  extractRulesEngine(raw: StrategyConfigWire | null | undefined): Record<string, unknown> | null {
    return getRulesEngineSource(raw)
  },
}
