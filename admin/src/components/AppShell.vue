<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { currentSession, logout, roleLabel, type StaffRole } from '@/api/session'

// One shell for every console page: the module list, the current user and the
// way out. A page never repeats its own copy of this navigation.
interface NavItem {
  label: string
  to: string
  roles: StaffRole[]
  group: string
}

const items: NavItem[] = [
  { label: '工作台', to: '/', roles: ['admin', 'operator', 'reviewer'], group: '总览' },
  { label: '采集与候选', to: '/source', roles: ['admin', 'operator'], group: '内容' },
  { label: '内容管理', to: '/content', roles: ['admin', 'operator', 'reviewer'], group: '内容' },
  { label: '素材与批次', to: '/media', roles: ['admin', 'operator'], group: '内容' },
  { label: '人工上传', to: '/uploads', roles: ['admin', 'operator'], group: '内容' },
  { label: '审核', to: '/reviews', roles: ['admin', 'operator', 'reviewer'], group: '发布' },
  {
    label: '授权依据',
    to: '/authorizations',
    roles: ['admin', 'operator', 'reviewer'],
    group: '发布',
  },
  { label: '审计记录', to: '/audit', roles: ['admin', 'operator', 'reviewer'], group: '系统' },
  { label: '账号', to: '/staff', roles: ['admin'], group: '系统' },
  { label: '来源与通道', to: '/sources', roles: ['admin', 'operator', 'reviewer'], group: '系统' },
]

const route = useRoute()
const router = useRouter()
const session = ref(currentSession())
const role = computed(() => (session.value?.staff.role ?? 'reviewer') as StaffRole)

const visible = computed(() => items.filter((item) => item.roles.includes(role.value)))
const groups = computed(() => {
  const out: { name: string; items: NavItem[] }[] = []
  for (const item of visible.value) {
    const last = out[out.length - 1]
    if (last && last.name === item.group) last.items.push(item)
    else out.push({ name: item.group, items: [item] })
  }
  return out
})
const current = computed(() => items.find((item) => item.to === route.path))
const who = computed(
  () => session.value?.staff.display_name || session.value?.staff.username || '未登录',
)

// The guard resolves the session before a navigation completes, so re-reading
// here keeps the shell in step with it.
watch(
  () => route.fullPath,
  () => {
    session.value = currentSession()
  },
)

async function signOut() {
  await logout()
  await router.replace({ name: 'login' })
}
</script>

<template>
  <div class="shell">
    <nav class="side" aria-label="主导航">
      <RouterLink class="brand" to="/">
        <span class="mark" aria-hidden="true">映</span>
        <span>映刻</span>
        <small>运营后台</small>
      </RouterLink>
      <template v-for="group in groups" :key="group.name">
        <span class="group">{{ group.name }}</span>
        <RouterLink
          v-for="item in group.items"
          :key="item.to"
          :to="item.to"
          :aria-current="current?.to === item.to ? 'page' : undefined"
        >
          {{ item.label }}
        </RouterLink>
      </template>
    </nav>
    <div class="main">
      <header class="top">
        <span class="crumb">{{ current?.label ?? '映刻运营后台' }}</span>
        <div class="who">
          <span>{{ who }}</span>
          <span class="role">{{ roleLabel(role) }}</span>
          <button class="logout" type="button" @click="signOut">退出</button>
        </div>
      </header>
      <RouterView />
    </div>
  </div>
</template>

<style scoped>
.shell {
  display: grid;
  grid-template-columns: 232px minmax(0, 1fr);
  min-height: 100vh;
}
.side {
  background: #14161c;
  color: #c9cfda;
  padding: var(--sp-3) var(--sp-2);
  display: flex;
  flex-direction: column;
  gap: 2px;
  position: sticky;
  top: 0;
  align-self: start;
  height: 100vh;
  overflow-y: auto;
}
.brand {
  display: flex;
  align-items: center;
  gap: var(--sp-2);
  padding: var(--sp-2) var(--sp-3) var(--sp-4);
  color: #fff;
  font-weight: 700;
  font-size: var(--fs-16);
}
.brand small {
  margin-left: auto;
  color: #6b7486;
  font-size: var(--fs-11);
  font-weight: 500;
}
.mark {
  width: 24px;
  height: 24px;
  border-radius: 7px;
  display: grid;
  place-items: center;
  font-size: 12px;
  font-weight: 800;
  color: #fff;
  background: linear-gradient(135deg, var(--brand), #ff7a5c);
}
.group {
  padding: var(--sp-3) var(--sp-3) var(--sp-1);
  color: #6b7486;
  font-size: var(--fs-11);
  letter-spacing: 1px;
}
.side a:not(.brand) {
  padding: var(--sp-2) var(--sp-3);
  border-radius: var(--r-sm);
  color: #c9cfda;
  font-size: var(--fs-13);
  display: block;
}
.side a:not(.brand):hover {
  background: rgba(255, 255, 255, 0.06);
  color: #fff;
}
.side a[aria-current='page'] {
  background: rgba(226, 58, 78, 0.16);
  color: #fff;
  box-shadow: inset 2px 0 0 var(--brand);
}
.main {
  min-width: 0;
  display: flex;
  flex-direction: column;
}
.top {
  height: 60px;
  background: var(--surface);
  border-bottom: 1px solid var(--border);
  display: flex;
  align-items: center;
  gap: var(--sp-4);
  padding: 0 var(--sp-5);
  position: sticky;
  top: 0;
  z-index: 10;
}
.crumb {
  color: var(--ink-400);
  font-size: var(--fs-13);
}
.who {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: var(--sp-2);
  font-size: var(--fs-13);
  color: var(--ink-600);
}
.role {
  padding: 1px var(--sp-2);
  border-radius: var(--r-sm);
  background: var(--surface-3);
  font-size: var(--fs-12);
}
.logout {
  height: 30px;
  padding: 0 var(--sp-3);
  border: 1px solid var(--border);
  border-radius: var(--r-sm);
  background: var(--surface);
  color: var(--ink-600);
  font-size: var(--fs-13);
}
.logout:hover {
  border-color: var(--border-strong);
  color: var(--ink-900);
}

@media (max-width: 900px) {
  .shell {
    grid-template-columns: 1fr;
  }
  .side {
    height: auto;
    position: static;
    flex-direction: row;
    overflow-x: auto;
    gap: var(--sp-1);
    padding: var(--sp-2);
  }
  .brand,
  .group {
    display: none;
  }
  .side a:not(.brand) {
    white-space: nowrap;
  }
  .top {
    padding: 0 var(--sp-4);
  }
  .crumb {
    flex: 1;
  }
}
</style>
