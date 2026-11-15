import { test, expect, type Page } from '@playwright/test'
import { mockSession } from './session'

// A published work: the console has to be able to take it down, and the
// request must carry the version the operator saw plus the stated reason.
const seriesItem = {
  series_id: '3',
  title: '在架剧集',
  description: '简介',
  revision_id: '9',
  revision_no: 2,
  revision_state: 'approved',
  status: 'published',
  version: 6,
  updated_at: '2026-11-15T04:00:00Z',
  episode_count: 2,
  published: true,
}

const detail = {
  id: '3',
  title: '在架剧集',
  description: '简介',
  status: 'published',
  version: 6,
  current_draft_revision_id: '9',
  published_revision: {
    id: '9',
    revision_no: 2,
    state: 'approved',
    version: 2,
    title: '在架剧集',
    description: '简介',
  },
  published_revision_id: '9',
  episodes: [],
}

async function mockConsole(
  page: Page,
  onUnpublish: (body: unknown) => { status: number; json: unknown },
) {
  await mockSession(page, 'admin')
  await page.route('**/api/admin/v1/series?*', (route) =>
    route.fulfill({ json: { total: 1, page: 1, page_size: 20, items: [seriesItem] } }),
  )
  await page.route('**/api/admin/v1/series/3/publication', (route) =>
    route.fulfill({
      json: {
        id: 1,
        series_id: '3',
        revision_id: '9',
        status: 'published',
        published_by: 'admin',
        published_at: '2026-11-15T04:00:00Z',
        version: 3,
      },
    }),
  )
  await page.route('**/api/admin/v1/series/3/publications/unpublish', async (route) => {
    const body = route.request().postDataJSON()
    const answer = onUnpublish(body)
    await route.fulfill({ status: answer.status, json: answer.json })
  })
  await page.route('**/api/admin/v1/series/3', (route) => route.fulfill({ json: detail }))
  await page.goto('/content')
  await page.getByRole('cell', { name: '在架剧集' }).click()
  await expect(page.getByRole('button', { name: '下架' })).toBeVisible()
}

test('takes a published work down with a stated reason and the version seen', async ({ page }) => {
  const sent: unknown[] = []
  await mockConsole(page, (body) => {
    sent.push(body)
    return { status: 200, json: { id: 1, series_id: '3', status: 'unpublished', version: 4 } }
  })

  await page.getByRole('button', { name: '下架' }).click()
  const dialog = page.getByRole('dialog', { name: '下架作品' })
  await expect(dialog).toBeVisible()
  // The work being taken down is named, not just an identifier.
  await expect(dialog).toContainText('在架剧集')

  // A reason is required: an empty form does not send anything.
  await dialog.getByRole('button', { name: '确认下架' }).click()
  await expect(dialog.getByText('请填写下架原因')).toBeVisible()
  expect(sent).toHaveLength(0)

  await dialog.getByPlaceholder('例如：授权到期、内容有误需要重新处理').fill('授权到期')
  await dialog.getByRole('button', { name: '确认下架' }).click()
  await expect(page.getByText('已下架：公开页面不再展示，新的播放会话会被拒绝')).toBeVisible()
  expect(sent).toEqual([{ expected_version: 3, reason: '授权到期' }])
})

test('keeps the reason and refreshes the version when the work moved on', async ({ page }) => {
  const sent: unknown[] = []
  let version = 3
  await mockConsole(page, (body) => {
    sent.push(body)
    // The first attempt meets a newer publication; the console refreshes the
    // version and asks again instead of retrying on its own.
    if (sent.length === 1) {
      version = 5
      return {
        status: 409,
        json: { code: 409, reason: 'VERSION_CONFLICT', message: '版本已变化' },
      }
    }
    return {
      status: 200,
      json: { id: 1, series_id: '3', status: 'unpublished', version: version + 1 },
    }
  })

  await page.getByRole('button', { name: '下架' }).click()
  const dialog = page.getByRole('dialog', { name: '下架作品' })
  await dialog.getByPlaceholder('例如：授权到期、内容有误需要重新处理').fill('内容有误')
  await dialog.getByRole('button', { name: '确认下架' }).click()

  await expect(dialog.getByText('版本已变化', { exact: false })).toBeVisible()
  // The typed reason survives the conflict.
  await expect(dialog.getByPlaceholder('例如：授权到期、内容有误需要重新处理')).toHaveValue(
    '内容有误',
  )
  expect(sent).toHaveLength(1)
})
