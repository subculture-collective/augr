import { type TextareaHTMLAttributes, forwardRef } from 'react'

import { cn } from '@/lib/utils'

const Textarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement>>(
  ({ className, ...props }, ref) => {
    return (
      <textarea
        className={cn(
          'flex min-h-15 w-full rounded-none border border-border bg-panel px-3 py-2 text-sm text-ink shadow-[4px_4px_0_0_rgb(0_0_0_/_0.7)]',
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

Textarea.displayName = 'Textarea'

export { Textarea }
