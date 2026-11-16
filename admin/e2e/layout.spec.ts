import { test, expect, type Page } from '@playwright/test'
import { mockSession } from './session'

async function expectNoHorizontalOverflow(page: Page) {
  await expect
    .poll(() =>
      page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      ),
    )
    .toBe(0)
}

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

test('keeps the content table inside the viewport through narrow resizes', async ({ page }) => {
  await mockSession(page, 'admin')
  await page.route('**/api/admin/v1/series?**', (route) =>
    route.fulfill({ status: 200, json: { total: 0, page: 1, page_size: 20, items: [] } }),
  )

  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/content')
  await expect(page.locator('.el-table')).toBeVisible()
  await expectNoHorizontalOverflow(page)

  await page.setViewportSize({ width: 1024, height: 900 })
  await expectNoHorizontalOverflow(page)

  await page.setViewportSize({ width: 768, height: 900 })
  await expectNoHorizontalOverflow(page)

  await page.setViewportSize({ width: 1440, height: 900 })
  await expectNoHorizontalOverflow(page)

  await page.setViewportSize({ width: 1024, height: 900 })
  await page.goto('/content')
  await expect(page.locator('.el-table')).toBeVisible()
  await expectNoHorizontalOverflow(page)
})
