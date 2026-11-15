import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  ApiError,
  cancelMediaBatch,
  getMediaBatch,
  listMediaBatches,
  listMediaItems,
  retryMediaBatch,
  submitMediaBatch,
} from '@/api/media'

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

describe('media batch api', () => {
  beforeEach(() => {
    fetchMock.mockReset()
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('lists the batches with the documented filters', async () => {
    fetchMock.mockResolvedValue(reply({ items: [{ id: 1 }], total: 1, page: 2, page_size: 10 }))
    const page = await listMediaBatches(2, 10, 3, 7, 'running')
    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [path] = lastCall()
    expect(path).toContain('/api/admin/v1/media-batches?')
    expect(path).toContain('page=2')
    expect(path).toContain('page_size=10')
    expect(path).toContain('series_id=3')
    expect(path).toContain('revision_id=7')
    expect(path).toContain('status=running')
    expect(page.total).toBe(1)
  })

  it('submits a batch on the revision path and accepts 202', async () => {
    fetchMock.mockResolvedValue(reply({ id: 9, revision_id: 7 }, 202))
    const batch = await submitMediaBatch(7, 4, 'web-mp4-h264-aac-v1', 'sdk', 12, 3)
    const [path, init] = lastCall()
    expect(path).toBe('/api/admin/v1/revisions/7/media-batches')
    expect(init.method).toBe('POST')
    expect(JSON.parse(String(init.body))).toEqual({
      expected_version: 4,
      processing_profile_key: 'web-mp4-h264-aac-v1',
      input_kind: 'sdk',
      authorization_grant_id: 12,
      expected_authorization_version: 3,
    })
    expect(batch.id).toBe(9)
  })

  it('reads one batch and its items', async () => {
    fetchMock.mockResolvedValue(reply({ batch: { id: 9 }, items: [], item_total: 0 }))
    const detail = await getMediaBatch(9)
    expect(lastCall()[0]).toBe('/api/admin/v1/media-batches/9')
    expect(detail.batch.id).toBe(9)

    fetchMock.mockResolvedValue(reply({ items: [], total: 0, page: 1, page_size: 20 }))
    await listMediaItems(9, 1, 20, 'failed')
    expect(lastCall()[0]).toContain('/api/admin/v1/media-batches/9/items?')
    expect(lastCall()[0]).toContain('status=failed')
  })

  it('retries and cancels with the expected version', async () => {
    fetchMock.mockResolvedValue(reply({ id: 9 }, 202))
    await retryMediaBatch(9, 3, [1, 2])
    const [retryPath, retryInit] = lastCall()
    expect(retryPath).toBe('/api/admin/v1/media-batches/9/retry')
    expect(JSON.parse(String(retryInit.body))).toEqual({ expected_version: 3, item_ids: [1, 2] })

    await cancelMediaBatch(9, 3, '运营取消')
    const [cancelPath, cancelInit] = lastCall()
    expect(cancelPath).toBe('/api/admin/v1/media-batches/9/cancel')
    expect(JSON.parse(String(cancelInit.body))).toEqual({ expected_version: 3, reason: '运营取消' })
  })

  it('reports the documented error reasons', async () => {
    fetchMock.mockResolvedValue(
      reply({ code: 404, reason: 'BATCH_NOT_FOUND', message: '素材批次不存在' }, 404),
    )
    await expect(getMediaBatch(404)).rejects.toBeInstanceOf(ApiError)
    await expect(getMediaBatch(404)).rejects.toMatchObject({ reason: 'BATCH_NOT_FOUND' })
  })
})
