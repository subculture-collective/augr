import { describe, expect, it } from 'vitest'

import type { CircuitBreakerStatus, KillSwitchStatus, RiskStatus } from '@/lib/api/types'
import {
  RISK_COCKPIT_MARKET_LABELS,
  RISK_COCKPIT_MARKET_ORDER,
  RISK_MARKET_KILL_SWITCH_LABELS,
  RISK_MARKET_KILL_SWITCH_ORDER,
  formatKillSwitchMechanism,
  getCircuitBreakerDisplay,
  getCircuitBreakerSummaryDisplay,
  getCockpitMarketLabel,
  getKillSwitchDisplay,
  getMarketKillSwitchLabel,
  getRiskStatusDisplay,
} from '@/lib/risk/presentation'

describe('risk presentation', () => {
  it('maps risk severity to badge copy', () => {
    const cases: Array<[RiskStatus, string, 'success' | 'warning' | 'destructive']> = [
      ['normal', 'Normal', 'success'],
      ['warning', 'Warning', 'warning'],
      ['breached', 'Breached', 'destructive'],
    ]

    for (const [status, label, variant] of cases) {
      expect(getRiskStatusDisplay(status)).toMatchObject({ label, variant })
    }
  })

  it('maps circuit breaker states to labels and variants', () => {
    const cases: Array<[CircuitBreakerStatus['state'], string, 'success' | 'warning' | 'destructive']> = [
      ['open', 'Open', 'success'],
      ['tripped', 'Tripped', 'destructive'],
      ['cooldown', 'Cooldown', 'warning'],
    ]

    for (const [state, label, variant] of cases) {
      expect(getCircuitBreakerDisplay({ state })).toEqual({ label, variant })
    }

    expect(getCircuitBreakerSummaryDisplay(true)).toEqual({
      badgeLabel: 'Circuit breaker tripped',
      badgeVariant: 'warning',
    })
    expect(getCircuitBreakerSummaryDisplay(false)).toEqual({
      badgeLabel: 'Circuit breaker clear',
      badgeVariant: 'success',
    })
  })

  it('formats kill switch display copy and mechanism labels', () => {
    const inactive: KillSwitchStatus = { active: false, reason: '  ', mechanisms: ['api_toggle'] }
    expect(getKillSwitchDisplay(inactive)).toEqual({
      badgeLabel: 'Trading enabled',
      badgeVariant: 'success',
      description: 'The engine can submit orders normally.',
      mechanismText: 'API toggle',
    })

    const active: KillSwitchStatus = {
      active: true,
      reason: ' Manual stop ',
      mechanisms: ['file_flag', 'env_var', 'unknown'],
    }
    expect(getKillSwitchDisplay(active)).toEqual({
      badgeLabel: 'Trading halted',
      badgeVariant: 'destructive',
      description: 'Manual stop',
      mechanismText: 'File flag, Environment variable, Unknown',
    })

    expect(formatKillSwitchMechanism('api_toggle')).toBe('API toggle')
    expect(formatKillSwitchMechanism('file_flag')).toBe('File flag')
    expect(formatKillSwitchMechanism('env_var')).toBe('Environment variable')
    expect(formatKillSwitchMechanism('unknown')).toBe('Unknown')
  })

  it('keeps cockpit and kill switch market ordering stable', () => {
    expect(RISK_COCKPIT_MARKET_ORDER).toEqual(['stock', 'crypto', 'options', 'polymarket'])
    expect(RISK_MARKET_KILL_SWITCH_ORDER).toEqual(['stock', 'crypto', 'polymarket'])
    expect(getCockpitMarketLabel('options')).toBe('Options')
    expect(getMarketKillSwitchLabel('stock')).toBe('Stocks')
    expect(RISK_COCKPIT_MARKET_LABELS.polymarket).toBe('Polymarket')
    expect(RISK_MARKET_KILL_SWITCH_LABELS.crypto).toBe('Crypto')
  })
})
