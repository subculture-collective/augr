import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { ConsolePanel, HudBadge, HudRow, HudSection, StatusLed, TabButton } from '@/components/ui/hud'

describe('HUD primitives', () => {
  it('renders a console panel with the hud panel class contract', () => {
    render(
      <ConsolePanel data-testid="panel">
        <span>Console payload</span>
      </ConsolePanel>,
    )

    expect(screen.getByText('Console payload')).toBeInTheDocument()
    expect(screen.getByTestId('panel')).toHaveClass('hud-panel')
  })

  it('renders a labeled hud section', () => {
    render(<HudSection label="Operator log" note="Live" />)

    expect(screen.getByRole('region', { name: 'Operator log' })).toBeInTheDocument()
    expect(screen.getByText('Operator log')).toBeInTheDocument()
    expect(screen.getByText('Live')).toBeInTheDocument()
  })

  it('pairs led text with an accessible status label', () => {
    render(<StatusLed label="Broadcast" state="sync" />)

    const led = screen.getByRole('status', { name: /broadcast sync/i })
    expect(led).toHaveTextContent('Broadcast')
    expect(led.querySelector('.hud-led-sync')).not.toBeNull()
  })

  it('renders badge tones without backend data dependencies', () => {
    render(<HudBadge tone="signal">Signal</HudBadge>)

    expect(screen.getByText('Signal')).toBeInTheDocument()
    expect(screen.getByText('Signal')).toHaveClass('hud-badge')
  })

  it('renders tab buttons with role and selection state', () => {
    render(
      <>
        <TabButton active label="Monitor" note="01" />
        <TabButton active={false} label="Audit" />
      </>,
    )

    expect(screen.getByRole('tab', { name: /monitor/i })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('tab', { name: /audit/i })).toHaveAttribute('aria-selected', 'false')
  })

  it('renders row label and value pairing', () => {
    render(<HudRow label="Latency" value="12ms" accent="hsl(var(--signal))" />)

    expect(screen.getByRole('group', { name: 'Latency: 12ms' })).toBeInTheDocument()
    expect(screen.getByText('Latency')).toBeInTheDocument()
    expect(screen.getByText('12ms')).toBeInTheDocument()
  })
})
