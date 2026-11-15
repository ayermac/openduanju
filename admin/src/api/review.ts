// Review worklist, decisions and publication. The server owns every rule;
// the UI shows the reason it returns.

import { request } from './client'

export { ApiError } from './client'

export interface ReviewCase {
  task_id: number
  revision_id: number
  series_id: number
  batch_id: number
  status: 'open' | 'claimed' | 'approved' | 'rejected'
  assignee?: string
  submitted_by?: string
  version: number
  created_at: string
  series_title?: string
  item_total: number
  item_ready: number
  cover_status: string
}

export interface ReviewAsset {
  asset_id: number
  content_type: string
  size_bytes: number
  duration_ms: number
  width: number
  height: number
  preview_url?: string
  preview_expires_at?: string
}

export interface ReviewEpisode {
  episode_id: number
  number: number
  external_id: string
  asset?: ReviewAsset
}

export interface ReviewDetail {
  case: ReviewCase
  episodes: ReviewEpisode[]
  cover?: ReviewAsset
  authorization?: {
    id: number
    reference: string
    allow_process: boolean
    allow_display: boolean
    allow_playback: boolean
    valid_from: string
    valid_until?: string
    status: string
    version: number
  }
  decision?: {
    decision: string
    reason?: string
    decided_by: string
    decided_at: string
    task_version: number
  }
}

export interface Publication {
  id: number
  series_id: number
  revision_id: number
  status: 'published' | 'unpublished'
  published_by: string
  published_at: string
  unpublished_by?: string
  unpublished_at?: string
  version: number
}

export interface ReviewCaseSet {
  items: ReviewCase[]
  total: number
  page: number
  page_size: number
}

export function listReviews(status?: string, page = 1, pageSize = 20): Promise<ReviewCaseSet> {
  const search = new URLSearchParams({ page: String(page), page_size: String(pageSize) })
  if (status) search.set('status', status)
  return request<ReviewCaseSet>(`/api/admin/v1/reviews?${search.toString()}`)
}

export function getReview(id: number): Promise<ReviewDetail> {
  return request<ReviewDetail>(`/api/admin/v1/reviews/${id}`)
}

export function claimReview(id: number, expectedVersion: number): Promise<ReviewCase> {
  return request<ReviewCase>(`/api/admin/v1/reviews/${id}/claim`, {
    method: 'POST',
    body: JSON.stringify({ expected_version: expectedVersion }),
  })
}

export interface ReviewChecklist {
  title_cover_intro: boolean
  episode_mapping: boolean
  audio_video: boolean
  authorization_public: boolean
  reviewed_episodes: number
}

export function decideReview(
  id: number,
  expectedVersion: number,
  decision: 'approved' | 'rejected',
  reason?: string,
  checklist?: ReviewChecklist,
): Promise<ReviewCase> {
  const body: Record<string, unknown> = { expected_version: expectedVersion, decision, reason }
  if (checklist) body.checklist = checklist
  return request<ReviewCase>(`/api/admin/v1/reviews/${id}/decision`, {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

export function publishRevision(seriesId: number, revisionId: number): Promise<Publication> {
  return request<Publication>(`/api/admin/v1/series/${seriesId}/publications`, {
    method: 'POST',
    body: JSON.stringify({ revision_id: revisionId }),
  })
}

export function unpublishSeries(
  seriesId: string | number,
  expectedVersion: number,
  reason: string,
): Promise<Publication> {
  return request<Publication>(`/api/admin/v1/series/${seriesId}/publications/unpublish`, {
    method: 'POST',
    // The reason is kept on the audit row, so a later reader knows why the
    // work left the shelf.
    body: JSON.stringify({ expected_version: expectedVersion, reason }),
  })
}

export function getPublication(seriesId: string | number): Promise<Publication> {
  return request<Publication>(`/api/admin/v1/series/${seriesId}/publication`)
}
