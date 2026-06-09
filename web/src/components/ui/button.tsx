/* eslint-disable react-refresh/only-export-components */
import { Slot } from '@radix-ui/react-slot'
import { cva, type VariantProps } from 'class-variance-authority'
import { type ComponentProps } from 'react'

import { cn } from '@/lib/utils'

const buttonVariants = cva(
  'inline-flex cursor-pointer items-center justify-center gap-2 whitespace-nowrap rounded-none border text-hud font-medium uppercase tracking-[0.12em] transition-colors disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:size-4 [&_svg]:shrink-0 outline-none focus-visible:ring-1 focus-visible:ring-pulse focus-visible:ring-offset-0',
  {
    variants: {
      variant: {
        default: 'border-signal bg-signal text-void shadow-[6px_6px_0_0_rgb(0_0_0_/_0.88)] hover:bg-signal-bright',
        secondary: 'border-border bg-panel text-ink shadow-[6px_6px_0_0_rgb(0_0_0_/_0.82)] hover:border-border-strong hover:bg-panel-raised',
        outline: 'border-border bg-void text-ink-dim hover:border-pulse hover:bg-panel hover:text-ink',
        ghost: 'border-transparent bg-transparent text-ink-dim hover:border-border-faint hover:bg-panel/60 hover:text-ink',
        destructive: 'border-alert bg-alert text-void shadow-[6px_6px_0_0_rgb(0_0_0_/_0.88)] hover:bg-red-500',
        success: 'border-confirm bg-confirm text-void shadow-[6px_6px_0_0_rgb(0_0_0_/_0.88)] hover:bg-emerald-400',
        warning: 'border-caution bg-caution text-void shadow-[6px_6px_0_0_rgb(0_0_0_/_0.88)] hover:bg-amber-400',
      },
      size: {
        default: 'h-9 px-3.5 py-2',
        sm: 'h-8 px-3 text-[11px]',
        lg: 'h-10 px-5',
        dense: 'h-8 px-2.5 text-[11px]',
      },
    },
    defaultVariants: {
      variant: 'default',
      size: 'default',
    },
  },
)

type ButtonProps = ComponentProps<'button'> &
  VariantProps<typeof buttonVariants> & {
    asChild?: boolean
  }

function Button({ className, variant, size, asChild = false, ...props }: ButtonProps) {
  const Comp = asChild ? Slot : 'button'

  return <Comp className={cn(buttonVariants({ variant, size, className }))} {...props} />
}

export { Button, buttonVariants }
