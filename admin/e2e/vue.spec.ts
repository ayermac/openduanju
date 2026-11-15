import { test, expect } from '@playwright/test'
import { mockSession } from './session'

test.beforeEach(async ({ page }) => {
  await mockSession(page)
})

test('serves the admin foundation without browser errors', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  await page.goto('/')
  await expect(page.getByRole('heading', { name: '运营工作台', exact: true })).toBeVisible()
  await expect(
    page.getByText('真实 SDK 取源与真实 COS 仍待外部条件', { exact: false }),
  ).toBeVisible()
  await expect(page.getByRole('link', { name: /审计记录/ }).first()).toBeVisible()
  expect(errors).toEqual([])
})
