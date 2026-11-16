import { test, expect, type Page } from '@playwright/test'
import { mockSession } from './session'

function staffRow(id: number, version: number, role = 'operator', enabled = true) {
  return {
    id,
    username: `staff-${id}`,
    display_name: `人员 ${id}`,
    role,
    enabled,
    version,
    created_at: '2026-11-15T10:00:00Z',
    updated_at: '2026-11-15T10:00:00Z',
  }
}

async function mockStaff(page: Page, rows = [staffRow(1, 2, 'admin'), staffRow(2, 3)]) {
  let updates = 0
  let resets = 0
  await page.route('**/api/admin/v1/staff?**', (route) =>
    route.fulfill({
      status: 200,
      json: { total: rows.length, page: 1, page_size: 20, items: rows },
    }),
  )
  await page.route('**/api/admin/v1/staff/2', (route) => {
    updates += 1
    return route.fulfill({ status: 200, json: staffRow(2, 4) })
  })
  await page.route('**/api/admin/v1/staff/2/reset-password', (route) => {
    resets += 1
    return route.fulfill({ status: 200, json: staffRow(2, 4) })
  })
  return {
    updateCount: () => updates,
    resetCount: () => resets,
  }
}

test.beforeEach(async ({ page }) => {
  await mockSession(page, 'admin')
})

test('confirms stopping an account and does not write on cancel', async ({ page }) => {
  const counters = await mockStaff(page)
  await page.goto('/staff')
  const targetRow = page.getByRole('row').filter({ hasText: 'staff-2' })
  await targetRow.getByRole('button', { name: '停用' }).click()
  await expect(page.getByText('停用账号 staff-2')).toBeVisible()
  await expect(page.getByText('该账号已有会话会立即失效')).toBeVisible()
  await page.getByRole('button', { name: '取消' }).click()
  expect(counters.updateCount()).toBe(0)

  await targetRow.getByRole('button', { name: '停用' }).click()
  await page.getByRole('button', { name: '确认停用' }).click()
  expect(counters.updateCount()).toBe(1)
})

test('resets a password from a masked dialog without using prompt', async ({ page }) => {
  const counters = await mockStaff(page)
  await page.goto('/staff')
  const targetRow = page.getByRole('row').filter({ hasText: 'staff-2' })
  await targetRow.getByRole('button', { name: '重置密码' }).click()
  await expect(page.getByText('重置 staff-2 的密码')).toBeVisible()
  const secret = page.getByLabel('新密码')
  await expect(secret).toHaveAttribute('type', 'password')
  await secret.fill('local-reset-password-2026')
  await page.getByRole('button', { name: '取消' }).click()
  expect(counters.resetCount()).toBe(0)

  await targetRow.getByRole('button', { name: '重置密码' }).click()
  await page.getByLabel('新密码').fill('local-reset-password-2026')
  await page.getByRole('button', { name: '确认重置' }).click()
  expect(counters.resetCount()).toBe(1)
})

test('keeps staff and review and grant lists on their requested page', async ({ page }) => {
  const rows = Array.from({ length: 20 }, (_, index) => staffRow(index + 1, 1))
  await page.route('**/api/admin/v1/staff?**', (route) => {
    const page2 = new URL(route.request().url()).searchParams.get('page') === '2'
    return route.fulfill({
      status: 200,
      json: {
        total: 21,
        page: page2 ? 2 : 1,
        page_size: 20,
        items: page2 ? [staffRow(21, 1)] : rows,
      },
    })
  })
  await page.route('**/api/admin/v1/reviews?**', (route) => {
    const page2 = new URL(route.request().url()).searchParams.get('page') === '2'
    return route.fulfill({
      status: 200,
      json: { total: 21, page: page2 ? 2 : 1, page_size: 20, items: [] },
    })
  })
  await page.route('**/api/admin/v1/authorization-grants?**', (route) => {
    const page2 = new URL(route.request().url()).searchParams.get('page') === '2'
    return route.fulfill({
      status: 200,
      json: { total: 21, page: page2 ? 2 : 1, page_size: 20, items: [] },
    })
  })

  await page.goto('/staff')
  await page.locator('.el-pagination .btn-next').click()
  await expect(page.getByText('staff-21')).toBeVisible()

  await page.goto('/reviews')
  await page.locator('.el-pagination .btn-next').click()
  await expect(page.getByText('共 21 个任务')).toBeVisible()

  await page.goto('/authorizations')
  await page.locator('.el-pagination .btn-next').click()
  await expect(page.getByText('共 21 条')).toBeVisible()
})

test('revokes a grant through a required-reason dialog and carries the version', async ({
  page,
}) => {
  const grant = {
    id: 3,
    series_id: 1,
    reference: 'AUTH-2026-003',
    allow_process: true,
    allow_display: true,
    allow_playback: true,
    valid_from: '2026-11-15T00:00:00Z',
    status: 'active',
    version: 2,
    confirmed_by: 'admin',
    confirmed_at: '2026-11-14T10:00:00Z',
    created_at: '2026-11-14T10:00:00Z',
    updated_at: '2026-11-14T10:00:00Z',
  }
  let revocations = 0
  await page.route('**/api/admin/v1/authorization-grants?**', (route) =>
    route.fulfill({ status: 200, json: { total: 1, page: 1, page_size: 20, items: [grant] } }),
  )
  await page.route('**/api/admin/v1/authorization-grants/3/revoke', async (route) => {
    revocations += 1
    const body = route.request().postDataJSON() as { expected_version: number; reason: string }
    expect(body).toMatchObject({ expected_version: 2, reason: '授权材料到期' })
    return route.fulfill({ status: 200, json: { ...grant, status: 'revoked', version: 3 } })
  })

  await page.goto('/authorizations')
  await page.getByRole('button', { name: '撤销' }).click()
  await expect(page.getByText('撤销授权依据 AUTH-2026-003')).toBeVisible()
  await page.getByRole('button', { name: '确认撤销' }).click()
  await expect(page.getByText('撤销原因是必填项', { exact: true })).toBeVisible()
  expect(revocations).toBe(0)

  await page.getByLabel('撤销原因').fill('授权材料到期')
  await page.getByRole('button', { name: '确认撤销' }).click()
  expect(revocations).toBe(1)
})

test('guards unsaved manual work against accidental close', async ({ page }) => {
  await page.route('**/api/admin/v1/series?**', (route) =>
    route.fulfill({ status: 200, json: { total: 0, page: 1, page_size: 20, items: [] } }),
  )
  let creations = 0
  await page.route('**/api/admin/v1/series', (route) => {
    if (route.request().method() === 'POST') {
      creations += 1
      return route.fulfill({ status: 200, json: {} })
    }
    return route.fallback()
  })

  await page.goto('/content')
  await page.getByRole('button', { name: '新建作品' }).click()
  await page.getByLabel('标题（必填，最多120字）').fill('未保存的作品')
  await page.keyboard.press('Escape')
  await expect(
    page.getByText('关闭会丢失未保存内容。继续编辑保留；放弃修改会清空并关闭。'),
  ).toBeVisible()
  expect(creations).toBe(0)
  await page.getByRole('button', { name: '继续编辑' }).click()
  await expect(page.getByLabel('标题（必填，最多120字）')).toHaveValue('未保存的作品')

  await page.keyboard.press('Escape')
  await page.getByRole('button', { name: '放弃修改' }).click()
  await expect(page.getByRole('dialog', { name: '新建作品（人工创建）' })).toHaveCount(0)
  expect(creations).toBe(0)
})
