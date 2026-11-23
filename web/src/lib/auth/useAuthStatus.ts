import { useSyncExternalStore } from 'react'
import { getAuthStatus, subscribeAuth } from './token'

export function useAuthStatus() {
  return useSyncExternalStore(subscribeAuth, getAuthStatus)
}
