import { test, expect, type Page } from '@playwright/test'
import { mockSession } from './session'

// The audit list is read-only. These cases cover what an operator needs from
// it: the filters reach the server, a refused write is visible as a failure
// with its reason and report number, and an unreadable trail is reported as an
// error instead of an empty page.

const successRow = {
  id: '1',
  trace_id: '4bf92f3577b34da6a3ce929d0e0e4736',
  actor_id: '7',
  actor_name: 'operator-a',
  action: 'media.batch.submit',
  resource_type: 'media_batch',
  resource_id: '12',
  resource_version: 3,
  result: 'success',
  reason: '',
  created_at: '2026-11-15T07:00:00Z',
}

const failureRow = {
  id: '2',
  trace_id: 'failure-trace-1',
  actor_id: '9',
  actor_name: 'operator-b',
  action: 'publication.publish',
  resource_type: 'publication',
  resource_id: '3',
  resource_version: 0,
  result: 'failure',
  reason: 'PRECONDITION_FAILED',
  created_at: '2026-11-15T07:05:00Z',
}

async function routeAudit(
  page: Page,
  body: unknown,
  status = 200,
  onRequest?: (url: string) => void,
) {
  await page.route('**/api/admin/v1/audit?**', async (route) => {
    onRequest?.(route.request().url())
    await route.fulfill({ status, json: body })
  })
}

test.beforeEach(async ({ page }) => {
  await mockSession(page, 'operator')
})

test('lists a success and a failure with their reason and report number', async ({ page }) => {
  await routeAudit(page, {
    items: [successRow, failureRow],
    total: 2,
    page: 1,
    page_size: 20,
  })
  await page.goto('/audit')
  await expect(page.getByRole('heading', { name: '审计记录' })).toBeVisible()
  await expect(page.locator('.el-table__row')).toHaveCount(2)
  // The column header is "失败码", so the result tag is matched exactly.
  await expect(page.getByText('成功', { exact: true })).toBeVisible()
  await expect(page.getByText('失败', { exact: true })).toBeVisible()
  await expect(page.getByText('PRECONDITION_FAILED')).toBeVisible()
  await expect(page.getByText(failureRow.trace_id)).toBeVisible()
  await expect(page.getByText('media_batch #12 · v3')).toBeVisible()
})

test('sends the filters the operator chose', async ({ page }) => {
  let called = ''
  await routeAudit(page, { items: [failureRow], total: 1, page: 1, page_size: 20 }, 200, (url) => {
    called = url
  })
  // The first load is unfiltered; the filtered call is the one under test.
  await page.goto('/audit')
  await page.getByPlaceholder('报障编号（X-Trace-Id）').fill('failure-trace-1')
  await page.getByPlaceholder('操作人').fill('operator-b')
  await page.getByRole('button', { name: '查询' }).click()
  await expect(page.locator('.el-table__row')).toHaveCount(1)
  expect(called).toContain('trace_id=failure-trace-1')
  expect(called).toContain('actor_name=operator-b')
})

test('reports an unreadable trail as an error, not an empty list', async ({ page }) => {
  await routeAudit(
    page,
    {
      code: 500,
      reason: 'INTERNAL',
      message: '审计记录读取失败',
      metadata: { trace_id: 'read-trace-500' },
    },
    500,
  )
  await page.goto('/audit')
  await expect(page.getByText('审计记录读取失败（报障编号 read-trace-500）')).toBeVisible()
  await expect(page.locator('.el-table__row')).toHaveCount(0)
  await expect(page.getByText('暂无审计记录')).toHaveCount(0)
})

test('a reviewer may read the trail', async ({ page }) => {
  await mockSession(page, 'reviewer')
  await routeAudit(page, { items: [successRow], total: 1, page: 1, page_size: 20 })
  await page.goto('/audit')
  await expect(page.locator('.el-table__row')).toHaveCount(1)
  // Read-only: the page offers no write action at all.
  await expect(page.getByRole('button', { name: '查询' })).toBeVisible()
  await expect(page.getByRole('button', { name: '新建' })).toHaveCount(0)
})
