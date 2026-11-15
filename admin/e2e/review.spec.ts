import { test, expect, type Page, type Route } from '@playwright/test'
import { mockSession } from './session'

// A claimed task waiting for the reviewer's decision.
const reviewCase = {
  task_id: 7,
  revision_id: 3,
  series_id: 1,
  batch_id: 4,
  status: 'claimed',
  assignee: 'rev-acc',
  submitted_by: 'op-acc',
  version: 2,
  created_at: '2026-11-15T05:00:00Z',
  series_title: '人工创建验收剧',
  item_total: 2,
  item_ready: 2,
  cover_status: 'ready',
}

async function routeReview(page: Page, onDecision: (route: Route) => Promise<void>) {
  await page.route('**/api/admin/v1/reviews?**', (route) =>
    route.fulfill({
      status: 200,
      json: { total: 1, page: 1, page_size: 20, items: [reviewCase] },
    }),
  )
  await page.route('**/api/admin/v1/reviews/7', (route) =>
    route.fulfill({
      status: 200,
      json: {
        case: reviewCase,
        episodes: [
          { episode_id: 11, number: 1, external_id: '', asset: { asset_id: 2 } },
          { episode_id: 12, number: 2, external_id: '', asset: { asset_id: 3 } },
        ],
        cover: { asset_id: 1 },
      },
    }),
  )
  await page.route('**/api/admin/v1/reviews/7/decision', async (route) => {
    await onDecision(route)
  })
}

test.beforeEach(async ({ page }) => {
  await mockSession(page, 'reviewer')
})

test('sends the checklist with an approval', async ({ page }) => {
  let sent: Record<string, unknown> | null = null
  await routeReview(page, async (route) => {
    sent = route.request().postDataJSON() as Record<string, unknown>
    await route.fulfill({ status: 200, json: { ...reviewCase, status: 'approved', version: 3 } })
  })
  await page.goto('/reviews')
  await page.getByRole('button', { name: '查看' }).first().click()
  for (const label of ['标题/封面/简介匹配', '分集对应', '声音画面正常', '授权与全年龄可公开']) {
    await page.locator('.checklist .el-checkbox').filter({ hasText: label }).click()
  }
  await page.locator('.checklist .el-input-number input').first().fill('2')
  await page.getByRole('button', { name: '通过' }).click()
  await expect(page.getByText('已通过，尚未发布')).toBeVisible()
  expect(sent).toMatchObject({
    expected_version: 2,
    decision: 'approved',
    checklist: {
      title_cover_intro: true,
      episode_mapping: true,
      audio_video: true,
      authorization_public: true,
      reviewed_episodes: 2,
    },
  })
})

test('sends no checklist with a rejection', async ({ page }) => {
  let sent: Record<string, unknown> | null = null
  await routeReview(page, async (route) => {
    sent = route.request().postDataJSON() as Record<string, unknown>
    await route.fulfill({ status: 200, json: { ...reviewCase, status: 'rejected', version: 3 } })
  })
  await page.goto('/reviews')
  await page.getByRole('button', { name: '查看' }).first().click()
  await page.getByPlaceholder('驳回理由（通过时忽略）').fill('第1集音画不同步')
  // The status label also appears in the filter, so the decision response is
  // the page's real acknowledgement of the rejection.
  await Promise.all([
    page.waitForResponse((response) => response.url().endsWith('/decision')),
    page.getByRole('button', { name: '驳回' }).click(),
  ])
  expect(sent).toMatchObject({ decision: 'rejected', reason: '第1集音画不同步' })
  expect(sent).not.toHaveProperty('checklist')
})
