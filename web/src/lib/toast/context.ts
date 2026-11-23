import { createContext } from 'react'

export type ToastKind = 'success' | 'info' | 'error'

export interface ToastOptions {
  title: string
  description?: string
  action?: { label: string; onClick?: () => void; href?: string }
  /** Overrides the default: 5000 for success and info, none for error. 0 keeps it open. */
  durationMs?: number
  /** A second toast with the same key replaces the first instead of stacking. */
  key?: string
}

export interface ToastApi {
  success: (options: ToastOptions) => string
  error: (options: ToastOptions) => string
  info: (options: ToastOptions) => string
  /** Closes a visible or queued toast by the id a helper returned. */
  close: (id: string) => void
}

export const ToastContext = createContext<ToastApi | null>(null)
