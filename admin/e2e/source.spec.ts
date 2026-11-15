import { test, expect, type Route } from '@playwright/test'
import { mockSession, sessionBody } from './session'

const plan = {
  id: 1,
  code: 'hongguo-hot-drama',
  name: '红果热播榜',
  source_code: 'hongguo',
  channel: 'hot-drama',
  page_start: 1,
  page_end: 1,
  request_budget: 80,
  item_budget: 200,
  fetch_details: true,
  fetch_episodes: true,
  enabled: true,
  version: 1,
}

const candidate = {
  id: 21,
  source_code: 'hongguo',
  channel: 'hot-drama',
  external_id: '7703173252444589080',
  title: '悍妻当家，靠美食扶起一家软骨头',
  cover_url: '',
  description: '测试简介',
  tags: ['AI剧', '种田'],
  episode_count: 3,
  open_episode_count: 1,
  rank: 21,
  metrics: { heat: '5773万热度' },
  content_hash: 'hash-21',
  completeness: 'complete',
  series_id: 0,
  first_seen_at: '2026-11-14T08:55:12Z',
  last_seen_at: '2026-11-14T08:55:12Z',
}

const episodes = [
  { external_id: 'ep-1', number: 1, selected: false },
  { external_id: 'ep-2', number: 2, selected: false },
  { external_id: 'ep-3', number: 3, selected: false },
]

let runPolls = 0

const run = (status: string) => ({
  id: 9,
  plan_id: 1,
  kind: 'ranking',
  source_code: 'hongguo',
  channel: 'hot-drama',
  page_start: 1,
  page_end: 1,
  status,
  request_budget: 80,
  item_budget: 200,
  requests_used: status === 'succeeded' ? 2 : 0,
  page_cursor: status === 'succeeded' ? 1 : 0,
  pages_fetched: status === 'succeeded' ? 1 : 0,
  items_seen: status === 'succeeded' ? 1 : 0,
  items_upserted: status === 'succeeded' ? 1 : 0,
  detail_fetched: status === 'succeeded' ? 1 : 0,
  episodes_fetched: status === 'succeeded' ? 1 : 0,
  cancel_requested: false,
  version: 2,
  started_at: '2026-11-14T08:55:12Z',
})

const seriesPayload = (selected: string[]) => ({
  id: 1,
  source_code: 'hongguo',
  external_id: candidate.external_id,
  candidate_id: candidate.id,
  status: 'draft',
  version: 1,
  current_draft_revision_id: 10,
  draft: {
    id: 10,
    series_id: 1,
    revision_no: 1,
    state: 'draft',
    version: selected.length,
    title: candidate.title,
    description: candidate.description,
    cover_url: '',
    tags: candidate.tags,
    selected_count: selected.length,
  },
  episodes: episodes.map((episode) => ({
    ...episode,
    selected: selected.includes(episode.external_id),
  })),
})

test.beforeEach(async ({ page }) => {
  await mockSession(page)
})

test('submits a persistent run, imports a snapshot and saves selection', async ({ page }) => {
  const browserErrors: string[] = []
  page.on('pageerror', (error) => browserErrors.push(error.message))
  let selected: string[] = []
  let savedIds: string[] = []
  runPolls = 0

  const fulfill = (route: Route, body: unknown) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })

  await page.route('**/api/admin/v1/**', async (route) => {
    // The broad route is registered after the session mock, so it has to
    // answer the session call itself.
    if (route.request().url().includes('/auth/me')) {
      await route.fulfill({ status: 200, json: sessionBody('admin') })
      return
    }
    // The broad route is registered after the session mock, so it has to
    // answer the session call itself.
    if (route.request().url().includes('/auth/me')) {
      await route.fulfill({ status: 200, json: sessionBody('admin') })
      return
    }
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === '/api/admin/v1/ingestion/plans') {
      return fulfill(route, { items: [plan] })
    }
    if (path === '/api/admin/v1/ingestion/plans/1/runs') {
      runPolls = 0
      return fulfill(route, run('queued'))
    }
    if (path === '/api/admin/v1/ingestion/runs') {
      runPolls += 1
      return fulfill(route, {
        items: [run(runPolls > 1 ? 'succeeded' : 'running')],
        total: 1,
        page: 1,
        page_size: 8,
      })
    }
    if (path === '/api/admin/v1/candidates') {
      return fulfill(route, {
        items: [{ ...candidate, series_id: selected.length > 0 ? 1 : 0 }],
        total: 1,
        page: 1,
        page_size: 10,
      })
    }
    if (path === '/api/admin/v1/series' && request.method() === 'POST') {
      return fulfill(route, seriesPayload(selected))
    }
    if (path === '/api/admin/v1/series/1') {
      return fulfill(route, seriesPayload(selected))
    }
    if (path === '/api/admin/v1/revisions/10/selection') {
      savedIds = JSON.parse(request.postData() ?? '{}').episode_ids ?? []
      selected = savedIds
      return fulfill(route, {
        revision: seriesPayload(selected).draft,
        selected_count: savedIds.length,
      })
    }
    return route.fulfill({ status: 404, contentType: 'application/json', body: '{}' })
  })

  await page.goto('/source')
  await expect(page.getByRole('heading', { name: '来源采集与本地选剧' })).toBeVisible()
  await expect(page.getByText(candidate.title)).toBeVisible()

  await page.getByRole('button', { name: '提交采集' }).click()
  await expect(page.getByText('已提交运行 #9')).toBeVisible()
  await expect(page.getByText('运行 #9', { exact: true })).toBeVisible({ timeout: 8000 })

  // Creating the content is an explicit action; the read-only view never
  // creates it.
  await page.getByRole('button', { name: '创建内容' }).click()
  await expect(page.getByText('分集勾选')).toBeVisible()
  await page.locator('.episode', { hasText: '第 2 集' }).click()
  await page.locator('.episode', { hasText: '第 3 集' }).click()
  await page.getByRole('button', { name: /保存勾选（2）/ }).click()
  await expect(page.getByText('已保存 2 集')).toBeVisible()
  expect(savedIds.sort()).toEqual(['ep-2', 'ep-3'])
  expect(browserErrors).toEqual([])
})

test('resumes a budget-exhausted run with extra requests', async ({ page }) => {
  let retryBody: Record<string, unknown> = {}
  const budgetRun = {
    ...run('budget_exhausted'),
    id: 10,
    version: 5,
    request_budget: 20,
    requests_used: 20,
    error_code: 'SOURCE_BUDGET_EXCEEDED',
    error_reason: 'request budget exhausted',
  }
  const fulfill = (route: Route, body: unknown) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })

  await page.route('**/api/admin/v1/**', async (route) => {
    // The broad route is registered after the session mock, so it has to
    // answer the session call itself.
    if (route.request().url().includes('/auth/me')) {
      await route.fulfill({ status: 200, json: sessionBody('admin') })
      return
    }
    // The broad route is registered after the session mock, so it has to
    // answer the session call itself.
    if (route.request().url().includes('/auth/me')) {
      await route.fulfill({ status: 200, json: sessionBody('admin') })
      return
    }
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === '/api/admin/v1/ingestion/plans') return fulfill(route, { items: [plan] })
    if (path === '/api/admin/v1/ingestion/runs') {
      return fulfill(route, { items: [budgetRun], total: 1, page: 1, page_size: 8 })
    }
    if (path === '/api/admin/v1/candidates') {
      return fulfill(route, { items: [], total: 0, page: 1, page_size: 10 })
    }
    if (path === '/api/admin/v1/ingestion/runs/10/retry') {
      retryBody = JSON.parse(request.postData() ?? '{}')
      return fulfill(route, { ...budgetRun, status: 'queued', request_budget: 50 })
    }
    return route.fulfill({ status: 404, contentType: 'application/json', body: '{}' })
  })

  await page.goto('/source')
  await expect(page.getByText('总预算耗尽')).toBeVisible()
  const inputs = page.locator('.retry-row input')
  await inputs.nth(0).fill('30')
  await page.locator('.retry-reason input').fill('运营追加额度')
  await page.getByRole('button', { name: '追加预算并重试' }).click()
  await expect(page.getByText('已恢复运行 #10，已消耗预算保留')).toBeVisible()
  expect(retryBody.additional_requests).toBe(30)
  expect(retryBody.reason).toBe('运营追加额度')
  expect(retryBody.expected_version).toBe(5)
})

test('saves a cron schedule on the plan', async ({ page }) => {
  let patchBody: Record<string, unknown> = {}
  const scheduled = {
    ...plan,
    cron: '0 3 * * *',
    timezone: 'Asia/Shanghai',
    next_run_at: '2026-11-15T03:00:00Z',
    version: 2,
  }
  const fulfill = (route: Route, body: unknown) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })

  await page.route('**/api/admin/v1/**', async (route) => {
    // The broad route is registered after the session mock, so it has to
    // answer the session call itself.
    if (route.request().url().includes('/auth/me')) {
      await route.fulfill({ status: 200, json: sessionBody('admin') })
      return
    }
    // The broad route is registered after the session mock, so it has to
    // answer the session call itself.
    if (route.request().url().includes('/auth/me')) {
      await route.fulfill({ status: 200, json: sessionBody('admin') })
      return
    }
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === '/api/admin/v1/ingestion/plans') return fulfill(route, { items: [plan] })
    if (path === '/api/admin/v1/ingestion/plans/1' && request.method() === 'PATCH') {
      patchBody = JSON.parse(request.postData() ?? '{}')
      return fulfill(route, scheduled)
    }
    if (path === '/api/admin/v1/ingestion/runs') {
      return fulfill(route, { items: [], total: 0, page: 1, page_size: 8 })
    }
    if (path === '/api/admin/v1/candidates') {
      return fulfill(route, { items: [], total: 0, page: 1, page_size: 10 })
    }
    return route.fulfill({ status: 404, contentType: 'application/json', body: '{}' })
  })

  await page.goto('/source')
  await expect(page.getByText('未设置定时：该计划仅手动触发')).toBeVisible()
  await page.getByRole('button', { name: '编辑计划' }).click()
  await page.getByPlaceholder('如 0 3 * * *（留空则仅手动触发）').fill('0 3 * * *')
  await page.getByPlaceholder('Asia/Shanghai').fill('Asia/Shanghai')
  await page.locator('.el-dialog').getByRole('button', { name: '保存', exact: true }).click()
  await expect(page.getByText('计划已更新')).toBeVisible()
  expect(patchBody.cron).toBe('0 3 * * *')
  expect(patchBody.timezone).toBe('Asia/Shanghai')
  expect(patchBody.expected_version).toBe(1)
  await expect(page.getByText(/定时 0 3 \* \* \*（Asia\/Shanghai）/)).toBeVisible()
})
