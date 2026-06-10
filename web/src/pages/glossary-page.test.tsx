import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it } from 'vitest'

import { GlossaryPage, GLOSSARY_TERMS } from '@/pages/glossary-page'

function renderPage(path = '/glossary') {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/glossary" element={<GlossaryPage />} />
        <Route path="/glossary/:slug" element={<GlossaryPage />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('GlossaryPage', () => {
  afterEach(() => {
    cleanup()
  })

  it('renders the glossary index and all terms by default', () => {
    renderPage()
    expect(screen.getByTestId('glossary-page')).toBeInTheDocument()

    for (const term of GLOSSARY_TERMS) {
      expect(screen.getByTestId(`glossary-term-${term.slug}`)).toBeInTheDocument()
    }
  })

  it('links index cards to term detail pages', () => {
    renderPage()

    const card = screen.getByTestId('glossary-term-eps')
    expect(within(card).getByRole('link', { name: 'EPS' })).toHaveAttribute('href', '/glossary/eps')
    expect(within(card).getByRole('link', { name: 'Open detail' })).toHaveAttribute('href', '/glossary/eps')
  })

  it('filters terms by search query (term name)', () => {
    renderPage()
    const input = screen.getByTestId('glossary-search')
    fireEvent.change(input, { target: { value: 'kill switch' } })

    expect(screen.getByTestId('glossary-term-kill-switch')).toBeInTheDocument()
    expect(screen.queryByTestId('glossary-term-backtest')).not.toBeInTheDocument()
  })

  it('filters terms by formula and definition text', () => {
    renderPage()
    const input = screen.getByTestId('glossary-search')
    fireEvent.change(input, { target: { value: 'weighted average diluted shares' } })

    expect(screen.getByTestId('glossary-term-eps')).toBeInTheDocument()
  })

  it('renders a detail route with back and anchor links', () => {
    renderPage('/glossary/eps')

    expect(screen.getByText('EPS')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Back to index' })).toHaveAttribute('href', '/glossary')
    expect(screen.getByRole('link', { name: 'Formula' })).toHaveAttribute('href', '#formula')
  })

  it('shows formula text on a detail page', () => {
    renderPage('/glossary/eps')

    expect(
      screen.getByText('EPS = (net income - preferred dividends) / weighted average diluted shares'),
    ).toBeInTheDocument()
  })

  it('links related terms from the detail page', () => {
    renderPage('/glossary/eps')

    const relatedSection = screen.getByRole('heading', { name: 'Related terms' }).closest('section')
    expect(relatedSection).not.toBeNull()

    const scoped = within(relatedSection as HTMLElement)
    expect(scoped.getByRole('link', { name: 'SEC Filing' })).toHaveAttribute('href', '/glossary/sec-filing')
    expect(scoped.getByRole('link', { name: 'Sentiment' })).toHaveAttribute('href', '/glossary/sentiment')
  })

  it('only references glossary terms that exist', () => {
    const knownSlugs = new Set(GLOSSARY_TERMS.map((term) => term.slug))

    for (const term of GLOSSARY_TERMS) {
      for (const relatedSlug of term.related) {
        expect(knownSlugs.has(relatedSlug), `${term.slug} references ${relatedSlug}`).toBe(true)
      }
    }
  })

  it('groups terms alphabetically', () => {
    renderPage()
    const headings = screen.getAllByRole('heading', { level: 2 })
    const letters = headings
      .filter((heading) => heading.id.startsWith('glossary-letter-'))
      .map((heading) => heading.textContent)
    const sorted = [...letters].sort()
    expect(letters).toEqual(sorted)
  })
})
