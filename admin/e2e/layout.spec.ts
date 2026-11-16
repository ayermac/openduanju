import { test, expect } from '@playwright/test'
import { mockSession } from './session'

// The shell owns identity and exit for every guarded page. A page that still
// renders its own SessionBar shows two exits and two identities.
const guardedPages = [
  '/sources',
  '/source',
  '/media',
  '/uploads',
  '/authorizations',
  '/reviews',
  '/audit',
  '/staff',
  '/content',
]

for (const path of guardedPages) {
  test(`keeps one shell identity and exit on ${path}`, async ({ page }) => {
    await mockSession(page, 'admin')
    await page.route('**/api/admin/v1/**', (route) => {
      if (route.request().url().includes('/auth/me')) return route.fallback()
      return route.fulfill({ status: 200, json: { items: [], total: 0 } })
    })
    await page.goto(path)
    await expect(page.locator('.side')).toBeVisible()
    await expect(page.getByRole('button', { name: '退出' })).toHaveCount(1)
    await expect(page.locator('.session-bar')).toHaveCount(0)
    await expect(page.locator('.who .role')).toHaveText('管理员')
  })
}
