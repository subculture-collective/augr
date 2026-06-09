import { type LabelHTMLAttributes, forwardRef } from 'react'

import { cn } from '@/lib/utils'

const Label = forwardRef<HTMLLabelElement, LabelHTMLAttributes<HTMLLabelElement>>(
  ({ className, ...props }, ref) => {
    return (
      <label
        className={cn(
          'font-mono text-[10px] font-bold uppercase leading-none tracking-[0.16em] text-ink-dim peer-disabled:cursor-not-allowed peer-disabled:opacity-70',
          className,
        )}
        ref={ref}
        {...props}
      />
    )
  },
)

Label.displayName = 'Label'

export { Label }
