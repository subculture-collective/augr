import { useMemo, useState, type ReactNode } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { Copy, Check } from 'lucide-react'
import { useOptionalAccount } from '@/shared/account/AccountProvider'

type EntityKind = 'strategy' | 'run' | 'order' | 'trade' | 'position' | 'decision' | 'event' | 'opportunity' | 'risk'

type Crumb = {
  label: ReactNode
  to?: string
}

function shortId(value: string) {
  if (value.length <= 16) return value
  return `${value.slice(0, 8)}…${value.slice(-4)}`
}

function defaultEntityLabel(kind: EntityKind, id: string, label?: string) {
  if (label) return label
  return `${kind[0]!.toUpperCase()}${kind.slice(1)} ${shortId(id)}`
}

function hrefFor(kind: EntityKind, id: string, accountId?: string, tradeDate?: string) {
  const base = accountId ? `/accounts/${accountId}` : ''
  switch (kind) {
    case 'strategy': return `/strategies/${id}`
    case 'run': return tradeDate ? `${base}/runs/${id}?trade_date=${encodeURIComponent(tradeDate)}` : undefined
    case 'order': return `${base}/orders/${id}`
    case 'position': return `${base}/trades?position_id=${encodeURIComponent(id)}`
    case 'trade': return `${base}/trades?trade_id=${encodeURIComponent(id)}`
    case 'decision': return `${base}/events?decision_id=${encodeURIComponent(id)}`
    case 'event': return `${base}/events?event_id=${encodeURIComponent(id)}`
    case 'opportunity': return `${base}/portfolio?tab=allocator&opportunity_id=${encodeURIComponent(id)}`
    case 'risk': return `${base}/risk`
  }
}

function withSourceContext(href: string, from: string) {
  if (!from.includes('?')) return href
  const [path, query = ''] = href.split('?')
  const params = new URLSearchParams(query)
  if (!params.has('from')) params.set('from', from)
  return `${path}?${params.toString()}`
}

export function CopyButton({ value, label = 'Copy ID' }: { value: string; label?: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <button
      type="button"
      className="btn-icon copy-btn"
      onClick={() => {
        void navigator.clipboard?.writeText(value)
        setCopied(true)
        window.setTimeout(() => setCopied(false), 1200)
      }}
      aria-label={label}
    >
      {copied ? <Check size={14} /> : <Copy size={14} />}
    </button>
  )
}

export function EntityLink({ kind, id, label, accountId, tradeDate, preserveContext = true, copy = true }: { kind: EntityKind; id?: string; label?: string; accountId?: string; tradeDate?: string; preserveContext?: boolean; copy?: boolean }) {
  const accountContext = useOptionalAccount()
  const resolvedAccountId = accountId ?? accountContext?.account.id
  const location = useLocation()
  const from = `${location.pathname}${location.search}`
  const baseHref = id ? hrefFor(kind, id, resolvedAccountId, tradeDate) : undefined
  const href = useMemo(() => baseHref ? withSourceContext(baseHref, from) : undefined, [baseHref, from])
  if (!id) return <span className="muted">No {kind} ID recorded</span>
  const text = defaultEntityLabel(kind, id, label)
  return (
    <span className="entity-link">
      {href ? <Link to={preserveContext ? href : baseHref!}>{text}</Link> : <span title="Run trade date is required for navigation">{text}</span>}
      {copy ? <> <CopyButton value={id} label={`Copy ${kind} ID`} /></> : null}
    </span>
  )
}

export function EntityId({ kind, id, label }: { kind: EntityKind; id?: string; label?: string }) {
  if (!id) return <span className="muted">No {kind} ID recorded</span>
  return (
    <span className="entity-id">
      <code title={id}>{label ?? shortId(id)}</code> <CopyButton value={id} label={`Copy ${kind} ID`} />
    </span>
  )
}

export function Breadcrumbs({ items }: { items: Crumb[] }) {
  return (
    <nav className="breadcrumbs" aria-label="Breadcrumbs">
      {items.map((item, index) => (
        <span key={index} className="breadcrumb-item">
          {index > 0 ? <span aria-hidden="true">/</span> : null}
          {item.to ? <Link to={item.to}>{item.label}</Link> : <span>{item.label}</span>}
        </span>
      ))}
    </nav>
  )
}
