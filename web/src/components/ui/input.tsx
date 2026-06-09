import { type InputHTMLAttributes, forwardRef } from 'react'

import { cn } from '@/lib/utils'

const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(
  ({ className, type, ...props }, ref) => {
    return (
      <input
        type={type}
        className={cn(
          'flex h-9 w-full rounded-none border border-border bg-panel px-3 py-1 text-sm text-ink shadow-[4px_4px_0_0_rgb(0_0_0_/_0.7)] transition-colors',
          'file:border-0 file:bg-transparent file:text-sm file:font-medium',
          'placeholder:text-ink-mute',
          'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-pulse focus-visible:ring-offset-0',
          'disabled:cursor-not-allowed disabled:opacity-50',
          className,
        )}
        ref={ref}
        {...props}
      />
    )
  },
)

Input.displayName = 'Input'

export { Input }
