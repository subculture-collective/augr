import '@testing-library/jest-dom/vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import App from '@/App'
import { setTokenSnapshot } from '@/shared/auth/tokenStore'
import { buildAuthResponse } from '@/test/fixtures'
import { apiBaseUrl, installAppTestHarness, resetApp, server, state } from '@/test/app-harness'

const assessmentId = '00000000-0000-4000-8000-0000000000c1'
const transactionId = '00000000-0000-4000-8000-0000000000c2'

describe('total-overhaul operator workspace', () => {
  installAppTestHarness()

  it('separates the static roadmap from live, prerequisite-only evidence', async () => {
    resetApp('/overhaul')
    setTokenSnapshot(buildAuthResponse())
    render(<App />)

    expect(await screen.findByRole('heading', { name: /^capital & evidence$/i })).toBeTruthy()
    expect(await screen.findByText(/required prerequisite checks are passing/i)).toBeTruthy()
    expect(screen.getByText(/live flag: disabled/i)).toBeTruthy()
    expect(screen.getByRole('heading', { name: /prerequisite checks/i })).toBeTruthy()
    expect(screen.getByText(/combine configuration and dependency checks with limited live checks/i)).toBeTruthy()
    expect(screen.getByText(/do not prove provider connectivity, successful execution, or deployment approval/i)).toBeTruthy()
    expect(screen.getAllByText(/^passing$/i).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/^not passing$/i).length).toBeGreaterThan(0)
    expect(screen.getByRole('heading', { name: /r0–r6 roadmap/i })).toBeTruthy()
    expect(screen.getByText(/static reference · not runtime evidence/i)).toBeTruthy()
    expect(screen.getByText('R0')).toBeTruthy()
    expect(screen.getByText('R6')).toBeTruthy()
    expect(await screen.findByRole('list', { name: /economic accounts/i })).toBeTruthy()
    expect(await screen.findByText('USD 515.00000000')).toBeTruthy()
    expect(await screen.findByText(/cutover evidence predicate passes/i)).toBeTruthy()
    expect(screen.getByText(/observed target: kalshi \/ paper-scored/i)).toBeTruthy()
    expect(screen.getByText(/promotion eligible evidence/i)).toBeTruthy()
    expect(screen.getByText(/inspection is not activation/i)).toBeTruthy()
    expect(screen.getAllByText(/read only/i).length).toBeGreaterThan(0)
    expect(screen.getByText(/net contributed capital is deposits, including opening capital, minus withdrawals/i)).toBeTruthy()
    expect(screen.getByText(/stored policy multiplier/i)).toBeTruthy()
  })

  it('labels incomplete required prerequisite checks without configuration claims', async () => {
    resetApp('/overhaul')
    setTokenSnapshot(buildAuthResponse())
    server.use(http.get(`${apiBaseUrl}/release/readiness`, () => HttpResponse.json({
      release_ready: false,
      live_trading_enabled: false,
      capabilities: [
        { name: 'database', mode: 'paper', ready: true, required: true },
        { name: 'scheduler', mode: 'paper', ready: false, required: true, blockers: ['scheduler unavailable'] },
      ],
      generated_at: '2026-08-24T12:00:00Z',
    })))
    render(<App />)

    expect(await screen.findByText(/required prerequisite checks are incomplete/i)).toBeTruthy()
    expect(screen.getByText(/1 of 2 required checks are passing/i)).toBeTruthy()
    expect(screen.getByText(/^not passing$/i)).toBeTruthy()
    expect(screen.queryByText(/not configured/i)).toBeNull()
  })

  it('validates lookup IDs and reconstructs milestone and balanced-ledger evidence independently', async () => {
    resetApp('/overhaul')
    setTokenSnapshot(buildAuthResponse())
    render(<App />)
    await screen.findByRole('heading', { name: /^capital & evidence$/i })

    const assessmentInput = screen.getByLabelText(/assessment uuid/i)
    await userEvent.type(assessmentInput, 'not-an-id')
    expect(screen.getByText(/enter a valid uuid/i)).toBeTruthy()
    expect(screen.getByRole('button', { name: /inspect/i })).toBeDisabled()
    await userEvent.clear(assessmentInput)
    await userEvent.type(assessmentInput, assessmentId)
    await userEvent.click(screen.getByRole('button', { name: /inspect/i }))
    expect(await screen.findByText('fixture-shadow-campaign')).toBeTruthy()
    expect(screen.getByText(/30 elapsed days are required/i)).toBeTruthy()

    const ledgerInput = screen.getByLabelText(/transaction uuid/i)
    await userEvent.type(ledgerInput, transactionId)
    await userEvent.click(screen.getByRole('button', { name: /^trace$/i }))
    const ledgerPanel = screen.getByRole('heading', { name: /ledger trace/i }).closest('section') as HTMLElement
    expect(await within(ledgerPanel).findByText('capital.deposit')).toBeTruthy()
    expect(within(ledgerPanel).getByText('-25.00000000')).toBeTruthy()
    expect(within(ledgerPanel).getByText(/stored record/i)).toBeTruthy()
    expect(within(ledgerPanel).queryByText(/balanced record/i)).toBeNull()
  })

  it('distinguishes an empty account projection from a disabled runtime gate', async () => {
    resetApp('/overhaul')
    state.scenario = 'empty-data'
    setTokenSnapshot(buildAuthResponse())
    render(<App />)
    expect(await screen.findByText(/no economic accounts/i)).toBeTruthy()

    resetApp('/overhaul')
    setTokenSnapshot(buildAuthResponse())
    server.use(http.get(`${apiBaseUrl}/economic/accounts`, () => HttpResponse.json({ error: 'economic account reads are disabled', code: 'ERR_NOT_IMPLEMENTED' }, { status: 501 })))
    render(<App />)
    expect(await screen.findByText(/endpoint disabled or not implemented/i)).toBeTruthy()
    expect(screen.getByText(/required prerequisite checks are passing/i)).toBeTruthy()
  })

  it('does not render zero-valued cutover evidence when its snapshot is unavailable', async () => {
    resetApp('/overhaul')
    setTokenSnapshot(buildAuthResponse())
    server.use(http.get(`${apiBaseUrl}/release/cutover-status`, () => HttpResponse.json({
      generated_at: '2026-08-24T12:00:00Z',
      promotion_ready: false,
      account_trusted: false,
      account_id: '00000000-0000-4000-8000-0000000000a1',
      scoped_artifacts: 0,
      quarantined_legacy_rows: 0,
      canonical_lots: 0,
      scope_mismatches: 0,
      missing_canonical_links: 0,
      fresh_marks: 0,
      stale_marks: 0,
      unavailable_marks: 0,
      reconciliation_available: false,
      reconciliation_passed: false,
      unavailable_reasons: ['projection_unavailable'],
      promotion_block_reasons: ['configured_projection_account_unavailable'],
    })))
    render(<App />)

    await screen.findByText(/projection unavailable/i)
    const cutoverPanel = screen.getByRole('heading', { name: /cutover evidence check/i }).closest('section') as HTMLElement
    expect(within(cutoverPanel).getByText(/projection unavailable/i)).toBeTruthy()
    expect(within(cutoverPanel).getByText(/reconciliation evidence unavailable/i)).toBeTruthy()
    expect(within(cutoverPanel).getByText(/evidence snapshot unavailable/i)).toBeTruthy()
    expect(within(cutoverPanel).getByText(/quarantined legacy rows/i).parentElement).toHaveTextContent(/not reported/i)
  })
})
