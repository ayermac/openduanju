import { test, expect } from '@playwright/test'
import { mockSession } from './session'

// A 401 from any guarded call means the session is gone: the client must clear
// the cached identity and CSRF token and return to login with the current
// internal path. A 403 is a permission answer and must not do this.
test('returns to login when a guarded request reports an expired session', async ({ page }) => {
  await mockSession(page, 'admin')
  await page.route('**/api/admin/v1/authorization-grants?**', (route) =>
    route.fulfill({
      status: 401,
      json: { code: 401, reason: 'AUTH_REQUIRED', message: '登录已失效' },
    }),
  )
  await page.goto('/authorizations')
  await expect(page).toHaveURL(/\/login\?next=\/authorizations/)
  await expect(page.getByRole('heading', { name: '登录运营后台' })).toBeVisible()
})

test('logout still reaches login when the logout call itself is unauthorized', async ({ page }) => {
  await mockSession(page, 'admin')
  await page.route('**/api/admin/v1/auth/logout', (route) =>
    route.fulfill({
      status: 401,
      json: { code: 401, reason: 'AUTH_REQUIRED', message: '登录已失效' },
    }),
  )
  await page.goto('/authorizations')
  await page.getByRole('button', { name: '退出' }).click()
  await expect(page).toHaveURL(/\/login/)
  await expect(page.getByRole('heading', { name: '登录运营后台' })).toBeVisible()
})
