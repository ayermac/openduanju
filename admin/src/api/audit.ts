// REQ-OBS-001 admin API. Paths and field names follow
// server/api/admin/v1/admin.proto (GET /api/admin/v1/audit).
//
// The list holds what one write did: who did it, to which object, which
// version it produced, whether it succeeded, and the trace identity that also
// appears in the error report of the same request. It never holds a payload or
// a credential, so this page can stay open to every signed-in role.

import { request } from './client'

export { ApiError } from './client'

export interface AuditEvent {
  id: number
  trace_id: string
  actor_id: number
  actor_name: string
  action: string
  resource_type: string
  resource_id: string
  resource_version: number
  result: 'success' | 'failure'
  reason: string
  created_at: string
}

export interface AuditEventSet {
  items: AuditEvent[]
  total: number
  page: number
  page_size: number
}

export interface AuditQuery {
  page?: number
  pageSize?: number
  resourceType?: string
  resourceId?: string
  action?: string
  actorName?: string
  traceId?: string
}

// listAuditEvents reads one page. A failed read is reported as an error: an
// unreadable list is never shown as "no events".
export function listAuditEvents(query: AuditQuery = {}): Promise<AuditEventSet> {
  const params = new URLSearchParams()
  params.set('page', String(query.page ?? 1))
  params.set('page_size', String(query.pageSize ?? 20))
  if (query.resourceType) params.set('resource_type', query.resourceType)
  if (query.resourceId) params.set('resource_id', query.resourceId)
  if (query.action) params.set('action', query.action)
  if (query.actorName) params.set('actor_name', query.actorName)
  if (query.traceId) params.set('trace_id', query.traceId)
  return request<AuditEventSet>(`/api/admin/v1/audit?${params.toString()}`)
}

// Actions and resource types the console writes. They mirror the stable names
// the server stores; the list is a convenience for the filter, not the source
// of truth.
export const AUDIT_RESOURCE_TYPES: { value: string; label: string }[] = [
  { value: 'series', label: '作品' },
  { value: 'revision', label: '修订' },
  { value: 'media_batch', label: '素材批次' },
  { value: 'review', label: '审核' },
  { value: 'publication', label: '发布' },
  { value: 'source', label: '来源' },
  { value: 'source_channel', label: '通道' },
  { value: 'ingestion_plan', label: '采集计划' },
  { value: 'authorization_grant', label: '授权依据' },
  { value: 'staff', label: '账号' },
]

export const AUDIT_ACTIONS: { value: string; label: string }[] = [
  { value: 'content.series.create', label: '创建作品' },
  { value: 'content.revision.create', label: '新建修订' },
  { value: 'content.revision.update', label: '编辑草稿' },
  { value: 'content.revision.select', label: '选集' },
  { value: 'media.batch.submit', label: '提交素材批次' },
  { value: 'media.batch.retry', label: '重试素材批次' },
  { value: 'media.batch.cancel', label: '取消素材批次' },
  { value: 'review.claim', label: '认领审核' },
  { value: 'review.decide', label: '审核裁决' },
  { value: 'publication.publish', label: '发布' },
  { value: 'publication.unpublish', label: '下架' },
  { value: 'source.update', label: '修改来源' },
  { value: 'source_channel.update', label: '修改通道' },
  { value: 'source_channel.status', label: '启停通道' },
  { value: 'ingestion.plan.create', label: '新建采集计划' },
  { value: 'ingestion.plan.update', label: '修改采集计划' },
  { value: 'authorization.create', label: '新增授权依据' },
  { value: 'authorization.revoke', label: '撤销授权依据' },
  { value: 'staff.create', label: '创建账号' },
  { value: 'staff.update', label: '修改账号' },
  { value: 'staff.reset_password', label: '重置密码' },
]
