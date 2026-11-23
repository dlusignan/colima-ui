import { getToken, markTokenRejected } from '@/lib/auth/token'

/** An error answer from the API, or a failure to get one (status 0). */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly details: Record<string, unknown>

  constructor(
    status: number,
    code: string,
    message: string,
    details: Record<string, unknown> = {},
  ) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.details = details
  }
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

/**
 * Decodes an error body. Accepts the contract shape
 * `{"error":{"code","message","details"}}` and the plain string shape
 * `{"error":"<code>"}` that the middleware emits today. Anything else is
 * `bad_response`.
 */
export function decodeError(status: number, body: unknown): ApiError {
  if (isRecord(body)) {
    const err = body.error
    if (typeof err === 'string') {
      return new ApiError(status, err, err)
    }
    if (isRecord(err) && typeof err.code === 'string') {
      return new ApiError(
        status,
        err.code,
        typeof err.message === 'string' ? err.message : err.code,
        isRecord(err.details) ? err.details : {},
      )
    }
  }
  return new ApiError(
    status,
    'bad_response',
    `Unexpected response from the server (HTTP ${status}).`,
  )
}

export interface RequestOptions {
  body?: unknown
  signal?: AbortSignal
}

/**
 * The single place that talks to the backend. Adds the bearer token, sends
 * relative URLs only, and turns every failure into an ApiError. Without a token
 * no request is sent. A 401 marks the token as rejected.
 */
export async function request<T>(
  method: string,
  url: string,
  options: RequestOptions = {},
): Promise<T> {
  const token = getToken()
  if (!token) {
    throw new ApiError(0, 'missing_token', 'No token. Reopen the link printed by the server.')
  }
  const headers: Record<string, string> = { Authorization: `Bearer ${token}` }
  const init: RequestInit = { method, headers, signal: options.signal }
  if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    init.body = JSON.stringify(options.body)
  }

  let res: Response
  try {
    res = await fetch(url, init)
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e
    throw new ApiError(0, 'network_error', 'Could not reach the server.')
  }

  let parsed: unknown
  let parseFailed = false
  try {
    parsed = await res.json()
  } catch {
    parseFailed = true
  }

  if (res.status === 401) markTokenRejected()
  if (!res.ok) {
    throw decodeError(res.status, parseFailed ? undefined : parsed)
  }
  if (parseFailed) {
    throw new ApiError(res.status, 'bad_response', 'The server sent a body that is not JSON.')
  }
  return parsed as T
}
