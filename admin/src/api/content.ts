import { request } from './client'

// The content list and the draft edit. The identifiers follow the API
// contract.
export interface SeriesItem {
  // 64-bit identifiers stay strings.
  series_id: string
  title: string
  description: string
  revision_id: string
  revision_no: number
  revision_state: string
  status: string
  version: number
  updated_at: string
  episode_count: number
  published: boolean
}

export interface SeriesSet {
  total: number
  page: number
  page_size: number
  items: SeriesItem[]
}

// SeriesDetail is the series with its current revision. The contract carries
// no series-level title: the title of the list row comes from the series and
// the editable title from the revision.
export interface SeriesDetail {
  id: string
  status: string
  version: number
  current_draft_revision_id: string
  // published_revision is the revision on the shelf.
  published_revision?: RevisionDetail
  published_revision_id?: string
  // draft is the working draft of the series.
  draft?: RevisionDetail
  episodes?: RevisionEpisode[]
}

export interface RevisionDetail {
  id: string
  revision_no: number
  state: string
  version: number
  title: string
  description: string
  updated_at?: string
}

export interface RevisionEpisode {
  id: string
  external_id: string
  number: number
  selected: boolean
}

export function listSeries(params: {
  q?: string
  status?: string
  page?: number
  pageSize?: number
}): Promise<SeriesSet> {
  const search = new URLSearchParams({
    page: String(params.page ?? 1),
    page_size: String(params.pageSize ?? 20),
  })
  if (params.q?.trim()) search.set('q', params.q.trim())
  if (params.status) search.set('status', params.status)
  return request<SeriesSet>(`/api/admin/v1/series?${search.toString()}`)
}

export function getSeries(seriesId: string): Promise<SeriesDetail> {
  return request<SeriesDetail>(`/api/admin/v1/series/${seriesId}`)
}

// updateRevision edits the working draft. The version of the draft the form
// was filled with travels with the request.
export function updateRevision(
  seriesId: string,
  revisionId: string,
  expectedVersion: number,
  edit: { title?: string; description?: string },
): Promise<SeriesDetail> {
  return request<SeriesDetail>(`/api/admin/v1/series/${seriesId}/revisions/${revisionId}`, {
    method: 'PATCH',
    body: JSON.stringify({ expected_version: expectedVersion, ...edit }),
  })
}

// CreateRevisionInput names the revision the new draft inherits from. When the
// series still has an editable draft, that draft and the version the operator
// saw have to be named as well: the server never replaces a draft silently.
export interface CreateRevisionInput {
  baseRevisionId?: string
  replaceDraftRevisionId?: string
  replaceDraftVersion?: number
}

// ManualSeriesInput declares a work that has no provider: the operator writes
// the title, the description and the original episode numbers.
export interface ManualSeriesInput {
  title: string
  description: string
  episodeNumbers: number[]
  // requestId is the idempotency key of the form: a second submission of the
  // same form returns the work that already exists.
  requestId: string
}

// createManualSeries declares a local work. Nothing is downloaded or
// processed: the work is created with an editable draft.
export function createManualSeries(input: ManualSeriesInput): Promise<SeriesDetail> {
  return request<SeriesDetail>('/api/admin/v1/series', {
    method: 'POST',
    body: JSON.stringify({
      mode: 'manual',
      title: input.title,
      description: input.description,
      episodes: input.episodeNumbers.map((number) => ({ original_number: number })),
      request_id: input.requestId,
    }),
  })
}

// createRevision creates a new working draft. The server answers 409
// DRAFT_EXISTS while an editable draft exists.
export function createRevision(
  seriesId: string,
  input: CreateRevisionInput = {},
): Promise<SeriesDetail> {
  const body: Record<string, string | number> = {}
  if (input.baseRevisionId) body.base_revision_id = input.baseRevisionId
  if (input.replaceDraftRevisionId) {
    body.replace_draft_revision_id = input.replaceDraftRevisionId
    body.replace_draft_version = input.replaceDraftVersion ?? 0
  }
  return request<SeriesDetail>(`/api/admin/v1/series/${seriesId}/revisions`, {
    method: 'POST',
    body: JSON.stringify(body),
  })
}
