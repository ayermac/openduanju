import { createRouter, createWebHistory } from 'vue-router'
import HomeView from '../views/HomeView.vue'
import { currentSession, loadSession } from '@/api/session'

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    {
      path: '/login',
      name: 'login',
      component: () => import('../views/LoginView.vue'),
      meta: { public: true, title: '登录' },
    },
    { path: '/', name: 'home', component: HomeView, meta: { title: '工作台' } },
    {
      path: '/content',
      name: 'content',
      component: () => import('../views/ContentView.vue'),
      meta: { title: '内容管理' },
    },
    {
      path: '/sources',
      name: 'source-config',
      component: () => import('../views/SourceConfigView.vue'),
      // The reviewer may read the configuration but not change it.
      meta: { roles: ['admin', 'operator', 'reviewer'], title: '来源与通道' },
    },
    {
      path: '/source',
      name: 'source-catalog',
      component: () => import('../views/SourceCatalogView.vue'),
      meta: { roles: ['admin', 'operator'], title: '采集与候选' },
    },
    {
      path: '/media',
      name: 'media-batches',
      component: () => import('../views/MediaBatchView.vue'),
      meta: { roles: ['admin', 'operator'], title: '素材与批次' },
    },
    {
      path: '/uploads',
      name: 'manual-uploads',
      component: () => import('../views/UploadView.vue'),
      meta: { roles: ['admin', 'operator'], title: '人工上传' },
    },
    {
      path: '/authorizations',
      name: 'authorizations',
      component: () => import('../views/AuthorizationView.vue'),
      meta: { roles: ['admin', 'operator', 'reviewer'], title: '授权依据' },
    },
    {
      path: '/reviews',
      name: 'reviews',
      component: () => import('../views/ReviewView.vue'),
      // Operators publish from this screen, so they may open it read-only.
      meta: { roles: ['admin', 'operator', 'reviewer'], title: '审核' },
    },
    {
      path: '/audit',
      name: 'audit',
      component: () => import('../views/AuditView.vue'),
      // The trail holds identities and actions, never a payload; every
      // signed-in role may read it. The server decides the same thing.
      meta: { roles: ['admin', 'operator', 'reviewer'], title: '审计记录' },
    },
    {
      path: '/staff',
      name: 'staff',
      component: () => import('../views/StaffView.vue'),
      meta: { roles: ['admin'], title: '账号' },
    },
    {
      path: '/forbidden',
      name: 'forbidden',
      component: () => import('../views/ForbiddenView.vue'),
      meta: { title: '无权限' },
    },
  ],
})

// The guard only decides what to render. Every call is authorised again on
// the server, so a hidden button is never the control.
router.beforeEach(async (to) => {
  if (to.meta.public) return true
  const session = currentSession() ?? (await loadSession())
  if (!session) {
    return { name: 'login', query: { next: to.fullPath } }
  }
  const roles = to.meta.roles as string[] | undefined
  if (roles && !roles.includes(session.staff.role)) {
    return { name: 'forbidden' }
  }
  return true
})

// The browser tab names the page, not the scaffold.
router.afterEach((to) => {
  const title = typeof to.meta.title === 'string' ? to.meta.title : ''
  document.title = title ? `${title} · 映刻运营后台` : '映刻运营后台'
})

export default router
