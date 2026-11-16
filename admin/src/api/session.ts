// Session, account and role helpers. The server is the authority on roles;
// the UI only uses these values to route and to explain a 403.

import { request, setCSRFToken, UNAUTHORIZED_EVENT } from './client'

export { ApiError, getCSRFToken, setCSRFToken } from './client'

export type StaffRole = 'admin' | 'operator' | 'reviewer'

export interface Staff {
  id: number
  username: string
  display_name?: string
  role: StaffRole
  enabled?: boolean
  version: number
  last_login_at?: string
  created_at: string
  updated_at: string
}

export interface AdminSession {
  staff: Staff
  permissions: string[]
  csrf_token: string
  expires_at: string
}

export interface StaffSet {
  items: Staff[]
  total: number
  page: number
  page_size: number
}

let current: AdminSession | null = null

// Any 401 invalidates the cached identity and CSRF token immediately; the
// router listens for the same event to send the user back to login.
window.addEventListener(UNAUTHORIZED_EVENT, () => {
  current = null
  setCSRFToken('')
})

// currentSession returns the cached session of this page load.
export function currentSession(): AdminSession | null {
  return current
}

// loadSession asks the server who the caller is. A 401 clears the cache.
export async function loadSession(): Promise<AdminSession | null> {
  try {
    const session = await request<AdminSession>('/api/admin/v1/auth/me')
    current = session
    setCSRFToken(session.csrf_token)
    return session
  } catch {
    current = null
    setCSRFToken('')
    return null
  }
}

export async function login(username: string, password: string): Promise<AdminSession> {
  const session = await request<AdminSession>('/api/admin/v1/auth/login', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  })
  current = session
  setCSRFToken(session.csrf_token)
  return session
}

export async function logout(): Promise<void> {
  try {
    await request<unknown>('/api/admin/v1/auth/logout', { method: 'POST', body: '{}' })
  } finally {
    current = null
    setCSRFToken('')
  }
}

export function roleLabel(role: string): string {
  switch (role) {
    case 'admin':
      return '管理员'
    case 'operator':
      return '运营'
    case 'reviewer':
      return '审核员'
    default:
      return role
  }
}

// can reports whether the current session holds a permission the server
// returned. It is a UI hint; the server re-checks every call.
export function can(permission: string): boolean {
  return current?.permissions.includes(permission) ?? false
}

export function listStaff(params: {
  page?: number
  pageSize?: number
  role?: string
  q?: string
  enabled?: boolean
}): Promise<StaffSet> {
  const search = new URLSearchParams({
    page: String(params.page ?? 1),
    page_size: String(params.pageSize ?? 20),
  })
  if (params.role) search.set('role', params.role)
  if (params.q) search.set('q', params.q)
  if (params.enabled !== undefined) search.set('enabled', String(params.enabled))
  return request<StaffSet>(`/api/admin/v1/staff?${search.toString()}`)
}

export function createStaff(input: {
  username: string
  displayName: string
  role: StaffRole
  password: string
}): Promise<Staff> {
  return request<Staff>('/api/admin/v1/staff', {
    method: 'POST',
    body: JSON.stringify({
      username: input.username,
      display_name: input.displayName,
      role: input.role,
      password: input.password,
    }),
  })
}

export function updateStaff(
  id: number,
  expectedVersion: number,
  patch: { displayName?: string; role?: StaffRole; enabled?: boolean },
): Promise<Staff> {
  const body: Record<string, unknown> = { expected_version: expectedVersion }
  if (patch.displayName !== undefined) body.display_name = patch.displayName
  if (patch.role !== undefined) body.role = patch.role
  if (patch.enabled !== undefined) body.enabled = patch.enabled
  return request<Staff>(`/api/admin/v1/staff/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(body),
  })
}

export function resetPassword(
  id: number,
  expectedVersion: number,
  newPassword: string,
): Promise<Staff> {
  return request<Staff>(`/api/admin/v1/staff/${id}/reset-password`, {
    method: 'POST',
    body: JSON.stringify({ expected_version: expectedVersion, new_password: newPassword }),
  })
}
