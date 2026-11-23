import { useContext } from 'react'
import { ToastContext, type ToastApi } from './context'

/** Success, error and info helpers. Each returns the toast id. Needs `ToastProvider` above it. */
export function useToast(): ToastApi {
  const api = useContext(ToastContext)
  if (!api) throw new Error('useToast must be used inside a ToastProvider')
  return api
}
