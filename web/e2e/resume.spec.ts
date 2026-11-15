import { test, expect, type Page } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync, existsSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

// The resume flow needs a real, seekable video: the test generates one with
// ffmpeg and skips when the tool is not installed.
const series = {
  series_id: '1',
  title: '续播验证剧',
  description: '本机续播',
  cover_url: '',
  episode_count: 5,
  category: '短剧',
  published_at: '2026-11-15T02:00:00Z',
}

const episodes = [1, 2, 3, 4, 5].map((number) => ({
  episode_id: `10${number}`,
  number,
  external_id: `ep-${number}`,
  asset_id: `20${number}`,
  duration_ms: 12000,
  width: 320,
  height: 240,
}))

let videoPath = ''

test.beforeAll(() => {
  try {
    const dir = mkdtempSync(join(tmpdir(), 'openduanju-resume-'))
    videoPath = join(dir, 'resume.mp4')
    execFileSync('ffmpeg', [
      '-nostdin',
      '-hide_banner',
      '-loglevel',
      'error',
      '-y',
      '-f',
      'lavfi',
      '-i',
      'testsrc=size=320x240:rate=15',
      '-f',
      'lavfi',
      '-i',
      'sine=frequency=440:sample_rate=44100',
      '-t',
      '12',
      '-c:v',
      'libx264',
      '-preset',
      'ultrafast',
      '-pix_fmt',
      'yuv420p',
      '-c:a',
      'aac',
      '-movflags',
      '+faststart',
      videoPath,
    ])
  } catch {
    videoPath = ''
  }
})

// routePublic answers the public API and serves the generated video for every
// episode, recording which episode the player asked for.
async function routePublic(page: Page, requested: string[], published = { episodes }) {
  await page.route('**/api/web/v1/categories', (route) =>
    route.fulfill({ json: { categories: [series.category] } }),
  )
  await page.route('**/api/web/v1/series?*', (route) =>
    route.fulfill({ json: { total: 1, page: 1, page_size: 24, items: [series] } }),
  )
  await page.route('**/api/web/v1/series/1', (route) =>
    route.fulfill({
      json: { series, revision_id: 3, publication_id: 1, episodes: published.episodes },
    }),
  )
  await page.route('**/api/web/v1/playback-sessions', async (route) => {
    const body = route.request().postDataJSON() as { episode_id: string }
    requested.push(String(body.episode_id))
    await route.fulfill({
      status: 201,
      json: {
        session_id: 1,
        token: `token-${body.episode_id}`,
        url: `/media/${body.episode_id}`,
        expires_at: new Date(Date.now() + 600_000).toISOString(),
        episode_id: body.episode_id,
        revision_id: 3,
      },
    })
  })
  // The media endpoint of the server answers Range requests (206); the mock
  // has to do the same, otherwise the player cannot seek to a stored
  // position on a resource the browser considers non-seekable.
  await page.route('**/media/*', async (route) => {
    const body = readFileSync(videoPath)
    const range = route.request().headers()['range']
    const match = range ? /bytes=(\d+)-(\d*)/.exec(range) : null
    if (match) {
      const start = Number(match[1])
      const end = match[2] ? Number(match[2]) : body.length - 1
      await route.fulfill({
        status: 206,
        headers: {
          'Content-Type': 'video/mp4',
          'Accept-Ranges': 'bytes',
          'Content-Range': `bytes ${start}-${end}/${body.length}`,
          'Content-Length': String(end - start + 1),
        },
        body: body.subarray(start, end + 1),
      })
      return
    }
    await route.fulfill({
      status: 200,
      headers: {
        'Content-Type': 'video/mp4',
        'Accept-Ranges': 'bytes',
        'Content-Length': String(body.length),
      },
      body,
    })
  })
}

async function position(page: Page): Promise<number> {
  return page.evaluate(() => document.querySelector('video')?.currentTime ?? 0)
}

async function paused(page: Page): Promise<boolean> {
  return page.evaluate(() => document.querySelector('video')?.paused ?? true)
}

async function rate(page: Page): Promise<number> {
  return page.evaluate(() => document.querySelector('video')?.playbackRate ?? 0)
}

// storedProgress reads what the page saved for this browser.
async function storedProgress(
  page: Page,
): Promise<Record<string, { position: number; paused: boolean; rate: number }>> {
  return page.evaluate(() =>
    JSON.parse(window.localStorage.getItem('openduanju.watch-progress') ?? '{}'),
  )
}

// The player uses the native controls, so the test drives the media element
// itself: play() and pause() raise the same events the page listens to.
async function playVideo(page: Page) {
  await page.evaluate(() => {
    void document.querySelector('video')?.play()
  })
}

async function pauseVideo(page: Page) {
  await page.evaluate(() => document.querySelector('video')?.pause())
}

// Without the generated sample the player has nothing to play: the run is
// skipped instead of reported as a pass.
test.beforeEach(() => {
  test.skip(!videoPath || !existsSync(videoPath), 'ffmpeg is required to generate the sample video')
})

test('continues the watched episode and its position in this browser', async ({ page }) => {
  const requested: string[] = []
  await routePublic(page, requested)
  await page.goto('/series/1')
  // Without progress the player opens the first episode.
  await expect(page.getByRole('button', { name: '第 1 集' })).toHaveAttribute(
    'aria-current',
    'true',
  )

  await page.getByRole('button', { name: '第 5 集' }).click()
  await expect(page.getByRole('button', { name: '第 5 集' })).toHaveAttribute(
    'aria-current',
    'true',
  )
  await expect(page.locator('video')).toBeVisible()
  await page.locator('select').selectOption('1.5')
  await playVideo(page)
  await page.waitForFunction(() => (document.querySelector('video')?.currentTime ?? 0) > 2.5)
  await pauseVideo(page)
  // The pause is persisted by the page; wait for that record instead of
  // reading it in the same tick.
  await expect
    .poll(async () => {
      const record = (await storedProgress(page))['1:105']
      return record ? record.paused && record.position > 2.5 : false
    })
    .toBe(true)
  const saved = (await storedProgress(page))['1:105'].position
  expect(saved).toBeGreaterThan(2.5)
  expect(await paused(page)).toBe(true)

  // Re-entering the series without a query continues the same episode.
  await page.goto('/series/1')
  await expect(page.getByRole('button', { name: '第 5 集' })).toHaveAttribute(
    'aria-current',
    'true',
  )
  await page.waitForFunction(
    (expected) => Math.abs((document.querySelector('video')?.currentTime ?? 0) - expected) < 1,
    saved,
  )
  expect(await paused(page)).toBe(true)
  expect(await rate(page)).toBe(1.5)
  expect(await position(page)).toBeCloseTo(saved, 0)

  // Continuing from the home page opens the same episode too.
  await page.goto('/')
  // The continue row names the episode and the position it resumes.
  const resumeLink = page.getByRole('link', { name: /看到第 5 集/ })
  await expect(resumeLink).toBeVisible()
  await resumeLink.click()
  await expect(page).toHaveURL(/episode=105/)
  await expect(page.getByRole('button', { name: '第 5 集' })).toHaveAttribute(
    'aria-current',
    'true',
  )
  await page.waitForFunction(
    (expected) => Math.abs((document.querySelector('video')?.currentTime ?? 0) - expected) < 1,
    saved,
  )
  // Coming back from the history list also keeps the paused state and the
  // speed; a seek is not a reason to start playing.
  expect(await paused(page)).toBe(true)
  expect(await rate(page)).toBe(1.5)
})

test('does not move a stored position onto another episode', async ({ page }) => {
  const requested: string[] = []
  const published = { episodes }
  await routePublic(page, requested, published)
  await page.goto('/series/1')
  await page.getByRole('button', { name: '第 5 集' }).click()
  await page.locator('select').selectOption('2')
  await playVideo(page)
  await page.waitForFunction(() => (document.querySelector('video')?.currentTime ?? 0) > 1.5)
  await pauseVideo(page)
  // Wait for the page to save the position it has to ignore later.
  await expect
    .poll(async () => {
      const record = (await storedProgress(page))['1:105']
      return record ? record.position > 1.5 : false
    })
    .toBe(true)

  // The published revision no longer holds that episode: the page reports it
  // and starts at the first episode instead of applying the old position.
  published.episodes = episodes.slice(0, 2)
  await page.goto('/series/1')
  await expect(page.getByText('上次观看的分集已不在当前发布版本中', { exact: false })).toBeVisible()
  await expect(page.getByRole('button', { name: '第 1 集' })).toHaveAttribute(
    'aria-current',
    'true',
  )
  // The position of the removed episode is not applied here.
  await expect.poll(() => position(page)).toBeLessThan(1)
})
