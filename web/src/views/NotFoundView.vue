<script setup lang="ts">
import { RouterLink } from 'vue-router'
</script>

<template>
  <main class="notfound">
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
  font-size: var(--fs-24);
  font-weight: 700;
}
p {
  color: var(--ink-600);
  font-size: var(--fs-14);
}
.back {
  display: inline-block;
  padding: 10px 16px;
  border-radius: var(--r-pill);
  background: var(--brand-weak);
  color: var(--brand-strong);
  text-decoration: none;
}
</style>
