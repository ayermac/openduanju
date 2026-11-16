import { test, expect, type Page, type Route } from '@playwright/test'
import { mockSession } from './session'

// A claimed task waiting for the reviewer's decision.
const reviewCase = {
  task_id: 7,
  revision_id: 3,
  series_id: 1,
  batch_id: 4,
  status: 'claimed',
  assignee: 'reviewer',
  submitted_by: 'op-acc',
  version: 2,
  created_at: '2026-11-15T05:00:00Z',
  series_title: '人工创建验收剧',
  item_total: 2,
  item_ready: 2,
  cover_status: 'ready',
}

async function routeReview(
  page: Page,
  onDecision: (route: Route) => Promise<void>,
  overrides: Partial<typeof reviewCase> = {},
) {
  const task = { ...reviewCase, ...overrides }
  await page.route('**/api/admin/v1/reviews?**', (route) =>
    route.fulfill({
      status: 200,
      json: { total: 1, page: 1, page_size: 20, items: [task] },
    }),
  )
  await page.route('**/api/admin/v1/reviews/7', (route) =>
    route.fulfill({
      status: 200,
      json: {
        case: task,
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

function detailFor(task: typeof reviewCase) {
  return {
    case: task,
    episodes: [{ episode_id: 11, number: 1, external_id: '', asset: { asset_id: 2 } }],
    cover: { asset_id: 1 },
  }
}

test('requires fresh confirmation and disables decisions while another task loads', async ({
  page,
}) => {
  const first = { ...reviewCase, assignee: 'reviewer' }
  const second = { ...reviewCase, task_id: 8, revision_id: 4, series_title: '第二部待审剧' }
  await page.route('**/api/admin/v1/reviews?**', (route) =>
    route.fulfill({
      status: 200,
      json: { total: 2, page: 1, page_size: 20, items: [first, second] },
    }),
  )
  await page.route('**/api/admin/v1/reviews/7', (route) =>
    route.fulfill({ status: 200, json: detailFor(first) }),
  )
  let releaseSecond: (() => void) | undefined
  const secondResponse = new Promise<void>((resolve) => {
    releaseSecond = resolve
  })
  await page.route('**/api/admin/v1/reviews/8', async (route) => {
    await secondResponse
    await route.fulfill({ status: 200, json: detailFor(second) })
  })

  await page.goto('/reviews')
  await page.getByRole('button', { name: '查看' }).first().click()
  for (const label of ['标题/封面/简介匹配', '分集对应', '声音画面正常', '授权与全年龄可公开']) {
    await page.locator('.checklist .el-checkbox').filter({ hasText: label }).click()
  }
  await page.locator('.checklist .el-input-number input').fill('1')
  await page.getByPlaceholder('驳回理由（通过时忽略）').fill('只适用于第一项任务')

  await page.getByRole('button', { name: '查看' }).nth(1).click()
  await expect(page.getByRole('button', { name: '通过' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '驳回' })).toHaveCount(0)
  releaseSecond?.()
  await expect(page.getByText('第二部待审剧', { exact: false }).last()).toBeVisible()
  await expect(page.locator('.checklist .el-checkbox input:checked')).toHaveCount(0)
  await expect(page.locator('.checklist .el-input-number input')).toHaveValue('1')
  await expect(page.getByPlaceholder('驳回理由（通过时忽略）')).toHaveValue('')
})

test('hides review actions from an operator and explains the read-only view', async ({ page }) => {
  await mockSession(page, 'operator')
  await routeReview(page, async () => {})
  await page.goto('/reviews')
  await page.getByRole('button', { name: '查看' }).first().click()
  await expect(page.getByRole('button', { name: '认领' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '通过' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '驳回' })).toHaveCount(0)
  await expect(page.getByText('审核裁决需要审核权限')).toBeVisible()
})

test('keeps decisions with the assignee and blocks self review', async ({ page }) => {
  // The general fixture belongs to the signed-in reviewer for decision tests;
  // this test needs a task held by another account.
  await routeReview(page, async () => {}, { assignee: 'other-reviewer' })
  await page.goto('/reviews')
  await page.getByRole('button', { name: '查看' }).first().click()
  // A reviewer may decide a task claimed by someone else only when they are
  // the assignee; this fixture was claimed by another account.
  await expect(page.getByRole('button', { name: '认领' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '通过' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '驳回' })).toHaveCount(0)
  await expect(page.getByText('该任务由其他认领人处理')).toBeVisible()

  await page.route(
    '**/api/admin/v1/reviews/7',
    (route) =>
      route.fulfill({
        status: 200,
        json: { case: { ...reviewCase, assignee: 'other-reviewer', submitted_by: 'reviewer' } },
      }),
    { times: 1 },
  )
  await page.getByRole('button', { name: '刷新' }).click()
  await page.getByRole('button', { name: '查看' }).first().click()
  await expect(page.getByRole('button', { name: '认领' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '通过' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '驳回' })).toHaveCount(0)
  await expect(page.getByText('不能审核自己提交的内容')).toBeVisible()
})

test('keeps the newest task when an earlier detail response arrives late', async ({ page }) => {
  const first = { ...reviewCase, assignee: 'reviewer' }
  const second = { ...reviewCase, task_id: 8, revision_id: 4, series_title: '最新打开的任务' }
  await page.route('**/api/admin/v1/reviews?**', (route) =>
    route.fulfill({
      status: 200,
      json: { total: 2, page: 1, page_size: 20, items: [first, second] },
    }),
  )
  let releaseFirst: (() => void) | undefined
  const firstResponse = new Promise<void>((resolve) => {
    releaseFirst = resolve
  })
  await page.route('**/api/admin/v1/reviews/7', async (route) => {
    await firstResponse
    await route.fulfill({ status: 200, json: detailFor(first) })
  })
  await page.route('**/api/admin/v1/reviews/8', (route) =>
    route.fulfill({ status: 200, json: detailFor(second) }),
  )

  await page.goto('/reviews')
  await page.getByRole('button', { name: '查看' }).first().click()
  await page.getByRole('button', { name: '查看' }).nth(1).click()
  const currentDetail = page.locator('.panel-title').filter({ hasText: '任务详情 #8' })
  await expect(currentDetail).toContainText('最新打开的任务')
  releaseFirst?.()
  await expect(currentDetail).toContainText('最新打开的任务')
})
