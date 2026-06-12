import { describe, expect, it } from 'vitest'

import { strategyConfigBoundary, type StrategyConfigForm, type StrategyConfigWire } from './boundary'

describe('strategyConfigBoundary', () => {
  it('loads and summarizes typed config state from raw wire JSON', () => {
    const raw: StrategyConfigWire = {
      llm_config: {
        provider: 'anthropic',
        deep_think_model: 'claude-3-opus',
        quick_think_model: 'gpt-4o-mini',
      },
      pipeline_config: {
        debate_rounds: 4,
        analysis_timeout_seconds: 120,
        debate_timeout_seconds: 600,
      },
      risk_config: {
        position_size_pct: 20,
        use_kelly_sizing: true,
        stop_loss_multiplier: 1.5,
        take_profit_multiplier: 2.5,
        min_confidence: 0.7,
      },
      analyst_selection: ['market_analyst', 'news_analyst'],
      prompt_overrides: { trader: 'Use concise prompts' },
    }

    const form = strategyConfigBoundary.load(raw)
    const view = strategyConfigBoundary.view(raw, ' 0 9 * * 1-5 ')

    expect(form).toEqual({
      scheduleCron: '',
      llm: {
        provider: 'anthropic',
        deepThinkModel: 'claude-3-opus',
        quickThinkModel: 'gpt-4o-mini',
      },
      pipeline: {
        debateRounds: '4',
        analysisTimeoutSeconds: '120',
        debateTimeoutSeconds: '600',
      },
      risk: {
        positionSizePct: '0.2',
        stopLossMultiplier: '1.5',
        takeProfitMultiplier: '2.5',
        minConfidence: '0.7',
        useKellySizing: true,
      },
      analysts: {
        mode: 'custom',
        selected: ['market_analyst', 'news_analyst'],
      },
      promptOverridesJson: '{\n  "trader": "Use concise prompts"\n}',
    })

    expect(view.scheduleCron).toBe('0 9 * * 1-5')
    expect(view.analysts.labels).toEqual(['market analyst', 'news analyst'])
    expect(view.promptOverrideCount).toBe(1)
    expect(view.risk.hints).toEqual([
      'Position size capped at 20%',
      'Kelly sizing enabled',
      'Stop loss uses ATR × 1.5',
      'Take profit uses ATR × 2.5',
      'Minimum confidence 70%',
    ])
  })

  it('serializes form state back to wire JSON and trims blank cron values', () => {
    const form: StrategyConfigForm = {
      scheduleCron: ' 0 9 * * 1-5 ',
      llm: {
        provider: 'openai',
        deepThinkModel: 'gpt-4.1-mini',
        quickThinkModel: 'gpt-4o-mini',
      },
      pipeline: {
        debateRounds: '3',
        analysisTimeoutSeconds: '90',
        debateTimeoutSeconds: '900',
      },
      risk: {
        positionSizePct: '0.25',
        stopLossMultiplier: '1.8',
        takeProfitMultiplier: '2.8',
        minConfidence: '0.75',
        useKellySizing: true,
      },
      analysts: {
        mode: 'custom',
        selected: ['market_analyst', 'news_analyst', 'fundamentals_analyst'],
      },
      promptOverridesJson: '{\n  "trader": "Use concise prompts"\n}',
    }

    const result = strategyConfigBoundary.submit(form)

    expect(result).toEqual({
      ok: true,
      scheduleCron: '0 9 * * 1-5',
      config: {
        llm_config: {
          provider: 'openai',
          deep_think_model: 'gpt-4.1-mini',
          quick_think_model: 'gpt-4o-mini',
        },
        pipeline_config: {
          debate_rounds: 3,
          analysis_timeout_seconds: 90,
          debate_timeout_seconds: 900,
        },
        risk_config: {
          position_size_pct: 25,
          use_kelly_sizing: true,
          stop_loss_multiplier: 1.8,
          take_profit_multiplier: 2.8,
          min_confidence: 0.75,
        },
        analyst_selection: ['market_analyst', 'news_analyst', 'fundamentals_analyst'],
        prompt_overrides: {
          trader: 'Use concise prompts',
        },
      },
    })

    const blankCronResult = strategyConfigBoundary.submit({
      ...form,
      scheduleCron: '   ',
    })

    expect(blankCronResult.ok).toBe(true)
    if (blankCronResult.ok) {
      expect(blankCronResult.scheduleCron).toBeUndefined()
    }
  })

  it('preserves unknown config sections while applying typed edits', () => {
    const raw = {
      llm_config: {
        provider: 'anthropic',
        deep_think_model: 'claude-3-opus',
        quick_think_model: 'gpt-4o-mini',
        llm_future_toggle: 'keep-me',
      },
      pipeline_config: {
        debate_rounds: 4,
        analysis_timeout_seconds: 120,
        debate_timeout_seconds: 600,
        pipeline_future_toggle: { enabled: true },
      },
      risk_config: {
        position_size_pct: 20,
        use_kelly_sizing: false,
        stop_loss_multiplier: 1.5,
        take_profit_multiplier: 2.5,
        min_confidence: 0.7,
        risk_future_toggle: { enabled: true },
      },
      analyst_selection: ['market_analyst'],
      prompt_overrides: {
        trader: 'Use concise prompts',
        prompt_future_toggle: 'keep-me',
      },
      rules_engine: { sentinel: 'rules-engine' },
      options_rules: { sentinel: 'options-rules' },
      future_toggle: { enabled: true },
    } as StrategyConfigWire & Record<string, unknown>

    const form = {
      ...strategyConfigBoundary.load(raw, '0 9 * * 1-5'),
      scheduleCron: ' 0 9 * * 1-5 ',
      llm: {
        provider: 'openai',
        deepThinkModel: 'gpt-4.1-mini',
        quickThinkModel: 'gpt-4o-mini',
      },
      pipeline: {
        debateRounds: '5',
        analysisTimeoutSeconds: '90',
        debateTimeoutSeconds: '900',
      },
      risk: {
        positionSizePct: '0.25',
        stopLossMultiplier: '1.8',
        takeProfitMultiplier: '2.8',
        minConfidence: '0.75',
        useKellySizing: false,
      },
      analysts: {
        mode: 'custom',
        selected: ['market_analyst', 'news_analyst'],
      },
      promptOverridesJson: '{\n  "trader": "Stay concise",\n  "prompt_future_toggle": "keep-me"\n}',
    } satisfies StrategyConfigForm

    const result = strategyConfigBoundary.submit(form, raw)

    expect(result).toEqual({
      ok: true,
      scheduleCron: '0 9 * * 1-5',
      config: {
        llm_config: {
          provider: 'openai',
          deep_think_model: 'gpt-4.1-mini',
          quick_think_model: 'gpt-4o-mini',
          llm_future_toggle: 'keep-me',
        },
        pipeline_config: {
          debate_rounds: 5,
          analysis_timeout_seconds: 90,
          debate_timeout_seconds: 900,
          pipeline_future_toggle: { enabled: true },
        },
        risk_config: {
          position_size_pct: 25,
          use_kelly_sizing: false,
          stop_loss_multiplier: 1.8,
          take_profit_multiplier: 2.8,
          min_confidence: 0.75,
          risk_future_toggle: { enabled: true },
        },
        analyst_selection: ['market_analyst', 'news_analyst'],
        prompt_overrides: {
          trader: 'Stay concise',
          prompt_future_toggle: 'keep-me',
        },
        rules_engine: { sentinel: 'rules-engine' },
        options_rules: { sentinel: 'options-rules' },
        future_toggle: { enabled: true },
      },
    })
  })

  it('rejects invalid numeric values and empty analyst selections', () => {
    const result = strategyConfigBoundary.submit({
      scheduleCron: '0 9 * * 1-5',
      llm: { provider: '', deepThinkModel: '', quickThinkModel: '' },
      pipeline: {
        debateRounds: '11',
        analysisTimeoutSeconds: '0',
        debateTimeoutSeconds: '-1',
      },
      risk: {
        positionSizePct: '2',
        stopLossMultiplier: '0',
        takeProfitMultiplier: '0',
        minConfidence: '2',
        useKellySizing: false,
      },
      analysts: {
        mode: 'custom',
        selected: [],
      },
      promptOverridesJson: '{invalid',
    })

    expect(result.ok).toBe(false)
    if (!result.ok) {
      expect(result.fieldErrors).toMatchObject({
        'pipeline.debateRounds': 'Debate rounds must be between 1 and 10',
        'pipeline.analysisTimeoutSeconds': 'Analysis timeout must be greater than 0',
        'pipeline.debateTimeoutSeconds': 'Debate timeout must be greater than 0',
        'risk.positionSizePct': 'Max position size % must be between 0.01 and 1.00',
        'risk.stopLossMultiplier': 'Stop loss ATR multiplier must be greater than 0',
        'risk.takeProfitMultiplier': 'Take profit ATR multiplier must be greater than 0',
        'risk.minConfidence': 'Min confidence threshold must be between 0 and 1',
        'analysts.selected': 'Select at least one analyst',
        promptOverridesJson: 'Prompt overrides must be valid JSON',
      })
    }
  })
})
