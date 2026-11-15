import { test, expect, type Page } from '@playwright/test'
import { mockSession } from './session'

const source = {
  id: 1,
  code: 'hongguo',
  name: '红果短剧',
  status: 'enabled',
  version: 1,
  updated_at: '2026-11-15T05:00:00Z',
}

const channel = {
  id: 1,
  source_id: 1,
  source_code: 'hongguo',
  source_name: '红果短剧',
  source_status: 'enabled',
  code: 'hot-drama',
  name: '热播榜',
  adapter_key: 'hongguo_web',
  entry_kind: 'rank',
  entry_code: 'hot-drama',
  base_url: 'https://hongguoduanju.com',
  allowed_hosts: 'hongguoduanju.com,www.hongguoduanju.com',
  rate_per_minute: 30,
  timeout_seconds: 20,
  has_secret: false,
  status: 'enabled',
  paused: false,
  paused_cause: '',
  version: 3,
  updated_at: '2026-11-15T05:00:00Z',
}

const entry = {
  source_code: 'hongguo',
  kind: 'rank',
  channel: 'hot-drama',
  label: '红果热播榜',
  adapter_key: 'hongguo_web',
  description: '只读服务端渲染页面里的元数据与分集身份',
}

async function routeConfiguration(page: Page) {
  await page.route('**/api/admin/v1/sources', (route) =>
    route.fulfill({ status: 200, json: { items: [source] } }),
  )
  await page.route('**/api/admin/v1/source-channels*', (route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({ status: 200, json: { items: [channel] } })
    }
    return route.fulfill({ status: 404, json: {} })
  })
  await page.route('**/api/admin/v1/ingestion/entries', (route) =>
    route.fulfill({ status: 200, json: { items: [entry] } }),
  )
}

test.beforeEach(async ({ page }) => {
  await mockSession(page, 'operator')
  await routeConfiguration(page)
})

test('edits the channel configuration with the version it read', async ({ page }) => {
  let sent: Record<string, unknown> | null = null
  await page.route('**/api/admin/v1/source-channels/1', async (route) => {
    if (route.request().method() !== 'PATCH') {
      await route.fulfill({ status: 404, json: {} })
      return
    }
    sent = route.request().postDataJSON() as Record<string, unknown>
    await route.fulfill({
      status: 200,
      json: { ...channel, rate_per_minute: 45, version: 4 },
    })
  })
  await page.goto('/sources')
  await expect(page.getByText('热播榜').first()).toBeVisible()
  await page.getByRole('button', { name: '查看' }).first().click()
  const rate = page.getByRole('dialog').locator('.el-input-number input').first()
  await rate.fill('45')
  await page.getByRole('button', { name: '保存' }).click()
  await expect(page.getByText('通道配置已保存', { exact: false })).toBeVisible()
  expect(sent).toMatchObject({
    expected_version: 3,
    name: '热播榜',
    entry_kind: 'rank',
    entry_code: 'hot-drama',
    base_url: 'https://hongguoduanju.com',
    allowed_hosts: 'hongguoduanju.com,www.hongguoduanju.com',
    rate_per_minute: 45,
    timeout_seconds: 20,
  })
})

test('keeps the operator input when the version is stale', async ({ page }) => {
  await page.route('**/api/admin/v1/source-channels/1', async (route) => {
    if (route.request().method() !== 'PATCH') {
      await route.fulfill({ status: 404, json: {} })
      return
    }
    await route.fulfill({
      status: 409,
      json: {
        code: 409,
        reason: 'VERSION_CONFLICT',
        message: '通道已被他人修改，请刷新后重试',
      },
    })
  })
  await page.goto('/sources')
  await page.getByRole('button', { name: '查看' }).first().click()
  const rate = page.getByRole('dialog').locator('.el-input-number input').first()
  await rate.fill('45')
  await page.getByRole('button', { name: '保存' }).click()
  await expect(page.getByText('VERSION_CONFLICT', { exact: false })).toBeVisible()
  // The typed value stays in the form.
  await expect(rate).toHaveValue('45')
})

test('pauses a channel and tests the connection explicitly', async ({ page }) => {
  const calls: string[] = []
  await page.route('**/api/admin/v1/source-channels/1/status', async (route) => {
    calls.push(`status ${route.request().method()}`)
    await route.fulfill({ status: 200, json: { ...channel, status: 'paused', paused: true } })
  })
  await page.route('**/api/admin/v1/source-channels/1/probe', async (route) => {
    calls.push(`probe ${route.request().method()}`)
    await route.fulfill({
      status: 200,
      json: {
        ok: true,
        channel: 'hot-drama',
        latency_ms: 421,
        items: 12,
        total_pages: 3,
        error_code: '',
        error_message: '',
        probed_at: '2026-11-15T05:10:00Z',
      },
    })
  })
  await page.goto('/sources')
  const channelRow = page.getByRole('row', { name: /hot-drama/ })
  await channelRow.getByRole('button', { name: '测试连接' }).click()
  await expect(page.getByText('成功：入口 hot-drama', { exact: false })).toBeVisible()
  await channelRow.getByRole('button', { name: '暂停' }).click()
  await expect(page.getByText('通道已暂停', { exact: false })).toBeVisible()
  expect(calls).toEqual(['probe POST', 'status POST'])
})
