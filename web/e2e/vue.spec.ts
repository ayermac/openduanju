import { test, expect } from '@playwright/test'

const series = {
  series_id: 1,
  title: '公开验证剧',
  description: 'WEB-001 公开投影',
  cover_url: '',
  episode_count: 2,
  category: '短剧',
  published_at: '2026-11-14T10:00:00Z',
}

test('lists the published series on the home page', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  await page.route('**/api/web/v1/categories', (route) =>
    route.fulfill({ json: { categories: [series.category] } }),
  )
  await page.route('**/api/web/v1/series?*', (route) =>
    route.fulfill({ json: { total: 1, page: 1, page_size: 24, items: [series] } }),
  )
  await page.goto('/')
  await expect(page.getByRole('heading', { name: '最近发布' })).toBeVisible()
  await expect(page.getByRole('link', { name: /公开验证剧/ })).toBeVisible()
  await expect(page.getByRole('button', { name: '短剧' })).toBeVisible()
  await expect(page.getByText('2 集可看')).toBeVisible()
  expect(errors).toEqual([])
})

test('search submits once and the latest query wins', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))

  let releaseOld: (() => void) | undefined
  const oldResponse = new Promise<void>((resolve) => {
    releaseOld = resolve
  })
  let oldRequests = 0
  let newRequests = 0

  await page.route('**/api/web/v1/categories', (route) =>
    route.fulfill({ json: { categories: [] } }),
  )
  await page.route('**/api/web/v1/series?*', async (route) => {
    const q = new URL(route.request().url()).searchParams.get('q') ?? ''
    if (q === '旧剧名') {
      oldRequests += 1
      await oldResponse
      await route.fulfill({
        json: {
          total: 1,
          page: 1,
          page_size: 24,
          items: [{ ...series, series_id: 1, title: '旧剧名命中' }],
        },
      })
      return
    }
    if (q === '新剧名') {
      newRequests += 1
      await route.fulfill({
        json: {
          total: 1,
          page: 1,
          page_size: 24,
          items: [{ ...series, series_id: 2, title: '新剧名命中' }],
        },
      })
      return
    }
    await route.fulfill({ json: { total: 0, page: 1, page_size: 24, items: [] } })
  })

  await page.goto('/')
  await expect(page.getByRole('heading', { name: '还没有已发布的作品' })).toBeVisible()
  await page.getByPlaceholder('搜索片名或简介').fill('旧剧名')
  await page.keyboard.press('Enter')
  await page.waitForFunction(() => document.body.textContent?.includes('旧剧名命中') === false)
  await page.getByPlaceholder('搜索片名或简介').fill('新剧名')
  await page.keyboard.press('Enter')
  await page.getByRole('link', { name: '新剧名命中' }).waitFor()
  // The first query was slower on purpose: it must not overwrite the newer one.
  releaseOld?.()
  await page.waitForTimeout(100)
  await expect(page.getByRole('link', { name: '新剧名命中' })).toBeVisible()
  await expect(page.getByRole('link', { name: '旧剧名命中' })).toHaveCount(0)
  expect(oldRequests).toBe(1)
  expect(newRequests).toBe(1)
  expect(errors).toEqual([])
})

test('opens a detail page with the episode list and the player', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  await page.route('**/api/web/v1/categories', (route) =>
    route.fulfill({ json: { categories: [series.category] } }),
  )
  await page.route('**/api/web/v1/series?*', (route) =>
    route.fulfill({ json: { total: 1, page: 1, page_size: 24, items: [series] } }),
  )
  await page.route('**/api/web/v1/series/1', (route) =>
    route.fulfill({
      json: {
        series,
        revision_id: 3,
        publication_id: 1,
        episodes: [
          {
            episode_id: 2,
            number: 1,
            external_id: 'ep-1',
            asset_id: 2,
            duration_ms: 2000,
            width: 320,
            height: 240,
          },
          {
            episode_id: 3,
            number: 2,
            external_id: 'ep-2',
            asset_id: 3,
            duration_ms: 2000,
            width: 320,
            height: 240,
          },
        ],
      },
    }),
  )
  await page.route('**/api/web/v1/playback-sessions', (route) =>
    route.fulfill({
      status: 201,
      json: {
        session_id: 13,
        token: 'test-token',
        url: '/api/web/v1/media/test-token',
        expires_at: new Date(Date.now() + 600000).toISOString(),
        episode_id: 2,
        revision_id: 3,
      },
    }),
  )
  await page.goto('/')
  await page.locator('a.poster').first().click()
  await expect(page.getByRole('heading', { name: '公开验证剧' })).toBeVisible()
  await expect(page.locator('video')).toBeVisible()
  await expect(page.getByRole('button', { name: '第 1 集' })).toBeVisible()
  await expect(page.getByRole('button', { name: '下一集' })).toBeEnabled()
  expect(errors).toEqual([])
})
