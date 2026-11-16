import { test, expect } from '@playwright/test'
import { mockSession } from './session'

// The dates use UTC here on purpose: the table must show the same UTC instant
// that the API returned, not the browser's local wall clock.
const grant = {
  id: 3,
  series_id: 1,
  reference: 'AUTH-2026-001',
  allow_process: true,
  allow_display: true,
  allow_playback: true,
  valid_from: '2026-11-15T00:00:00Z',
  valid_until: '2026-12-15T00:00:00Z',
  status: 'active',
  version: 2,
  confirmed_by: 'admin',
  confirmed_at: '2026-11-14T10:00:00Z',
  created_at: '2026-11-14T10:00:00Z',
  updated_at: '2026-11-14T10:00:00Z',
}

test.use({ timezoneId: 'Asia/Shanghai' })

test.beforeEach(async ({ page }) => {
  await mockSession(page, 'admin')
  await page.route('**/api/admin/v1/authorization-grants?**', (route) =>
    route.fulfill({ status: 200, json: { total: 1, page: 1, page_size: 20, items: [grant] } }),
  )
})

test('keeps authorization writes out of the reviewer view', async ({ page }) => {
  await mockSession(page, 'reviewer')
  await page.goto('/authorizations')
  await expect(page.getByText('AUTH-2026-001')).toBeVisible()
  await expect(page.getByRole('button', { name: '创建依据' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '撤销' })).toHaveCount(0)
  await expect(page.getByText('授权维护需要管理员权限，当前为只读视图')).toBeVisible()
})

test('loads existing grants immediately and shows UTC times', async ({ page }) => {
  await page.goto('/authorizations')
  await expect(page.getByText('AUTH-2026-001')).toBeVisible()
  // 2026-11-15T00:00:00Z is 2026-11-15 08:00 local in Asia/Shanghai; the page
  // must not silently convert it while still labelling the column UTC.
  await expect(page.locator('.el-table').getByText('2026-11-15 00:00:00 UTC')).toBeVisible()
  await expect(page.locator('.el-table').getByText('2026-12-15 00:00:00 UTC')).toBeVisible()
  // "active" is the stored lifecycle state, not a promise that the window has
  // already started; the minimum honest label is "not revoked".
  await expect(page.locator('.el-table').getByText('未撤销')).toBeVisible()
})
