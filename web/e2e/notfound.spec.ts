import { test, expect } from '@playwright/test'

// An unknown public address must be explicit or replace itself with home; it
// must not keep the bad URL while pretending to be a valid landing page.
test('does not render home content under an unknown address', async ({ page }) => {
  await page.route('**/api/web/v1/categories', (route) =>
    route.fulfill({ json: { categories: [] } }),
  )
  await page.route('**/api/web/v1/series?*', (route) =>
    route.fulfill({ json: { total: 0, page: 1, page_size: 24, items: [] } }),
  )
  await page.goto('/this-page-does-not-exist')
  await expect(page.getByRole('heading', { name: '页面不存在' })).toBeVisible()
  await expect(page.getByText('请确认地址，或返回首页。')).toBeVisible()
  await page.getByRole('link', { name: '返回首页' }).click()
  await expect(page).toHaveURL('/')
})
