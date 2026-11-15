import { test, expect, type Page, type Route } from '@playwright/test'
import { mockSession } from './session'

const session = {
  id: 1,
  revision_id: 7,
  episode_id: 11,
  kind: 'episode_input',
  status: 'pending',
  version: 1,
  filename: 'episode.mp4',
  content_type: 'video/mp4',
  size_bytes: 8,
  created_by: 'shared-token',
  revision_version: 1,
  expires_at: '2026-11-14T10:15:00Z',
  created_at: '2026-11-14T10:00:00Z',
  updated_at: '2026-11-14T10:00:00Z',
  authorization: {
    url: '/api/admin/v1/uploads/staging/signed-token',
    method: 'PUT',
    headers: { 'Content-Type': 'application/octet-stream' },
    expires_at: '2026-11-14T10:15:00Z',
  },
}

const episodes = {
  items: [
    { id: 11, external_id: 'ep-1', number: 1, selected: true },
    { id: 12, external_id: 'ep-2', number: 2, selected: true },
  ],
  total: 2,
}

// openUploadPage prepares the token and the revision, then loads the episodes.
async function openUploadPage(page: Page) {
  await page.goto('/uploads')
  const numbers = page.locator('.el-input-number input')
  await numbers.nth(0).fill('7')
  await numbers.nth(1).fill('1')
  await page.getByRole('button', { name: '加载分集' }).click()
  await expect(page.getByText('共 2 个分集')).toBeVisible()
}

// routeUploads mocks the session lifecycle; completeStatus decides the
// confirmation outcome.
async function routeUploads(page: Page, completeStatus: number, completeBody: unknown) {
  await page.route('**/api/admin/v1/revisions/7/episodes**', (route: Route) =>
    route.fulfill({ status: 200, json: episodes }),
  )
  await page.route('**/api/admin/v1/uploads?**', (route: Route) =>
    route.fulfill({ status: 200, json: { items: [], total: 0, page: 1, page_size: 20 } }),
  )
  await page.route('**/api/admin/v1/uploads/staging/**', (route: Route) =>
    route.fulfill({ status: 204, body: '' }),
  )
  await page.route('**/api/admin/v1/uploads/1/complete', (route: Route) =>
    route.fulfill({ status: completeStatus, json: completeBody }),
  )
  await page.route('**/api/admin/v1/uploads', (route: Route) => {
    if (route.request().method() !== 'POST') return route.continue()
    return route.fulfill({ status: 201, json: session })
  })
}

test.beforeEach(async ({ page }) => {
  await mockSession(page)
})

test('uploads an episode file and confirms the trusted input', async ({ page }) => {
  await routeUploads(page, 200, { ...session, status: 'completed', version: 2 })
  await openUploadPage(page)

  const rows = page.locator('.el-table__row')
  await rows
    .nth(0)
    .locator('input[type="file"]')
    .setInputFiles({
      name: 'episode.mp4',
      mimeType: 'video/mp4',
      buffer: Buffer.from('fake-mp4'),
    })
  await expect(rows.nth(0).getByText('已选择，未上传')).toBeVisible()
  await rows.nth(0).getByRole('button', { name: '上传并确认' }).click()
  await expect(rows.nth(0).getByText('已确认')).toBeVisible()
})

test('keeps the chosen file when the confirmation conflicts', async ({ page }) => {
  await routeUploads(page, 409, {
    code: 409,
    reason: 'REVISION_CHANGED',
    message: '修订已变化，旧上传不能绑定',
  })
  await openUploadPage(page)

  const rows = page.locator('.el-table__row')
  await rows
    .nth(0)
    .locator('input[type="file"]')
    .setInputFiles({
      name: 'episode.mp4',
      mimeType: 'video/mp4',
      buffer: Buffer.from('fake-mp4'),
    })
  await rows.nth(0).getByRole('button', { name: '上传并确认' }).click()
  // The failure is shown and the file stays selected, so a retry does not
  // ask the operator to pick it again.
  await expect(rows.nth(0).getByText(/REVISION_CHANGED/)).toBeVisible()
  // The retry button stays enabled and the session is still pending, so the
  // operator can confirm again without picking the file a second time.
  await expect(rows.nth(0).getByRole('button', { name: '上传并确认' })).toBeEnabled()
  await expect(rows.nth(0).getByText('待上传')).toBeVisible()
})
