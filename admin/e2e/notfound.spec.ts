import { test, expect } from '@playwright/test'
import { mockSession } from './session'

// An unknown admin address must explain that the page does not exist and offer
// a way back; it must not render an empty shell or an unrelated page.
test('explains an unknown admin address and returns to the workbench', async ({ page }) => {
  await mockSession(page, 'admin')
  await page.goto('/this-page-does-not-exist')
  await expect(page.getByRole('heading', { name: '页面不存在' })).toBeVisible()
  await expect(page.getByText('请检查地址，或返回运营工作台。')).toBeVisible()
  await page.getByRole('link', { name: '返回工作台' }).click()
  await expect(page).toHaveURL('/')
})

test('forbidden pages keep the existing explicit explanation', async ({ page }) => {
  await mockSession(page, 'reviewer')
  await page.goto('/staff')
  await expect(page).toHaveURL('/forbidden')
})
