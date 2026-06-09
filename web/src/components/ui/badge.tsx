/* eslint-disable react-refresh/only-export-components */
import { cva, type VariantProps } from 'class-variance-authority'
import { type ComponentProps } from 'react'

import { cn } from '@/lib/utils'

const badgeVariants = cva(
  'inline-flex items-center gap-1 rounded-none border px-2 py-0.5 font-mono text-[10px] font-bold uppercase tracking-[0.16em] transition-colors',
  {
    variants: {
      variant: {
        default: 'border-signal bg-signal/12 text-signal',
        secondary: 'border-border bg-panel text-ink',
        outline: 'border-border bg-void text-ink-dim',
        destructive: 'border-alert bg-alert/12 text-alert',
        success: 'border-confirm bg-confirm/12 text-confirm',
        warning: 'border-caution bg-caution/12 text-caution',
      },
    },
    defaultVariants: {
      variant: 'default',
    },
  },
)

type BadgeProps = ComponentProps<'span'> & VariantProps<typeof badgeVariants>

function Badge({ className, variant, ...props }: BadgeProps) {
  return <span className={cn(badgeVariants({ variant, className }))} {...props} />
}

export { Badge, badgeVariants }
