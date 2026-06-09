import type { ReactNode } from 'react'

import { HudBadge } from '@/components/ui/hud'
import { cn } from '@/lib/utils'

interface PageHeaderProps {
  eyebrow?: string
  title: string
  description?: string
  meta?: ReactNode
  actions?: ReactNode
  className?: string
}

export function PageHeader({ eyebrow, title, description, meta, actions, className }: PageHeaderProps) {
  return (
    <section
      className={cn(
        'hud-panel rounded-none px-4 py-4',
        className,
      )}
    >
      <div className="flex flex-col gap-3 xl:flex-row xl:items-end xl:justify-between">
        <div className="min-w-0 space-y-2">
          {eyebrow ? <HudBadge tone="ink">{eyebrow}</HudBadge> : null}
          <div className="flex flex-wrap items-center gap-2.5">
            <h1 className="text-lg font-semibold uppercase tracking-[0.08em] text-ink">
              {title}
            </h1>
            {meta}
          </div>
          {description ? <p className="max-w-3xl text-sm text-ink-dim">{description}</p> : null}
        </div>
        {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
      </div>
    </section>
  )
}
