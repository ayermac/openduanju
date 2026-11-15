import { test, expect, type Page, type Route } from '@playwright/test'
import { mockSession } from './session'

// The list row and the detail of one series whose current revision was
// rejected: it keeps the material the reviewer judged and is not editable.
const seriesItem = {
  series_id: '3',
  title: '测试剧集',
  description: '简介',
  revision_id: '9',
  revision_no: 2,
  revision_state: 'rejected',
  status: 'draft',
  version: 6,
  updated_at: '2026-11-15T04:00:00Z',
  episode_count: 1,
  published: false,
}

function detailBody(overrides: Record<string, unknown> = {}) {
  return {
    id: '3',
    title: '测试剧集',
    description: '简介',
    status: 'draft',
    version: 6,
    current_draft_revision_id: '9',
    draft: {
      id: '9',
      revision_no: 2,
      state: 'rejected',
      version: 3,
      title: '测试剧集',
      description: '简介',
    },
    episodes: [],
    ...overrides,
  }
}

// routeContent mocks the list, the detail and the new-revision call.
async function routeContent(
  page: Page,
  onCreate: (route: Route) => Promise<void> | void,
  detail = detailBody(),
) {
  await page.route('**/api/admin/v1/series?**', (route) =>
    route.fulfill({ status: 200, json: { total: 1, page: 1, page_size: 20, items: [seriesItem] } }),
  )
  await page.route('**/api/admin/v1/series/3', (route) =>
    route.fulfill({ status: 200, json: detail }),
  )
  await page.route('**/api/admin/v1/series/3/revisions', async (route) => {
    await onCreate(route)
  })
}

async function openSeries(page: Page) {
  await page.goto('/content')
  await page.locator('.el-table__row').first().click()
  await expect(page.getByRole('button', { name: '新建修订' })).toBeVisible()
}

test.beforeEach(async ({ page }) => {
  await mockSession(page, 'operator')
})

test('creates a new revision from the rejected revision', async ({ page }) => {
  let sent: unknown = null
  await routeContent(page, async (route) => {
    sent = route.request().postDataJSON()
    await route.fulfill({
      status: 200,
      json: detailBody({
        draft: { id: '10', revision_no: 3, state: 'draft', version: 1, title: '测试剧集' },
        current_draft_revision_id: '10',
      }),
    })
  })
  await openSeries(page)
  // A rejected revision is never edited in place.
  await expect(page.getByText('当前修订不能直接编辑', { exact: false })).toBeVisible()
  await page.getByRole('button', { name: '新建修订' }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await page.getByRole('button', { name: '创建修订' }).click()
  await expect(page.getByText('已创建修订 #3', { exact: false })).toBeVisible()
  expect(sent).toEqual({ base_revision_id: '9' })
})

test('offers the explicit replacement when a draft still exists', async ({ page }) => {
  const bodies: unknown[] = []
  await routeContent(
    page,
    async (route) => {
      const body = route.request().postDataJSON() as Record<string, unknown>
      bodies.push(body)
      if (!('replace_draft_revision_id' in body)) {
        await route.fulfill({
          status: 409,
          json: { code: 409, reason: 'DRAFT_EXISTS', message: '该作品已有工作草稿' },
        })
        return
      }
      await route.fulfill({
        status: 200,
        json: detailBody({
          draft: { id: '11', revision_no: 3, state: 'draft', version: 1, title: '测试剧集' },
          current_draft_revision_id: '11',
        }),
      })
    },
    // The current revision is an editable draft here.
    detailBody({
      draft: { id: '9', revision_no: 2, state: 'draft', version: 4, title: '测试剧集' },
    }),
  )
  await openSeries(page)
  await page.getByRole('button', { name: '新建修订' }).click()
  await page.getByRole('button', { name: '创建修订' }).click()
  // The operator sees the draft that blocked the creation, with its version.
  await expect(page.getByText('可编辑草稿 #2（版本 4）仍在使用中', { exact: false })).toBeVisible()
  await page.getByRole('button', { name: '替换草稿 #2（版本 4）' }).click()
  await expect(page.getByText('已创建修订 #3', { exact: false })).toBeVisible()
  expect(bodies[1]).toEqual({
    base_revision_id: '9',
    replace_draft_revision_id: '9',
    replace_draft_version: 4,
  })
})

test('does not offer the entry to a reviewer', async ({ page }) => {
  await mockSession(page, 'reviewer')
  await routeContent(page, async (route) => {
    await route.fulfill({ status: 403, json: { code: 403, reason: 'FORBIDDEN' } })
  })
  await page.goto('/content')
  await page.locator('.el-table__row').first().click()
  await expect(page.getByText('测试剧集').first()).toBeVisible()
  await expect(page.getByRole('button', { name: '新建修订' })).toHaveCount(0)
  // Creating a work is a content write too, so the reviewer never sees it.
  await expect(page.getByRole('button', { name: '新建作品' })).toHaveCount(0)
  // Creating a work is a content write too, so the reviewer never sees it.
  await expect(page.getByRole('button', { name: '新建作品' })).toHaveCount(0)
})

test('creates a manual work from the content page', async ({ page }) => {
  let sent: Record<string, unknown> | null = null
  await page.route('**/api/admin/v1/series?**', (route) =>
    route.fulfill({ status: 200, json: { total: 0, page: 1, page_size: 20, items: [] } }),
  )
  await page.route('**/api/admin/v1/series', async (route) => {
    if (route.request().method() !== 'POST') {
      await route.fulfill({ status: 404, json: {} })
      return
    }
    sent = route.request().postDataJSON() as Record<string, unknown>
    await route.fulfill({
      status: 200,
      json: {
        id: '5',
        title: '人工短剧',
        description: '人工创建',
        status: 'draft',
        version: 1,
        current_draft_revision_id: '6',
        draft: { id: '6', revision_no: 1, state: 'draft', version: 1, title: '人工短剧' },
        episodes: [
          { id: '11', number: 1, selected: true },
          { id: '12', number: 2, selected: true },
        ],
      },
    })
  })
  await page.goto('/content')
  await page.getByRole('button', { name: '新建作品' }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await page.getByRole('dialog').locator('input').first().fill('人工短剧')
  await page.getByRole('dialog').locator('textarea').fill('人工创建')
  await page.getByRole('button', { name: '添加一集' }).click()
  await page.getByRole('button', { name: '保存草稿' }).click()
  await expect(page.getByText('已创建《人工短剧》草稿', { exact: false })).toBeVisible()
  expect(sent).toMatchObject({
    mode: 'manual',
    title: '人工短剧',
    description: '人工创建',
    episodes: [{ original_number: 1 }, { original_number: 2 }],
  })
  expect(typeof sent?.request_id).toBe('string')
})

test('refuses a repeated episode number before the request', async ({ page }) => {
  let posted = 0
  await page.route('**/api/admin/v1/series?**', (route) =>
    route.fulfill({ status: 200, json: { total: 0, page: 1, page_size: 20, items: [] } }),
  )
  await page.route('**/api/admin/v1/series', async (route) => {
    posted += 1
    await route.fulfill({ status: 500, json: {} })
  })
  await page.goto('/content')
  await page.getByRole('button', { name: '新建作品' }).click()
  await page.getByRole('dialog').locator('input').first().fill('重复集')
  await page.getByRole('button', { name: '添加一集' }).click()
  // Both rows hold the same number: the form refuses it and sends nothing.
  const numbers = page.getByRole('dialog').locator('.episode-row input')
  await numbers.nth(1).fill('1')
  await page.getByRole('button', { name: '保存草稿' }).click()
  await expect(page.getByText('分集序号重复，请检查', { exact: false })).toBeVisible()
  expect(posted).toBe(0)
})
