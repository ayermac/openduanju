<script setup lang="ts">
import { RouterLink } from 'vue-router'
import SiteHeader from '@/components/SiteHeader.vue'
</script>

<template>
  <SiteHeader />
  <main class="notfound with-rail">
    <h1>页面不存在</h1>
    <p>请确认地址，或返回首页。</p>
    <RouterLink class="back" to="/">返回首页</RouterLink>
  </main>
</template>

<style scoped>
.notfound {
  min-height: 60vh;
  display: grid;
  place-content: center;
  text-align: center;
  gap: 10px;
  padding: var(--sp-6);
}
h1 {
  font-size: var(--fs-28);
  font-weight: 700;
}
p {
  color: var(--ink-600);
  font-size: var(--fs-14);
}
.back {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: var(--touch);
  margin-top: var(--sp-2);
  padding: 0 var(--sp-5);
  border-radius: var(--r-pill);
  background: var(--brand);
  color: var(--brand-ink);
  font-weight: 700;
  text-decoration: none;
}
</style>
