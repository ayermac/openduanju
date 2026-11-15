<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { currentSession, roleLabel, type StaffRole } from '@/api/session'

// The workbench names the four stages an operator moves through and the entry
// for each one. It shows no counts and no charts: there is no metric endpoint,
// and a number invented here would be worse than none.
const session = currentSession()
const role = (session?.staff.role ?? 'reviewer') as StaffRole

const stages = computed(() => {
  const writable = role === 'admin' || role === 'operator'
  return [
    {
      step: '01',
      title: '来源与本地候选',
      body: '配置来源与通道，同步元数据到本地候选，选出要做的作品。',
      links: writable
        ? [
            { label: '采集与候选', to: '/source' },
            { label: '来源与通道', to: '/sources' },
          ]
        : [{ label: '查看来源与通道', to: '/sources' }],
    },
    {
      step: '02',
      title: '内容与素材',
      body: writable
        ? '创建作品、编辑草稿与选集，上传或处理每集素材，全部就绪后才能送审。'
        : '查看已创建的作品与它们的素材进度。',
      links: writable
        ? [
            { label: '内容管理', to: '/content' },
            { label: '素材与批次', to: '/media' },
            { label: '人工上传', to: '/uploads' },
          ]
        : [{ label: '内容管理', to: '/content' }],
    },
    {
      step: '03',
      title: '审核与发布',
      body: writable
        ? '认领待审修订，逐项确认后通过或驳回；通过后人工发布，发布后可下架。'
        : '认领待审修订、逐项确认并给出结论。',
      links: [{ label: '审核队列', to: '/reviews' }],
    },
    {
      step: '04',
      title: '授权与追溯',
      body: '维护作品授权依据；每次关键写入都能按对象、动作与报障编号回查。',
      links: [
        { label: '授权依据', to: '/authorizations' },
        { label: '审计记录', to: '/audit' },
      ],
    },
  ]
})
</script>

<template>
  <main class="page">
    <div class="page-head">
      <div>
        <h1>运营工作台</h1>
        <p class="desc">
          当前角色：{{ roleLabel(role) }}。左侧按流程顺序排列，每一步都能直接进入。
        </p>
      </div>
    </div>

    <section class="stages" aria-label="流程入口">
      <article v-for="stage in stages" :key="stage.step" class="stage">
        <span class="step">{{ stage.step }}</span>
        <h2>{{ stage.title }}</h2>
        <p>{{ stage.body }}</p>
        <div class="links">
          <RouterLink v-for="link in stage.links" :key="link.to" class="link" :to="link.to">
            {{ link.label }} <span aria-hidden="true">→</span>
          </RouterLink>
        </div>
      </article>
    </section>

    <section class="panel">
      <div class="panel-body boundary">
        <h2>本期边界</h2>
        <p>
          采集、内容、素材、审核、发布、审计均可用；真实 SDK 取源与真实 COS 仍待外部条件，
          当前部署使用本地存储与受控解析。
        </p>
      </div>
    </section>
  </main>
</template>

<style scoped>
.stages {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: var(--sp-4);
}
.stage {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--r-lg);
  padding: var(--sp-4);
  display: flex;
  flex-direction: column;
  gap: var(--sp-2);
}
.step {
  font-size: var(--fs-11);
  letter-spacing: 2px;
  color: var(--ink-400);
}
.stage h2 {
  font-size: var(--fs-16);
}
.stage p {
  color: var(--ink-600);
  font-size: var(--fs-13);
  flex: 1;
}
.links {
  display: flex;
  flex-wrap: wrap;
  gap: var(--sp-2);
  margin-top: var(--sp-2);
}
.link {
  display: inline-flex;
  align-items: center;
  gap: var(--sp-1);
  height: 30px;
  padding: 0 var(--sp-3);
  border-radius: var(--r-sm);
  background: var(--brand-weak);
  color: var(--brand-strong);
  font-size: var(--fs-13);
}
.link:hover {
  background: #fbdfe4;
}
.boundary h2 {
  font-size: var(--fs-14);
  margin-bottom: var(--sp-1);
}
.boundary p {
  color: var(--ink-600);
  font-size: var(--fs-13);
}
</style>
