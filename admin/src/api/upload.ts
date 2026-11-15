// MEDIA-002 M3-B manual upload API. Paths and field names follow
// server/api/admin/v1/admin.proto; the staging PUT is signed by the server
// and never carries a permanent secret.

import { ApiError, request } from './client'

export { ApiError } from './client'

export type UploadKind = 'episode_input' | 'cover'

export type UploadStatus = 'pending' | 'completed' | 'cancelled'

export interface UploadAuthorization {
  url: string
  method: string
  headers: Record<string, string>
  expires_at: string
}

export interface UploadSession {
  id: number
  revision_id: number
  episode_id: number
  kind: UploadKind
  status: UploadStatus
  version: number
  filename: string
  content_type: string
  size_bytes: number
  expected_sha256?: string
  sha256?: string
  width: number
  height: number
  asset_id?: number
  created_by: string
  revision_version: number
  expires_at: string
  completed_at?: string
  created_at: string
  updated_at: string
  authorization?: UploadAuthorization
}

export interface UploadSessionSet {
  items: UploadSession[]
  total: number
  page: number
  page_size: number
}

export interface RevisionEpisodeOption {
  id: number
  external_id: string
  number: number
  selected: boolean
}

export interface RevisionEpisodeSet {
  items: RevisionEpisodeOption[]
  total: number
}

export interface CreateUploadInput {
  revisionId: number
  expectedRevisionVersion: number
  episodeId?: number
  kind: UploadKind
  filename: string
  sizeBytes: number
  contentType?: string
  expectedSha256?: string
}

// listRevisionEpisodes reads the selectable episodes of one revision.
export function listRevisionEpisodes(revisionId: number): Promise<RevisionEpisodeSet> {
  const params = new URLSearchParams({ page: '1', page_size: '200' })
  return request<RevisionEpisodeSet>(
    `/api/admin/v1/revisions/${revisionId}/episodes?${params.toString()}`,
  )
}

// createUpload opens a staging session and returns its short-lived
// authorization. The response is a 201; the client only writes that one key.
export function createUpload(input: CreateUploadInput): Promise<UploadSession> {
  const body: Record<string, unknown> = {
    revision_id: input.revisionId,
    expected_revision_version: input.expectedRevisionVersion,
    kind: input.kind,
    filename: input.filename,
    size_bytes: input.sizeBytes,
  }
  if (input.kind === 'episode_input') body.episode_id = input.episodeId ?? 0
  if (input.contentType) body.content_type = input.contentType
  if (input.expectedSha256) body.expected_sha256 = input.expectedSha256
  return request<UploadSession>('/api/admin/v1/uploads', {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

// renewUpload signs the same staging key again, so a chunked upload keeps one
// object identity.
export function renewUpload(id: number, expectedVersion: number): Promise<UploadSession> {
  return request<UploadSession>(`/api/admin/v1/uploads/${id}/renew`, {
    method: 'POST',
    body: JSON.stringify({ expected_version: expectedVersion }),
  })
}

// completeUpload asks the server to confirm the stored object. A video
// becomes a trusted input, not ready material.
export function completeUpload(id: number, expectedVersion: number): Promise<UploadSession> {
  return request<UploadSession>(`/api/admin/v1/uploads/${id}/complete`, {
    method: 'POST',
    body: JSON.stringify({ expected_version: expectedVersion }),
  })
}

export function cancelUpload(id: number, expectedVersion: number): Promise<UploadSession> {
  return request<UploadSession>(`/api/admin/v1/uploads/${id}/cancel`, {
    method: 'POST',
    body: JSON.stringify({ expected_version: expectedVersion }),
  })
}

export function listUploads(
  revisionId: number,
  kind?: UploadKind,
  status?: UploadStatus,
): Promise<UploadSessionSet> {
  const params = new URLSearchParams({ revision_id: String(revisionId) })
  if (kind) params.set('kind', kind)
  if (status) params.set('status', status)
  return request<UploadSessionSet>(`/api/admin/v1/uploads?${params.toString()}`)
}

// uploadToStorage sends the bytes to the signed URL. XHR is used instead of
// fetch because the upload progress of a large file has to be visible.
export function uploadToStorage(
  authorization: UploadAuthorization,
  file: File,
  onProgress?: (percent: number) => void,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const request = new XMLHttpRequest()
    request.open(authorization.method || 'PUT', authorization.url, true)
    for (const [name, value] of Object.entries(authorization.headers ?? {})) {
      if (name.toLowerCase() === 'content-type') continue
      request.setRequestHeader(name, value)
    }
    // The real file type is what the server records; a declared filename is
    // never evidence of the container.
    request.setRequestHeader('Content-Type', file.type || 'application/octet-stream')
    request.upload.onprogress = (event) => {
      if (!onProgress || !event.lengthComputable) return
      onProgress(Math.round((event.loaded / event.total) * 100))
    }
    request.onload = () => {
      if (request.status >= 200 && request.status < 300) {
        onProgress?.(100)
        resolve()
        return
      }
      reject(
        new ApiError(
          { code: request.status, reason: 'UPLOAD_FAILED' },
          `上传失败（HTTP ${request.status}）`,
        ),
      )
    }
    request.onerror = () => reject(new ApiError({ reason: 'UPLOAD_NETWORK' }, '上传连接中断'))
    request.ontimeout = () => reject(new ApiError({ reason: 'UPLOAD_TIMEOUT' }, '上传超时'))
    request.send(file)
  })
}
