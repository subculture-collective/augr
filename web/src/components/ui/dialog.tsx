import { type HTMLAttributes, type MouseEvent, createContext, forwardRef, useCallback, useContext, useEffect, useId, useRef } from 'react'

import { cn } from '@/lib/utils'

interface DialogContextValue {
  titleId: string
  descriptionId: string
}

const DialogContext = createContext<DialogContextValue | null>(null)

interface DialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  children: React.ReactNode
}

export function Dialog({ open, onOpenChange, children }: DialogProps) {
  const dialogRef = useRef<HTMLDivElement>(null)
  const previousFocusRef = useRef<HTMLElement | null>(null)
  const titleId = useId()
  const descriptionId = useId()

  useEffect(() => {
    if (!open) return

    previousFocusRef.current = document.activeElement as HTMLElement | null

    // Move focus into the dialog
    requestAnimationFrame(() => {
      const firstFocusable = dialogRef.current?.querySelector<HTMLElement>(
        'input, button, textarea, select, [tabindex]:not([tabindex="-1"])',
      )
      firstFocusable?.focus()
    })

    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        onOpenChange(false)
      }
    }

    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('keydown', handleKeyDown)
      // Restore focus on close
      previousFocusRef.current?.focus()
    }
  }, [open, onOpenChange])

  if (!open) return null

  return (
    <DialogContext.Provider value={{ titleId, descriptionId }}>
      <div className="fixed inset-0 z-50 flex items-center justify-center bg-void/90 hud-scan">
        <div
          className="fixed inset-0 bg-void/85"
          onClick={() => onOpenChange(false)}
          data-testid="dialog-overlay"
        />
        <div
          ref={dialogRef}
          role="dialog"
          aria-modal="true"
          aria-labelledby={titleId}
          aria-describedby={descriptionId}
          className="relative z-50 w-full max-w-lg px-4 overlay-safe sm:px-0"
        >
          {children}
        </div>
      </div>
    </DialogContext.Provider>
  )
}

const DialogContent = forwardRef<HTMLDivElement, HTMLAttributes<HTMLDivElement>>(
  ({ className, onClick, ...props }, ref) => {
    const handleClick = useCallback(
      (e: MouseEvent<HTMLDivElement>) => {
        e.stopPropagation()
        onClick?.(e)
      },
      [onClick],
    )

    return (
      <div
        ref={ref}
        className={cn(
          'hud-panel rounded-none p-5',
          className,
        )}
        onClick={handleClick}
        {...props}
      />
    )
  },
)

DialogContent.displayName = 'DialogContent'

function DialogHeader({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('mb-4 space-y-2 border-b border-border-faint pb-4', className)} {...props} />
}

function DialogTitle({ className, ...props }: HTMLAttributes<HTMLHeadingElement>) {
  const ctx = useContext(DialogContext)
  return <h2 id={ctx?.titleId} className={cn('text-lg font-semibold uppercase leading-none tracking-[0.08em]', className)} {...props} />
}

function DialogDescription({ className, ...props }: HTMLAttributes<HTMLParagraphElement>) {
  const ctx = useContext(DialogContext)
  return <p id={ctx?.descriptionId} className={cn('text-sm text-ink-dim', className)} {...props} />
}

function DialogFooter({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('mt-6 flex justify-end gap-2 border-t border-border-faint pt-4', className)} {...props} />
}

export { DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter }
