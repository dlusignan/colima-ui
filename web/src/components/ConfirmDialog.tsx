import { useId, useRef, useState, type ReactElement, type ReactNode } from 'react'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog'
import { Input } from '@/components/ui/input'

/** The wording and options of a confirm dialog, without the wiring. */
export type ConfirmDialogCopy = Pick<
  ConfirmDialogProps,
  'title' | 'description' | 'confirmLabel' | 'cancelLabel' | 'destructive' | 'items' | 'requireText'
>

interface ConfirmDialogProps {
  /** The element that opens the dialog; focus returns to it on close. */
  trigger?: ReactElement
  /** Controlled mode, for callers without a trigger (the command palette). */
  open?: boolean
  onOpenChange?: (open: boolean) => void
  title: string
  description: string
  confirmLabel: string
  cancelLabel?: string
  destructive?: boolean
  /** What the action touches, listed under the description (names, not counts). */
  items?: string[]
  /**
   * Type-to-confirm: the confirm button stays disabled until the input holds
   * exactly this text (case and spacing count). The input clears on close.
   */
  requireText?: string
  /** Extra content between the description and the type-to-confirm field (an option checkbox). */
  children?: ReactNode
  onConfirm: () => void
}

/**
 * Cancel is the default focus, Escape cancels, confirming closes the dialog.
 * `items` lists affected objects and `requireText` adds type-the-name
 * confirmation; both are optional and the plain form is unchanged.
 */
export function ConfirmDialog({
  trigger,
  open: openProp,
  onOpenChange,
  title,
  description,
  confirmLabel,
  cancelLabel = 'Cancel',
  destructive = false,
  items,
  requireText,
  children,
  onConfirm,
}: ConfirmDialogProps) {
  const [inner, setInner] = useState(false)
  const [typed, setTyped] = useState('')
  const inputId = useId()
  const cancelRef = useRef<HTMLButtonElement>(null)
  const open = openProp ?? inner
  const setOpen = (next: boolean) => {
    if (openProp === undefined) setInner(next)
    if (!next) setTyped('')
    onOpenChange?.(next)
  }
  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      {trigger && <AlertDialogTrigger render={trigger} />}
      <AlertDialogContent initialFocus={cancelRef}>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        {items && items.length > 0 && (
          <ul aria-label="Affected items" className="max-h-40 list-inside list-disc overflow-y-auto text-sm">
            {items.map((item) => (
              <li key={item} className="break-all font-mono">
                {item}
              </li>
            ))}
          </ul>
        )}
        {children}
        {requireText !== undefined && (
          <div className="flex flex-col gap-1.5 text-sm">
            <label htmlFor={inputId}>
              Type <span className="font-mono font-semibold">{requireText}</span> to confirm
            </label>
            <Input
              id={inputId}
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              autoComplete="off"
              spellCheck={false}
            />
          </div>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel ref={cancelRef}>{cancelLabel}</AlertDialogCancel>
          <AlertDialogAction
            variant={destructive ? 'destructive' : 'default'}
            disabled={requireText !== undefined && typed !== requireText}
            onClick={() => {
              setOpen(false)
              onConfirm()
            }}
          >
            {confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
