import { cleanup, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { AppRoutes } from '@/App'
import { isAuthenticated } from '@/lib/auth'

vi.mock('@/lib/auth', () => ({
  isAuthenticated: vi.fn(),
  getAccessToken: vi.fn().mockReturnValue(null),
}))

vi.mock('@/pages/dashboard-page', () => ({
  DashboardPage: () => <div>Dashboard page</div>,
}))

vi.mock('@/pages/calendar-page', () => ({
  CalendarPage: () => <div>Calendar page</div>,
}))

describe('AppRoutes auth guards', () => {
  beforeEach(() => {
    vi.mocked(isAuthenticated).mockReset()
  })

  afterEach(() => {
    cleanup()
  })

  it('redirects unauthenticated users away from personal dashboard data', () => {
    vi.mocked(isAuthenticated).mockReturnValue(false)

    render(
      <MemoryRouter initialEntries={['/']}>
        <AppRoutes />
      </MemoryRouter>,
    )

    expect(screen.queryByText('Dashboard page')).not.toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Sign in' })).toBeInTheDocument()
  })

  it('allows unauthenticated users to observe public market pages in guest mode', () => {
    vi.mocked(isAuthenticated).mockReturnValue(false)

    render(
      <MemoryRouter initialEntries={['/calendar']}>
        <AppRoutes />
      </MemoryRouter>,
    )

    expect(screen.getByText('Calendar page')).toBeInTheDocument()
    expect(screen.getByText('Guest mode')).toBeInTheDocument()
    expect(screen.queryByText('Portfolio')).not.toBeInTheDocument()
  })

  it('redirects authenticated users away from /login to /', () => {
    vi.mocked(isAuthenticated).mockReturnValue(true)

    render(
      <MemoryRouter initialEntries={['/login']}>
        <AppRoutes />
      </MemoryRouter>,
    )

    expect(screen.getByText('Dashboard page')).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Sign in' })).not.toBeInTheDocument()
  })
})
