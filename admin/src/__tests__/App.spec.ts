import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import App from '../App.vue'
import HomeView from '../views/HomeView.vue'

async function mountAt(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: HomeView, meta: { title: '工作台' } },
      { path: '/login', component: { template: '<p>登录页</p>' }, meta: { public: true } },
    ],
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(App, { global: { plugins: [router] } })
  return wrapper
}

describe('console shell and workbench', () => {
  it('names the flow and the boundary of this release', async () => {
    const wrapper = await mountAt('/')
    expect(wrapper.get('h1').text()).toBe('运营工作台')
    expect(wrapper.text()).toContain('来源与本地候选')
    expect(wrapper.text()).toContain('审核与发布')
    // The console states what is delivered and what still waits outside.
    expect(wrapper.text()).toContain('真实 SDK 取源与真实 COS 仍待外部条件')
  })

  it('keeps one navigation and shows who is signed in', async () => {
    const wrapper = await mountAt('/')
    const nav = wrapper.get('nav')
    expect(nav.text()).toContain('内容管理')
    expect(nav.text()).toContain('审计记录')
    // Without a loaded session the shell still names the state instead of
    // showing an empty corner.
    expect(wrapper.text()).toContain('未登录')
    expect(wrapper.get('button.logout').text()).toBe('退出')
  })

  it('does not wrap the login page in the console shell', async () => {
    const wrapper = await mountAt('/login')
    expect(wrapper.find('nav').exists()).toBe(false)
    expect(wrapper.text()).toContain('登录页')
  })
})
