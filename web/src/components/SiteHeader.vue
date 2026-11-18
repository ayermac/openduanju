<script setup lang="ts">
import { RouterLink } from 'vue-router'

// Chrome only. Search lives in the home canvas, the same place a visitor looks first.
defineProps<{ back?: boolean }>()
</script>

<template>
  <div class="chrome">
    <aside class="rail">
      <RouterLink class="logo" to="/" aria-label="映刻首页">
        <span class="mark" aria-hidden="true">映</span>
        <span class="brand">
          <span class="name">映刻</span>
          <small>已发布短剧</small>
        </span>
      </RouterLink>
      <nav class="rail-nav" aria-label="主导航">
        <RouterLink class="rail-link" to="/">首页</RouterLink>
        <a class="rail-link" href="/#continue">继续观看</a>
      </nav>
      <p class="rail-note">免费观看已审核发布的内容。进度只留在这台设备。</p>
    </aside>

    <header class="top">
      <RouterLink v-if="back" class="back" to="/" aria-label="返回首页">← 首页</RouterLink>
      <RouterLink v-else class="logo" to="/" aria-label="映刻首页">
        <span class="mark" aria-hidden="true">映</span>
        <span class="name">映刻</span>
      </RouterLink>
      <span class="head-note">已发布内容免费观看</span>
    </header>

    <nav class="dock" aria-label="底部导航">
      <RouterLink to="/">首页</RouterLink>
      <a href="/#continue">历史</a>
    </nav>
  </div>
</template>

<style scoped>
.rail {
  position: fixed;
  inset: 0 auto 0 0;
  z-index: 30;
  width: var(--side-w);
  display: flex;
  flex-direction: column;
  gap: var(--sp-5);
  padding: var(--sp-5) var(--sp-4);
  background: #16171c;
  border-right: 1px solid var(--border);
}
.logo {
  display: inline-flex;
  align-items: center;
  gap: var(--sp-3);
  min-height: var(--touch);
}
.mark {
  width: 32px;
  height: 32px;
  border-radius: 10px;
  display: grid;
  place-items: center;
  color: var(--brand-ink);
  font-size: 14px;
  font-weight: 800;
  background: var(--brand);
}
.brand {
  display: flex;
  flex-direction: column;
  line-height: 1.15;
}
.name {
  font-size: var(--fs-16);
  font-weight: 700;
}
.logo small,
.rail-note,
.head-note {
  color: var(--ink-400);
  font-size: var(--fs-12);
  font-weight: 500;
}
.rail-nav {
  display: flex;
  flex-direction: column;
  gap: var(--sp-2);
}
.rail-link {
  display: flex;
  align-items: center;
  min-height: var(--touch);
  padding: 0 var(--sp-3);
  border-radius: var(--r-md);
  color: var(--ink-700);
  background: #ffffff0d;
}
.rail-link.router-link-exact-active {
  background: var(--brand);
  color: var(--brand-ink);
  font-weight: 700;
}
.rail-note {
  margin-top: auto;
  line-height: 1.5;
}
.top,
.dock {
  display: none;
}

@media (max-width: 900px) {
  .rail {
    display: none;
  }
  .top {
    position: sticky;
    top: 0;
    z-index: 20;
    display: flex;
    align-items: center;
    gap: var(--sp-3);
    height: var(--head-h);
    padding: 0 var(--sp-4);
    background: #111214e6;
    backdrop-filter: blur(16px);
    border-bottom: 1px solid var(--border);
  }
  .head-note {
    margin-left: auto;
  }
  .back {
    display: inline-flex;
    align-items: center;
    min-height: var(--touch);
    padding: 0 var(--sp-3);
    border-radius: var(--r-pill);
    background: #ffffff12;
    color: var(--ink-700);
  }
  .dock {
    position: fixed;
    left: 12px;
    right: 12px;
    bottom: 10px;
    z-index: 30;
    display: grid;
    grid-template-columns: 1fr 1fr;
    height: var(--dock-h);
    padding: 6px;
    border-radius: 18px;
    background: #111214f0;
    border: 1px solid var(--border);
    box-shadow: var(--shadow-3);
  }
  .dock a {
    display: grid;
    place-items: center;
    border-radius: 14px;
    color: var(--ink-600);
    font-size: var(--fs-13);
    font-weight: 600;
  }
  .dock a.router-link-exact-active {
    background: var(--brand);
    color: var(--brand-ink);
  }
}
</style>
