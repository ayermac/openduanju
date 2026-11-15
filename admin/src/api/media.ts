// MEDIA-002 admin API. Paths and field names follow
// server/api/admin/v1/admin.proto (itself aligned with the admin contract);
// requests use the same absolute /api/admin/v1 path in dev and deployment.

import { request } from './client'

export { ApiError } from './client'

export type MediaBatchStatus =
  | 'queued'
  | 'running'
  | 'waiting'
  | 'partially_failed'
  | 'failed'
  | 'succeeded'
  | 'cancelling'
  | 'cancelled'
  | 'pending_review'

export type MediaItemStatus =
  | 'queued'
  | 'resolving'
  | 'acquiring'
  | 'validating'
  | 'processing'
  | 'uploading'
  | 'verifying'
  | 'ready'
  | 'waiting_retry'
  | 'failed'
  | 'cancelled'

export interface MediaBatch {
  id: number
  series_id: number
  revision_id: number
  selection_hash: string
  profile_version: string
  input_kind: string
  status: MediaBatchStatus
  cover_status: string
  item_total: number
  item_ready: number
  item_failed: number
  cancel_requested: boolean
  version: number
  error_code?: string
  error_message?: string
  created_by: string
  review_task_id?: number
  created_at: string
  updated_at: string
}

export interface MediaBatchSet {
  items: MediaBatch[]
  total: number
  page: number
  page_size: number
}

export interface MediaItem {
  id: number
  batch_id: number
  episode_id: number
  external_id: string
  episode_no: number
  status: MediaItemStatus
  attempt: number
  retryable: boolean
  next_eligible_at?: string
  error_code?: string
  error_message?: string
  sha256?: string
  size_bytes: number
  codec?: string
  duration_ms: number
  width: number
  height: number
  asset_id?: number
  created_at: string
  updated_at: string
}

export interface MediaItemSet {
  items: MediaItem[]
  total: number
  page: number
  page_size: number
}

export interface MediaBatchDetail {
  batch: MediaBatch
  items: MediaItem[]
  item_total: number
}

export function listMediaBatches(
  page: number,
  pageSize: number,
  seriesId?: number,
  revisionId?: number,
  status?: string,
): Promise<MediaBatchSet> {
  const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) })
  if (seriesId) params.set('series_id', String(seriesId))
  if (revisionId) params.set('revision_id', String(revisionId))
  if (status) params.set('status', status)
  return request<MediaBatchSet>(`/api/admin/v1/media-batches?${params.toString()}`)
}

export function getMediaBatch(id: number): Promise<MediaBatchDetail> {
  return request<MediaBatchDetail>(`/api/admin/v1/media-batches/${id}`)
}

export function listMediaItems(
  id: number,
  page: number,
  pageSize: number,
  status?: string,
): Promise<MediaItemSet> {
  const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) })
  if (status) params.set('status', status)
  return request<MediaItemSet>(`/api/admin/v1/media-batches/${id}/items?${params.toString()}`)
}

// submitMediaBatch freezes the revision selection and queues the batch. The
// server answers 202: the processing runs in the worker, not in the request.
export function submitMediaBatch(
  revisionId: number,
  expectedVersion: number,
  processingProfileKey: string,
  inputKind: string,
  authorizationGrantId: number,
  expectedAuthorizationVersion: number,
): Promise<MediaBatch> {
  return request<MediaBatch>(`/api/admin/v1/revisions/${revisionId}/media-batches`, {
    method: 'POST',
    body: JSON.stringify({
      expected_version: expectedVersion,
      processing_profile_key: processingProfileKey,
      input_kind: inputKind,
      authorization_grant_id: authorizationGrantId,
      expected_authorization_version: expectedAuthorizationVersion,
    }),
  })
}

export function retryMediaBatch(
  id: number,
  expectedVersion: number,
  itemIds: number[] = [],
): Promise<MediaBatch> {
  return request<MediaBatch>(`/api/admin/v1/media-batches/${id}/retry`, {
    method: 'POST',
    body: JSON.stringify({ expected_version: expectedVersion, item_ids: itemIds }),
  })
}

export function cancelMediaBatch(
  id: number,
  expectedVersion: number,
  reason: string,
): Promise<MediaBatch> {
  return request<MediaBatch>(`/api/admin/v1/media-batches/${id}/cancel`, {
    method: 'POST',
    body: JSON.stringify({ expected_version: expectedVersion, reason }),
  })
}
