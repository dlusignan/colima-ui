// In-memory holder for the startup token.
//
// The server prints `http://127.0.0.1:<port>/#token=<token>`. The token is read
// once from the URL fragment, kept in a module variable and never written to
// localStorage or sessionStorage (any script on the origin could read them). A
// reload therefore loses it, and the user reopens the printed link.

export type AuthStatus = 'missing' | 'ok' | 'rejected'

let token: string | null = null
let status: AuthStatus = 'missing'
let captured = false
const listeners = new Set<() => void>()

function emit() {
  listeners.forEach((l) => l())
}

/**
 * Reads `#token=` from the current location, stores it and strips the fragment
 * from the address bar and history entry. Idempotent: later calls do nothing.
 * Call it before the router mounts.
 */
export function captureTokenFromLocation(): void {
  if (captured) return
  captured = true
  const value = new URLSearchParams(window.location.hash.slice(1)).get('token')
  if (!value) return
  token = value
  status = 'ok'
  window.history.replaceState(
    window.history.state,
    '',
    window.location.pathname + window.location.search,
  )
  emit()
}

export function getToken(): string | null {
  return token
}

export function getAuthStatus(): AuthStatus {
  return status
}

/** Called by the API client when the server answers 401. */
export function markTokenRejected(): void {
  if (status === 'rejected') return
  status = 'rejected'
  emit()
}

export function subscribeAuth(listener: () => void): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

/** Test helper: sets the token directly. */
export function setTokenForTests(value: string | null): void {
  captured = true
  token = value
  status = value ? 'ok' : 'missing'
  emit()
}
