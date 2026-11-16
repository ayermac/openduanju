import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import App from '../App.vue'
import HomeView from '../views/HomeView.vue'

function json(body: unknown, status = 200) {
  return Promise.resolve({
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
  } as Response)
}

function stubPublicApi(series: unknown[], categories: string[] = []) {
  vi.stubGlobal(
    'fetch',
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/api/web/v1/categories')) return json({ categories })
      if (url.includes('/api/web/v1/series')) {
        return json({ total: series.length, page: 1, page_size: 24, items: series })
      }
      return json({ code: 404, reason: 'NOT_FOUND', message: '没有这个接口' }, 404)
    }),
  )
}

async function mountHome() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: HomeView },
      { path: '/series/:id', component: { template: '<div />' } },
    ],
  })
  await router.push('/')
  await router.isReady()
  const wrapper = mount(App, { global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}

afterEach(() => {
  vi.unstubAllGlobals()
  window.localStorage.clear()
})

describe('public home', () => {
  it('says the catalogue is empty instead of inventing content', async () => {
    stubPublicApi([])
    const wrapper = await mountHome()
    expect(wrapper.text()).toContain('最近发布')
    expect(wrapper.text()).toContain('还没有已发布的作品')
    // No category with no content behind it.
    expect(wrapper.find('nav.tabs').exists()).toBe(false)
  })

  it('shows the published episode count and the category of a card', async () => {
    stubPublicApi(
      [
        {
          series_id: '9007199254740993',
          title: '样例作品',
          description: '简介',
          cover_url: '',
          episode_count: 12,
          category: '都市',
          published_at: '2026-11-15T00:00:00Z',
        },
      ],
      ['都市'],
    )
    const wrapper = await mountHome()
    const card = wrapper.get('a.poster')
    // 64-bit identifiers stay strings and reach the detail route unchanged.
    expect(card.attributes('href')).toBe('/series/9007199254740993')
    expect(card.text()).toContain('样例作品')
    // Only the published episode count is known, so the badge says so.
    expect(card.text()).toContain('12 集可看')
    expect(card.text()).toContain('都市')
    expect(wrapper.get('nav.tabs').text()).toContain('都市')
  })

  it('reports a failed load and offers a retry', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => json({ code: 500, reason: 'INTERNAL', message: '服务异常' }, 500)),
    )
    const wrapper = await mountHome()
    expect(wrapper.text()).toContain('内容暂时取不到')
    expect(wrapper.get('button.btn.primary').text()).toBe('重新加载')
  })

  it('formats stored playback positions as seconds', async () => {
    const positions = [0, 13, 60, 65, 3600]
    const records = Object.fromEntries(
      positions.map((position, index) => [
        `${index + 1}:10`,
        {
          seriesId: String(index + 1),
          seriesTitle: `作品 ${index + 1}`,
          episodeId: '10',
          episodeNumber: 1,
          position,
          paused: true,
          rate: 1,
          updatedAt: index + 1,
        },
      ]),
    )
    window.localStorage.setItem('openduanju.watch-progress', JSON.stringify(records))
    stubPublicApi([])
    const wrapper = await mountHome()
    expect(wrapper.findAll('.row .sub').map((item) => item.text())).toEqual([
      '看到第 1 集 · 已看 1:00:00',
      '看到第 1 集 · 已看 1:05',
      '看到第 1 集 · 已看 1:00',
      '看到第 1 集 · 已看 0:13',
      '看到第 1 集 · 已看 0:00',
    ])
  })
})
