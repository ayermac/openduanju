import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { ApiError } from '@/api/client'
import { createManualSeries, createRevision } from '@/api/content'

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

describe('content revision api', () => {
  beforeEach(() => {
    fetchMock.mockReset()
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('creates a revision on the series path with the inherited base', async () => {
    fetchMock.mockResolvedValue(reply({ id: '3', draft: { id: '10' } }))
    const detail = await createRevision('3', { baseRevisionId: '9' })
    const [path, init] = lastCall()
    expect(path).toBe('/api/admin/v1/series/3/revisions')
    expect(init.method).toBe('POST')
    // 64-bit identifiers travel as strings.
    expect(JSON.parse(String(init.body))).toEqual({ base_revision_id: '9' })
    expect(detail.draft?.id).toBe('10')
  })

  it('sends no replacement unless the operator named a draft', async () => {
    fetchMock.mockResolvedValue(reply({ id: '3' }))
    await createRevision('3')
    expect(JSON.parse(String(lastCall()[1].body))).toEqual({})
  })

  it('names the replaced draft and the version the operator saw', async () => {
    fetchMock.mockResolvedValue(reply({ id: '3', draft: { id: '11' } }))
    await createRevision('3', {
      baseRevisionId: '9',
      replaceDraftRevisionId: '9',
      replaceDraftVersion: 4,
    })
    expect(JSON.parse(String(lastCall()[1].body))).toEqual({
      base_revision_id: '9',
      replace_draft_revision_id: '9',
      replace_draft_version: 4,
    })
  })

  it('keeps the draft conflict readable for the caller', async () => {
    fetchMock.mockResolvedValue(
      reply({ code: 409, reason: 'DRAFT_EXISTS', message: '该作品已有工作草稿' }, 409),
    )
    await expect(createRevision('3', { baseRevisionId: '9' })).rejects.toBeInstanceOf(ApiError)
    await expect(createRevision('3', { baseRevisionId: '9' })).rejects.toMatchObject({
      reason: 'DRAFT_EXISTS',
      code: 409,
    })
  })
})

describe('manual work api', () => {
  beforeEach(() => {
    fetchMock.mockReset()
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('declares a manual work without any provider identity', async () => {
    fetchMock.mockResolvedValue(reply({ id: '5', draft: { id: '6' } }))
    await createManualSeries({
      title: '人工短剧',
      description: '简介',
      episodeNumbers: [1, 2, 5],
      requestId: 'request-9',
    })
    const [path, init] = lastCall()
    expect(path).toBe('/api/admin/v1/series')
    expect(init.method).toBe('POST')
    expect(JSON.parse(String(init.body))).toEqual({
      mode: 'manual',
      title: '人工短剧',
      description: '简介',
      episodes: [{ original_number: 1 }, { original_number: 2 }, { original_number: 5 }],
      request_id: 'request-9',
    })
  })

  it('keeps the validation conflict readable for the form', async () => {
    fetchMock.mockResolvedValue(
      reply({ code: 409, reason: 'DUPLICATE_EPISODE_NUMBER', message: '分集序号重复' }, 409),
    )
    await expect(
      createManualSeries({ title: 't', description: '', episodeNumbers: [1], requestId: 'r' }),
    ).rejects.toMatchObject({ reason: 'DUPLICATE_EPISODE_NUMBER' })
  })
})
