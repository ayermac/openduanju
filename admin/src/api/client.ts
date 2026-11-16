// Shared admin HTTP client. Authentication is a session cookie set by the
// server; the CSRF token of that session is kept in memory and echoed on
// every mutating call. A 401 means the session is gone and the UI returns to
// the login page instead of pretending the call failed for another reason.

export interface Problem {
  code: number
  reason: string
  message: string
  // metadata carries the trace identity of the failed request. It is what a
  // user quotes when reporting the problem, and what the server log and the
  // audit rows are searched by.
  metadata?: Record<string, string>
}

export class ApiError extends Error {
  readonly code: number
  readonly reason: string
  // traceId is the report number of this request; empty when the response did
  // not carry one.
  readonly traceId: string

  constructor(problem: Partial<Problem>, fallback: string) {
    const traceId = problem.metadata?.trace_id ?? ''
    const message = problem.message || fallback
    super(traceId ? `${message}（报障编号 ${traceId}）` : message)
    this.code = problem.code ?? 0
    this.reason = problem.reason ?? 'UNKNOWN'
    this.traceId = traceId
  }
}

let csrfToken = ''

export function setCSRFToken(token: string): void {
  csrfToken = token
}

export function getCSRFToken(): string {
  return csrfToken
}

// request sends one admin call. Cookies travel with the request, and a
// mutating call carries the CSRF token of the current session.
// Unauthorized events are handled once by the session/router layer: the page
// returns to login with the current internal path. A 403 is a permission
// answer and must stay a normal error for the page to explain.
export const UNAUTHORIZED_EVENT = 'openduanju:unauthorized'

function safeInternalPath(): string {
  const path = `${window.location.pathname}${window.location.search}`
  return path.startsWith('/') && !path.startsWith('//') ? path : '/'
}

export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers)
  if (init?.body) headers.set('Content-Type', 'application/json')
  if (csrfToken) headers.set('X-CSRF-Token', csrfToken)
  const method = (init?.method ?? 'GET').toUpperCase()
  const response = await fetch(path, {
    ...init,
    method,
    headers,
    credentials: 'include',
  })
  if (!response.ok) {
    const problem = (await response.json().catch(() => ({}))) as Partial<Problem>
    if (response.status === 401) {
      csrfToken = ''
      window.dispatchEvent(
        new CustomEvent(UNAUTHORIZED_EVENT, {
          detail: { next: safeInternalPath(), path, reason: problem.reason ?? '' },
        }),
      )
    }
    throw new ApiError(problem, `请求失败（HTTP ${response.status}）`)
  }
  if (response.status === 204) return undefined as T
  return (await response.json()) as T
}
