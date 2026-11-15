import { createRouter, createWebHistory } from 'vue-router'
import HomeView from '../views/HomeView.vue'

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    { path: '/', name: 'home', component: HomeView, meta: { title: '映刻 · 已发布短剧' } },
    {
      path: '/series/:id',
      name: 'series',
      component: () => import('../views/SeriesView.vue'),
      // The series page replaces this with the title it loaded.
      meta: { title: '作品 · 映刻' },
    },
    {
      path: '/:pathMatch(.*)*',
      name: 'not-found',
      component: HomeView,
      meta: { title: '映刻 · 已发布短剧' },
    },
  ],
  scrollBehavior(_to, _from, saved) {
    return saved ?? { top: 0 }
  },
})

// The tab names the page, not the scaffolding.
router.afterEach((to) => {
  const title = to.meta.title
  if (typeof title === 'string') document.title = title
})

export default router
