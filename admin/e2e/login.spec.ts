import { test, expect } from '@playwright/test'
import { mockSession, sessionBody } from './session'

test('redirects an anonymous visitor to the login page', async ({ page }) => {
  await page.route('**/api/admin/v1/auth/me', (route) =>
    route.fulfill({
      status: 401,
      json: { code: 401, reason: 'AUTH_REQUIRED', message: '需要登录' },
    }),
  )
  await page.goto('/media')
  await expect(page).toHaveURL(/\/login/)
  await expect(page.getByRole('heading', { name: '登录运营后台' })).toBeVisible()
})

test('login submits once for one Enter press', async ({ page }) => {
  let loginRequests = 0
  await page.route('**/api/admin/v1/auth/me', (route) =>
    route.fulfill({
      status: 401,
      json: { code: 401, reason: 'AUTH_REQUIRED', message: '需要登录' },
    }),
  )
  await page.route('**/api/admin/v1/auth/login', (route) => {
    loginRequests += 1
    return route.fulfill({ status: 200, json: sessionBody('admin') })
  })
  await page.goto('/login')
  await page.getByPlaceholder('请输入用户名').fill('admin')
  await page.getByPlaceholder('请输入密码').fill('local-e2e-password-2026')
  await page.getByPlaceholder('请输入密码').press('Enter')
  await expect(page.getByRole('heading', { name: '运营工作台' })).toBeVisible()
  expect(loginRequests).toBe(1)
})

test('shows the reason of a rejected login', async ({ page }) => {
  await page.route('**/api/admin/v1/auth/me', (route) =>
    route.fulfill({
      status: 401,
      json: { code: 401, reason: 'AUTH_REQUIRED', message: '需要登录' },
    }),
  )
  await page.route('**/api/admin/v1/auth/login', (route) =>
    route.fulfill({
      status: 401,
      json: { code: 401, reason: 'INVALID_CREDENTIALS', message: '用户名或密码错误' },
    }),
  )
  await page.goto('/login')
  await page.getByPlaceholder('请输入用户名').fill('admin')
  await page.getByPlaceholder('请输入密码').fill('wrong-password')
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page.getByText(/INVALID_CREDENTIALS/)).toBeVisible()
})

test('logs in and lands on the workbench', async ({ page }) => {
  let loggedIn = false
  await page.route('**/api/admin/v1/auth/me', (route) =>
    loggedIn
      ? route.fulfill({ status: 200, json: sessionBody('admin') })
      : route.fulfill({
          status: 401,
          json: { code: 401, reason: 'AUTH_REQUIRED', message: '需要登录' },
        }),
  )
  await page.route('**/api/admin/v1/auth/login', (route) => {
    loggedIn = true
    return route.fulfill({ status: 200, json: sessionBody('admin') })
  })
  await page.goto('/login')
  await page.getByPlaceholder('请输入用户名').fill('admin')
  await page.getByPlaceholder('请输入密码').fill('local-e2e-password-2026')
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page.getByRole('heading', { name: '运营工作台' })).toBeVisible()
  await expect(page.locator('.top .role')).toHaveText('管理员')
})

test('ignores a protocol-relative next after login and falls back to the workbench', async ({
  page,
}) => {
  await page.route('**/api/admin/v1/auth/me', (route) =>
    route.fulfill({
      status: 401,
      json: { code: 401, reason: 'AUTH_REQUIRED', message: '需要登录' },
    }),
  )
  await page.route('**/api/admin/v1/auth/login', (route) =>
    route.fulfill({ status: 200, json: sessionBody('admin') }),
  )
  await page.goto('/login?next=//evil.example/path')
  await page.getByPlaceholder('请输入用户名').fill('admin')
  await page.getByPlaceholder('请输入密码').fill('local-e2e-password-2026')
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page).toHaveURL(/\/$/)
  await expect(page.getByRole('heading', { name: '运营工作台' })).toBeVisible()
})

test('honours an internal next after login', async ({ page }) => {
  await page.route('**/api/admin/v1/auth/me', (route) =>
    route.fulfill({
      status: 401,
      json: { code: 401, reason: 'AUTH_REQUIRED', message: '需要登录' },
    }),
  )
  await page.route('**/api/admin/v1/auth/login', (route) =>
    route.fulfill({ status: 200, json: sessionBody('admin') }),
  )
  await page.route('**/api/admin/v1/staff?**', (route) =>
    route.fulfill({ status: 200, json: { total: 0, page: 1, page_size: 20, items: [] } }),
  )
  await page.goto('/login?next=/staff')
  await page.getByPlaceholder('请输入用户名').fill('admin')
  await page.getByPlaceholder('请输入密码').fill('local-e2e-password-2026')
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page).toHaveURL(/\/staff$/)
})

test('an operator sees an explicit forbidden page for staff management', async ({ page }) => {
  await mockSession(page, 'operator')
  await page.goto('/staff')
  await expect(page).toHaveURL(/\/forbidden/)
  await expect(page.getByText('当前角色无权访问')).toBeVisible()
  await expect(page.getByText(/该页面需要更高权限/)).toBeVisible()
})
