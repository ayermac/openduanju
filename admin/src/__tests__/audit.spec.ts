import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { ApiError } from '@/api/client'
import { listAuditEvents } from '@/api/audit'

const fetchMock = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<unknown>>()

function lastCall(): [string, RequestInit] {
  const call = fetchMock.mock.calls[fetchMock.mock.calls.length - 1]
  if (!call) throw new Error('the api did not call the server')
  return [call[0] as string, call[1] as RequestInit]
}

function reply(body: unknown, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  }
}

describe('audit api', () => {
  beforeEach(() => {
    fetchMock.mockReset()
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('reads one page with the filters the operator chose', async () => {
    fetchMock.mockResolvedValue(
      reply({
        items: [
          {
            id: '1',
            trace_id: 'trace-1',
            actor_id: '7',
            actor_name: 'operator-a',
            action: 'media.batch.submit',
            resource_type: 'media_batch',
            resource_id: '12',
            resource_version: 3,
            result: 'success',
            reason: '',
            created_at: '2026-11-15T07:00:00Z',
          },
        ],
        total: 1,
        page: 2,
        page_size: 20,
      }),
    )
    const set = await listAuditEvents({
      page: 2,
      resourceType: 'media_batch',
      action: 'media.batch.submit',
      resourceId: '12',
      actorName: 'operator-a',
      traceId: 'trace-1',
    })
    const [path, init] = lastCall()
    expect(path).toContain('/api/admin/v1/audit?')
    expect(path).toContain('page=2')
    expect(path).toContain('resource_type=media_batch')
    expect(path).toContain('action=media.batch.submit')
    expect(path).toContain('resource_id=12')
    expect(path).toContain('actor_name=operator-a')
    expect(path).toContain('trace_id=trace-1')
    expect(init.method).toBe('GET')
    const [row] = set.items
    expect(row?.result).toBe('success')
    // The 64-bit identifiers arrive as strings and stay strings.
    expect(row?.id).toBe('1')
    expect(row?.actor_id).toBe('7')
  })

  it('sends only the page when no filter is set', async () => {
    fetchMock.mockResolvedValue(reply({ items: [], total: 0, page: 1, page_size: 20 }))
    await listAuditEvents()
    const [path] = lastCall()
    expect(path).toBe('/api/admin/v1/audit?page=1&page_size=20')
  })

  it('reports an unreadable trail as an error, not an empty list', async () => {
    fetchMock.mockResolvedValue(
      reply(
        {
          code: 500,
          reason: 'AUDIT_UNAVAILABLE',
          message: '审计记录暂时无法读取',
          metadata: { trace_id: 'trace-read-1' },
        },
        500,
      ),
    )
    const error = await listAuditEvents().catch((caught: unknown) => caught)
    expect(error).toBeInstanceOf(ApiError)
    expect((error as ApiError).reason).toBe('AUDIT_UNAVAILABLE')
    // The report number is part of the message an operator reads.
    expect((error as ApiError).traceId).toBe('trace-read-1')
    expect((error as Error).message).toContain('报障编号 trace-read-1')
  })
})

describe('problem report number', () => {
  it('keeps the trace of a refusal so it can be quoted', () => {
    const error = new ApiError(
      {
        code: 403,
        reason: 'FORBIDDEN',
        message: '当前角色无权执行该操作',
        metadata: { trace_id: 'trace-403' },
      },
      '请求失败',
    )
    expect(error.traceId).toBe('trace-403')
    expect(error.message).toBe('当前角色无权执行该操作（报障编号 trace-403）')
  })

  it('does not invent a report number when the response carries none', () => {
    const error = new ApiError({ code: 400, reason: 'INVALID', message: '参数错误' }, '请求失败')
    expect(error.traceId).toBe('')
    expect(error.message).toBe('参数错误')
  })
})
