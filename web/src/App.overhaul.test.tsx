import '@testing-library/jest-dom/vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import App from '@/App'
import { setTokenSnapshot } from '@/shared/auth/tokenStore'
import { buildAuthResponse } from '@/test/fixtures'
import { installAppTestHarness, resetApp } from '@/test/app-harness'

describe('removed overhaul workspace', () => {
  installAppTestHarness()

  it('does not expose the retired overhaul route', async () => {
    resetApp('/overhaul')
    setTokenSnapshot(buildAuthResponse())
    render(<App />)

    expect(
      await screen.findByRole('heading', { name: /page not found/i }),
    ).toBeTruthy()
    expect(
      screen.queryByRole('heading', { name: /capital & evidence/i }),
    ).toBeNull()
  })

  it('keeps canonical account valuation in Portfolio', async () => {
    resetApp('/portfolio')
    setTokenSnapshot(buildAuthResponse())
    render(<App />)

    expect(
      await screen.findByRole('heading', { name: /account capital summary/i }),
    ).toBeTruthy()
    expect(await screen.findByText(/canonical valuation/i)).toBeTruthy()
    expect(
      screen.getByText(/no legacy unscoped rows are included/i),
    ).toBeTruthy()

    await userEvent.click(screen.getByRole('tab', { name: /capital ledger/i }))
    expect(
      await screen.findByRole('heading', { name: /^capital ledger$/i }),
    ).toBeTruthy()
    expect(
      await screen.findByRole('table', { name: /capital flow records/i }),
    ).toBeTruthy()
    expect(
      screen.getByText(/not cash, equity, P&L, or spendable buying power/i),
    ).toBeTruthy()
  })

  it('keeps scoped strategy evidence in Reports', async () => {
    resetApp(
      '/strategies/00000000-0000-4000-8000-000000000010?tab=reports&evidence_scope_id=00000000-0000-4000-8000-000000000070',
    )
    setTokenSnapshot(buildAuthResponse())
    render(<App />)

    expect(
      await screen.findByRole('heading', { name: /evidence scope/i }),
    ).toBeTruthy()
    expect(await screen.findByText(/paper validation passed/i)).toBeTruthy()
    expect(
      screen.getByText(/legacy unscoped artifacts are never queried/i),
    ).toBeTruthy()
  })
})
