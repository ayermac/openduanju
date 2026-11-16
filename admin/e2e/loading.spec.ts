import { test, expect } from '@playwright/test'
import { mockSession } from './session'

// v-loading is a directive, not a component import. If main.ts forgets to
// register it, the button shows a spinner while the table stays silent.
test('shows a real loading mask while the source list is slow', async ({ page }) => {
  await mockSession(page, 'admin')
  let release: (() => void) | undefined
  const slow = new Promise<void>((resolve) => {
    release = resolve
  })
  await page.route('**/api/admin/v1/ingestion/sources', async (route) => {
    await slow
    await route.fulfill({ status: 200, json: { items: [], total: 0 } })
  })
  await page.route('**/api/admin/v1/ingestion/channels', (route) =>
    route.fulfill({ status: 200, json: { items: [], total: 0 } }),
  )

  await page.goto('/sources')
  await expect(page.locator('.el-loading-mask').first()).toBeVisible()
  release?.()
  await expect(page.locator('.el-loading-mask')).toHaveCount(0)
})
