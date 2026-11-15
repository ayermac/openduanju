// Public API client. The web surface is read-only and needs no account.

export interface SeriesCard {
  // 64-bit identifiers stay strings; a browser must never reparse them.
  series_id: string
  title: string
  description: string
  cover_url: string
  episode_count: number
  category: string
  published_at: string
}

export interface SeriesSet {
  total: number
  page: number
  page_size: number
  items: SeriesCard[]
}

export interface EpisodeCard {
  episode_id: string
  number: number
  external_id: string
  asset_id: string
  duration_ms: number
  width: number
  height: number
}

export interface SeriesDetail {
  series: SeriesCard
  revision_id: string
  publication_id: string
  episodes: EpisodeCard[]
}

export interface PlaybackTicket {
  session_id: string
  token: string
  url: string
  expires_at: string
  episode_id: string
  revision_id: string
}

export class WebError extends Error {
  readonly code: number
  readonly reason: string

  constructor(code: number, reason: string, message: string) {
    super(message)
    this.code = code
    this.reason = reason
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: { 'Content-Type': 'application/json', ...init?.headers },
  })
  if (!response.ok) {
    const problem = (await response.json().catch(() => ({}))) as {
      code?: number
      reason?: string
      message?: string
    }
    throw new WebError(
      problem.code ?? response.status,
      problem.reason ?? 'UNKNOWN',
      problem.message ?? `请求失败（HTTP ${response.status}）`,
    )
  }
  if (response.status === 204) return undefined as T
  return (await response.json()) as T
}

export function fetchHome(limit = 12): Promise<SeriesSet> {
  return request<SeriesSet>(`/api/web/v1/home?limit=${limit}`)
}

export function fetchSeries(params: {
  q?: string
  category?: string
  page?: number
  pageSize?: number
}): Promise<SeriesSet> {
  const search = new URLSearchParams({
    page: String(params.page ?? 1),
    page_size: String(params.pageSize ?? 20),
  })
  if (params.q) search.set('q', params.q)
  if (params.category) search.set('category', params.category)
  return request<SeriesSet>(`/api/web/v1/series?${search.toString()}`)
}

export interface CategorySet {
  categories: string[]
}

// fetchCategories reads the categories that currently have displayable content.
// The server derives them from the published projection, so the home page does
// not have to walk every page of series to build its filters.
export function fetchCategories(): Promise<CategorySet> {
  return request<CategorySet>('/api/web/v1/categories')
}

export function fetchDetail(seriesId: string): Promise<SeriesDetail> {
  return request<SeriesDetail>(`/api/web/v1/series/${seriesId}`)
}

export function openPlayback(seriesId: string, episodeId: string): Promise<PlaybackTicket> {
  return request<PlaybackTicket>('/api/web/v1/playback-sessions', {
    method: 'POST',
    body: JSON.stringify({ series_id: seriesId, episode_id: episodeId }),
  })
}

export function refreshPlayback(token: string): Promise<PlaybackTicket> {
  return request<PlaybackTicket>('/api/web/v1/playback-sessions/refresh', {
    method: 'POST',
    body: JSON.stringify({ token }),
  })
}
