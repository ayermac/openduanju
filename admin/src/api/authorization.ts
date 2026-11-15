// Series-level authorization grants. A grant is append-only: a corrected
// permission or validity window is a new grant, and an existing one can only
// be revoked. Permission bits default to false.

import { request } from './client'

export { ApiError } from './client'

export interface AuthorizationGrant {
  id: number
  series_id: number
  reference: string
  allow_process: boolean
  allow_display: boolean
  allow_playback: boolean
  valid_from: string
  valid_until?: string
  status: 'active' | 'revoked'
  version: number
  confirmed_by: string
  confirmed_at: string
  revoked_by?: string
  revoked_at?: string
  revoke_reason?: string
  created_at: string
  updated_at: string
}

export interface AuthorizationGrantSet {
  items: AuthorizationGrant[]
  total: number
  page: number
  page_size: number
}

export interface CreateGrantInput {
  reference: string
  allowProcess: boolean
  allowDisplay: boolean
  allowPlayback: boolean
  validFrom: string
  validUntil?: string
}

// listGrants reads the grants of one series.
export function listGrants(
  seriesId?: number,
  status?: string,
  page = 1,
  pageSize = 20,
): Promise<AuthorizationGrantSet> {
  const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) })
  if (seriesId) params.set('series_id', String(seriesId))
  if (status) params.set('status', status)
  return request<AuthorizationGrantSet>(`/api/admin/v1/authorization-grants?${params.toString()}`)
}

// createGrant records one grant. The response is a 201.
export function createGrant(
  seriesId: number,
  input: CreateGrantInput,
): Promise<AuthorizationGrant> {
  const body: Record<string, unknown> = {
    reference: input.reference,
    allow_process: input.allowProcess,
    allow_display: input.allowDisplay,
    allow_playback: input.allowPlayback,
    valid_from: input.validFrom,
  }
  if (input.validUntil) body.valid_until = input.validUntil
  return request<AuthorizationGrant>(`/api/admin/v1/series/${seriesId}/authorization-grants`, {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

// revokeGrant applies a version CAS. A repeated revoke with the same version
// is idempotent; a different version is a conflict.
export function revokeGrant(
  id: number,
  expectedVersion: number,
  reason: string,
): Promise<AuthorizationGrant> {
  return request<AuthorizationGrant>(`/api/admin/v1/authorization-grants/${id}/revoke`, {
    method: 'POST',
    body: JSON.stringify({ expected_version: expectedVersion, reason }),
  })
}
