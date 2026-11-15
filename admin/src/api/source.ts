// SOURCE-001 admin API. Paths and field names follow
// server/api/admin/v1/admin.proto (itself aligned with the admin contract);
// requests use the same absolute /api/admin/v1 path in dev and deployment.

export interface Plan {
  id: number
  code: string
  name: string
  source_code: string
  channel: string
  page_start: number
  page_end: number
  request_budget: number
  item_budget: number
  fetch_details: boolean
  fetch_episodes: boolean
  enabled: boolean
  version: number
  rate_per_minute: number
  cron: string
  timezone: string
  next_run_at?: string
}

export interface PlanSet {
  items: Plan[]
}

export type RunStatus =
  'queued' | 'running' | 'waiting' | 'budget_exhausted' | 'succeeded' | 'failed' | 'cancelled'

export interface Run {
  id: number
  plan_id: number
  kind: string
  candidate_id?: number
  source_code: string
  channel: string
  page_start: number
  page_end: number
  status: RunStatus
  request_budget: number
  item_budget: number
  requests_used: number
  page_cursor: number
  pages_fetched: number
  items_seen: number
  items_upserted: number
  detail_fetched: number
  episodes_fetched: number
  cancel_requested: boolean
  error_code?: string
  error_reason?: string
  version: number
  rate_per_minute?: number
  started_at: string
  finished_at?: string
}

export interface RunSet {
  items: Run[]
  total: number
  page: number
  page_size: number
}

export interface Candidate {
  id: number
  source_code: string
  channel: string
  external_id: string
  title: string
  cover_url: string
  description: string
  tags: string[]
  episode_count: number
  open_episode_count: number
  rank: number
  metrics: Record<string, string>
  content_hash: string
  completeness: 'summary' | 'detail' | 'complete'
  detail_fetched_at?: string
  episodes_fetched_at?: string
  series_id?: number
  first_seen_at: string
  last_seen_at: string
}

export interface CandidateSet {
  items: Candidate[]
  total: number
  page: number
  page_size: number
}

export interface SourceEpisode {
  external_id: string
  number: number
  title?: string
}

export interface Revision {
  id: number
  series_id: number
  revision_no: number
  state: string
  version: number
  title: string
  description: string
  cover_url: string
  tags: string[]
  selected_count?: number
}

export interface RevisionEpisode {
  external_id: string
  number: number
  selected?: boolean
}

export interface SeriesDetail {
  id: number
  source_code: string
  external_id: string
  candidate_id?: number
  status: string
  version: number
  current_draft_revision_id: number
  draft: Revision
  episodes: RevisionEpisode[]
}

import { request } from './client'

export { ApiError } from './client'

export interface SourceEntry {
  source_code: string
  kind: string
  channel: string
  label: string
  // adapter_key names the registered metadata adapter that serves the entry.
  adapter_key: string
  description: string
}

export interface SourceEntrySet {
  items: SourceEntry[]
}

// listSourceEntries reads the entry points the source adapter can execute.
export function listSourceEntries(): Promise<SourceEntrySet> {
  return request<SourceEntrySet>('/api/admin/v1/ingestion/entries')
}

export interface RunItem {
  id: number
  run_id: number
  candidate_id: number
  candidate_external_id: string
  stage: string
  result: string
  error_code: string
  error_message: string
  attempt: number
  created_at: string
}

export interface RunItemSet {
  total: number
  page: number
  page_size: number
  items: RunItem[]
}

// listRunItems reads the stored rows of one run with their results.
export function listRunItems(runId: number, page = 1, pageSize = 20): Promise<RunItemSet> {
  return request<RunItemSet>(
    `/api/admin/v1/ingestion/runs/${runId}/items?page=${page}&page_size=${pageSize}`,
  )
}

// listRunCandidates reads the rows one collection run produced.
export function listRunCandidates(runId: number, page = 1, pageSize = 20): Promise<CandidateSet> {
  return request<CandidateSet>(
    `/api/admin/v1/ingestion/runs/${runId}/candidates?page=${page}&page_size=${pageSize}`,
  )
}

export function listPlans(): Promise<PlanSet> {
  return request<PlanSet>('/api/admin/v1/ingestion/plans')
}

export function triggerPlan(planId: number, expectedVersion: number): Promise<Run> {
  return request<Run>(`/api/admin/v1/ingestion/plans/${planId}/runs`, {
    method: 'POST',
    body: JSON.stringify({ expected_version: expectedVersion }),
  })
}

export interface PlanEdit {
  name: string
  page_start: number
  page_end: number
  request_budget: number
  item_budget: number
  rate_per_minute: number
  fetch_details: boolean
  fetch_episodes: boolean
  enabled: boolean
  cron: string
  timezone: string
}

export interface PlanNew extends PlanEdit {
  code: string
  source_code: string
  channel: string
}

export function createPlan(plan: PlanNew): Promise<Plan> {
  return request<Plan>('/api/admin/v1/ingestion/plans', {
    method: 'POST',
    body: JSON.stringify(plan),
  })
}

export function updatePlan(planId: number, expectedVersion: number, edit: PlanEdit): Promise<Plan> {
  return request<Plan>(`/api/admin/v1/ingestion/plans/${planId}`, {
    method: 'PATCH',
    body: JSON.stringify({ expected_version: expectedVersion, ...edit }),
  })
}

export function retryRun(
  runId: number,
  expectedVersion: number,
  additionalRequests: number,
  additionalItems: number,
  reason: string,
): Promise<Run> {
  return request<Run>(`/api/admin/v1/ingestion/runs/${runId}/retry`, {
    method: 'POST',
    body: JSON.stringify({
      expected_version: expectedVersion,
      additional_requests: additionalRequests,
      additional_items: additionalItems,
      reason,
    }),
  })
}

export function listRuns(page = 1, pageSize = 10): Promise<RunSet> {
  return request<RunSet>(`/api/admin/v1/ingestion/runs?page=${page}&page_size=${pageSize}`)
}

export function getRun(id: number): Promise<Run> {
  return request<Run>(`/api/admin/v1/ingestion/runs/${id}`)
}

export function cancelRun(id: number, expectedVersion: number): Promise<Run> {
  return request<Run>(`/api/admin/v1/ingestion/runs/${id}/cancel`, {
    method: 'POST',
    body: JSON.stringify({ expected_version: expectedVersion }),
  })
}

export function listCandidates(
  page: number,
  pageSize: number,
  keyword: string,
  completeness: string,
  imported?: boolean,
): Promise<CandidateSet> {
  const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) })
  if (keyword.trim()) params.set('q', keyword.trim())
  if (completeness) params.set('completeness', completeness)
  if (imported !== undefined) params.set('imported', String(imported))
  return request<CandidateSet>(`/api/admin/v1/candidates?${params.toString()}`)
}

export function getCandidate(
  id: number,
): Promise<{ candidate: Candidate; episodes: SourceEpisode[] }> {
  return request<{ candidate: Candidate; episodes: SourceEpisode[] }>(
    `/api/admin/v1/candidates/${id}`,
  )
}

export function createSeries(candidateId: number, expectedHash = ''): Promise<SeriesDetail> {
  return request<SeriesDetail>('/api/admin/v1/series', {
    method: 'POST',
    body: JSON.stringify({
      mode: 'source',
      candidate_id: candidateId,
      expected_candidate_hash: expectedHash,
    }),
  })
}

export function getSeries(seriesId: number): Promise<SeriesDetail> {
  return request<SeriesDetail>(`/api/admin/v1/series/${seriesId}`)
}

export function setSelection(
  revisionId: number,
  expectedVersion: number,
  mode: 'explicit' | 'all',
  episodeIds: string[],
): Promise<{ revision: Revision; selected_count: number }> {
  return request<{ revision: Revision; selected_count: number }>(
    `/api/admin/v1/revisions/${revisionId}/selection`,
    {
      method: 'PUT',
      body: JSON.stringify({
        expected_version: expectedVersion,
        selection_mode: mode,
        episode_ids: mode === 'explicit' ? episodeIds : [],
      }),
    },
  )
}

// Source and channel configuration. The server declares which adapters and
// entries exist, so the console offers nothing it cannot run.

export interface SourceConfig {
  id: number
  code: string
  name: string
  status: 'enabled' | 'paused'
  version: number
  updated_at: string
}

export interface SourceSet {
  items: SourceConfig[]
}

export interface ChannelConfig {
  id: number
  source_id: number
  source_code: string
  source_name: string
  source_status: string
  code: string
  name: string
  adapter_key: string
  entry_kind: string
  entry_code: string
  base_url: string
  allowed_hosts: string
  rate_per_minute: number
  timeout_seconds: number
  // has_secret reports whether a private reference is configured; the
  // reference itself is never returned.
  has_secret: boolean
  status: 'enabled' | 'paused'
  paused: boolean
  paused_cause: string
  version: number
  updated_at: string
}

export interface ChannelSet {
  items: ChannelConfig[]
}

export interface ChannelProbe {
  ok: boolean
  channel: string
  latency_ms: number
  items: number
  total_pages: number
  error_code: string
  error_message: string
  probed_at: string
}

// listSources reads the configured sources.
export function listSources(): Promise<SourceSet> {
  return request<SourceSet>('/api/admin/v1/sources')
}

// updateSource renames or pauses one source with a version check.
export function updateSource(input: {
  id: number
  expectedVersion: number
  name: string
  status?: string
}): Promise<SourceConfig> {
  return request<SourceConfig>(`/api/admin/v1/sources/${input.id}`, {
    method: 'PATCH',
    body: JSON.stringify({
      expected_version: input.expectedVersion,
      name: input.name,
      status: input.status ?? '',
    }),
  })
}

// listChannels reads the channels, optionally of one source.
export function listChannels(sourceId?: number): Promise<ChannelSet> {
  const params = new URLSearchParams()
  if (sourceId) params.set('source_id', String(sourceId))
  const query = params.toString()
  return request<ChannelSet>(`/api/admin/v1/source-channels${query ? `?${query}` : ''}`)
}

// updateChannel edits the configuration with a version check.
export function updateChannel(input: {
  id: number
  expectedVersion: number
  name: string
  entryKind: string
  entryCode: string
  baseUrl: string
  allowedHosts: string
  ratePerMinute: number
  timeoutSeconds: number
  secretRef?: string
}): Promise<ChannelConfig> {
  return request<ChannelConfig>(`/api/admin/v1/source-channels/${input.id}`, {
    method: 'PATCH',
    body: JSON.stringify({
      expected_version: input.expectedVersion,
      name: input.name,
      entry_kind: input.entryKind,
      entry_code: input.entryCode,
      base_url: input.baseUrl,
      allowed_hosts: input.allowedHosts,
      rate_per_minute: input.ratePerMinute,
      timeout_seconds: input.timeoutSeconds,
      secret_ref: input.secretRef ?? '',
    }),
  })
}

// setChannelStatus enables or pauses one channel with a version check.
export function setChannelStatus(input: {
  id: number
  expectedVersion: number
  status: 'enabled' | 'paused'
}): Promise<ChannelConfig> {
  return request<ChannelConfig>(`/api/admin/v1/source-channels/${input.id}/status`, {
    method: 'POST',
    body: JSON.stringify({ expected_version: input.expectedVersion, status: input.status }),
  })
}

// testChannel runs one explicit connection test. Saving never calls out.
export function testChannel(id: number): Promise<ChannelProbe> {
  return request<ChannelProbe>(`/api/admin/v1/source-channels/${id}/probe`, {
    method: 'POST',
    body: JSON.stringify({}),
  })
}
