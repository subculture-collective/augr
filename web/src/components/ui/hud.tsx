import { type ButtonHTMLAttributes, type ComponentPropsWithoutRef, type ReactNode, forwardRef } from 'react'

import { cn } from '@/lib/utils'

type StatusLedState = 'live' | 'sync' | 'ok' | 'warn' | 'dead'
type HudBadgeTone = 'signal' | 'pulse' | 'alert' | 'caution' | 'confirm' | 'dead' | 'ink'

const badgeToneClasses: Record<HudBadgeTone, string> = {
  signal: 'border-signal/35 bg-signal/12 text-signal',
  pulse: 'border-pulse/35 bg-pulse/12 text-pulse',
  alert: 'border-alert/35 bg-alert/12 text-alert',
  caution: 'border-caution/35 bg-caution/12 text-caution',
  confirm: 'border-confirm/35 bg-confirm/12 text-confirm',
  dead: 'border-border-faint bg-void-800 text-ink-mute',
  ink: 'border-border bg-panel text-ink',
}

const ledStateClasses: Record<StatusLedState, string> = {
  live: 'hud-led-live',
  sync: 'hud-led-sync',
  ok: 'hud-led-ok',
  warn: 'hud-led-warn',
  dead: '',
}

function primitiveLabel(value: ReactNode) {
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
    return String(value)
  }

  return 'value'
}

type ConsolePanelProps = ComponentPropsWithoutRef<'div'>

const ConsolePanel = forwardRef<HTMLDivElement, ConsolePanelProps>(function ConsolePanel(
  { className, ...props },
  ref,
) {
  return <div ref={ref} className={cn('hud-panel rounded-none', className)} {...props} />
})

interface HudSectionProps {
  label: string
  note?: ReactNode
  className?: string
}

function HudSection({ label, note, className }: HudSectionProps) {
  return (
    <section aria-label={label} className={cn('hud-section', className)}>
      <span className="hud-section-label">{label}</span>
      <span aria-hidden="true" className="hud-section-line" />
      {note ? <span className="text-2xs text-ink-dim">{note}</span> : null}
    </section>
  )
}

interface StatusLedProps {
  state: StatusLedState
  label: string
}

function StatusLed({ state, label }: StatusLedProps) {
  const ledClassName = ledStateClasses[state]
  const accessibleLabel = `${label} ${state}`

  return (
    <span aria-label={accessibleLabel} aria-live="polite" aria-atomic="true" className="inline-flex items-center gap-2 text-2xs uppercase tracking-[0.12em] text-ink-dim" role="status">
      <span aria-hidden="true" className={cn('hud-led', ledClassName)} />
      <span>{label}</span>
    </span>
  )
}

interface HudBadgeProps extends ComponentPropsWithoutRef<'span'> {
  tone?: HudBadgeTone
}

function HudBadge({ className, tone = 'ink', ...props }: HudBadgeProps) {
  return <span className={cn('hud-badge', badgeToneClasses[tone], className)} {...props} />
}

interface TabButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  active: boolean
  label: string
  note?: ReactNode
}

const TabButton = forwardRef<HTMLButtonElement, TabButtonProps>(function TabButton(
  { active, label, note, className, type = 'button', ...props },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      role="tab"
      aria-selected={active}
      tabIndex={active ? 0 : -1}
      className={cn(
        'inline-flex items-center gap-2 border px-3 py-2 text-hud uppercase tracking-[0.12em] transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-pulse disabled:pointer-events-none disabled:opacity-50',
        active
          ? 'border-pulse bg-panel-raised text-ink shadow-[6px_6px_0_0_rgb(0_0_0_/_0.88)]'
          : 'border-border bg-panel text-ink-dim hover:border-pulse hover:bg-panel-raised hover:text-ink',
        className,
      )}
      {...props}
    >
      <span>{label}</span>
      {note ? <span className="text-2xs text-ink-dim">{note}</span> : null}
    </button>
  )
})

interface HudRowProps extends ComponentPropsWithoutRef<'div'> {
  label: string
  value: ReactNode
  accent?: string
}

const HudRow = forwardRef<HTMLDivElement, HudRowProps>(function HudRow(
  { label, value, accent, className, ...props },
  ref,
) {
  return (
    <div ref={ref} aria-label={`${label}: ${primitiveLabel(value)}`} className={cn('hud-row', className)} role="group" {...props}>
      <span className="hud-row-key">{label}</span>
      <span className="hud-row-val" style={accent ? { color: accent } : undefined}>
        {value}
      </span>
    </div>
  )
})

export { ConsolePanel, HudBadge, HudRow, HudSection, StatusLed, TabButton }
export type { HudBadgeTone, StatusLedState }
