import { Toast as ToastPrimitive } from '@base-ui/react/toast'
import {
  ToastAction,
  ToastClose,
  ToastDescription,
  ToastRoot,
  ToastTitle,
  ToastViewport,
} from '@/components/ui/toast'
import { cn } from '@/lib/utils'
import type { ToastData } from './ToastProvider'

const KIND_BORDER: Record<string, string> = {
  success: 'border-success',
  error: 'border-destructive',
  info: 'border-border',
}

/** The viewport and the toasts in it, newest first. Render once, inside `ToastProvider`. */
export function Toaster() {
  const { toasts, close } = ToastPrimitive.useToastManager<ToastData>()
  return (
    <ToastViewport>
      {toasts.map((toast) => {
        const action = toast.data?.action
        const isError = toast.type === 'error'
        return (
          <ToastRoot
            key={toast.id}
            toast={toast}
            // Errors are assertive, the rest polite. Base UI's own default is a dialog role.
            role={isError ? 'alert' : 'status'}
            aria-modal={undefined}
            className={cn(KIND_BORDER[toast.type ?? 'info'])}
          >
            <ToastTitle />
            <ToastDescription />
            {action?.href ? (
              <ToastAction
                nativeButton={false}
                role="link"
                render={<a href={action.href} />}
                onClick={() => {
                  action.onClick?.()
                  close(toast.id)
                }}
              >
                {action.label}
              </ToastAction>
            ) : action ? (
              <ToastAction
                onClick={() => {
                  action.onClick?.()
                  close(toast.id)
                }}
              >
                {action.label}
              </ToastAction>
            ) : null}
            <ToastClose />
          </ToastRoot>
        )
      })}
    </ToastViewport>
  )
}
