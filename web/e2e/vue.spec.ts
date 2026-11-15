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
