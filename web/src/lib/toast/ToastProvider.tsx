import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { Toast as ToastPrimitive } from '@base-ui/react/toast'
import { ToastContext, type ToastApi, type ToastKind, type ToastOptions } from './context'

/** At most this many toasts show; the rest wait in a queue and appear as slots free up. */
export const MAX_VISIBLE = 3
export const AUTO_DISMISS_MS = 5000

export interface ToastData {
  action?: ToastOptions['action']
}

type Manager = ReturnType<typeof ToastPrimitive.createToastManager<ToastData>>

interface Entry {
  id: string
  kind: ToastKind
  options: ToastOptions
}

function createController(manager: Manager): ToastApi & { connect: () => () => void } {
  // id -> auto-dismiss delay, for the toasts that have one.
  const active = new Map<string, number>()
  let pending: Entry[] = []
  let counter = 0

  const release = (id: string) => {
    if (!active.delete(id)) return
    const next = pending.shift()
    if (next) start(next)
  }

  function start({ id, kind, options }: Entry) {
    const timeout = options.durationMs ?? (kind === 'error' ? 0 : AUTO_DISMISS_MS)
    active.set(id, timeout)
    manager.add({
      id,
      type: kind,
      title: options.title,
      description: options.description,
      timeout: document.hidden ? 0 : timeout,
      data: { action: options.action },
      onClose: () => release(id),
    })
  }

  const show = (kind: ToastKind, options: ToastOptions): string => {
    const id = options.key ?? `toast-${++counter}`
    const entry: Entry = { id, kind, options }
    if (active.has(id)) {
      // Same id on a visible toast updates it in place and restarts its timer.
      start(entry)
    } else if (pending.some((p) => p.id === id)) {
      pending = pending.map((p) => (p.id === id ? entry : p))
    } else if (active.size < MAX_VISIBLE) {
      start(entry)
    } else {
      pending.push(entry)
    }
    return id
  }

  /**
   * Base UI pauses on hover, focus and window blur, but a hidden tab does not
   * always blur the window. While hidden every timer is cleared; on return each
   * auto-dismiss toast gets its full delay again, so none vanishes unseen.
   */
  const onVisibilityChange = () => {
    for (const [id, timeout] of active) {
      if (timeout > 0) manager.update(id, { timeout: document.hidden ? 0 : timeout })
    }
  }

  return {
    connect: () => {
      document.addEventListener('visibilitychange', onVisibilityChange)
      return () => document.removeEventListener('visibilitychange', onVisibilityChange)
    },
    success: (o) => show('success', o),
    error: (o) => show('error', o),
    info: (o) => show('info', o),
    close: (id) => {
      pending = pending.filter((p) => p.id !== id)
      if (active.has(id)) manager.close(id)
    },
  }
}

/** Mount once near the root, with `Toaster` somewhere inside it. */
export function ToastProvider({ children }: { children: ReactNode }) {
  const [manager] = useState(() => ToastPrimitive.createToastManager<ToastData>())
  const controller = useMemo(() => createController(manager), [manager])
  useEffect(() => controller.connect(), [controller])
  const api = useMemo<ToastApi>(
    () => ({ success: controller.success, error: controller.error, info: controller.info, close: controller.close }),
    [controller],
  )
  return (
    <ToastContext.Provider value={api}>
      <ToastPrimitive.Provider toastManager={manager} limit={MAX_VISIBLE} timeout={AUTO_DISMISS_MS}>
        {children}
      </ToastPrimitive.Provider>
    </ToastContext.Provider>
  )
}
