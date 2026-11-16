<script setup lang="ts">
import { RouterLink } from 'vue-router'
</script>

<template>
  <main class="media">
    <ElCard shadow="never" class="panel">
      <h1 class="heading">页面不存在</h1>
      <p class="hint">请检查地址，或返回运营工作台。</p>
      <RouterLink class="back" to="/">返回工作台</RouterLink>
    </ElCard>
  </main>
</template>

<style scoped>
.heading {
  margin: 0 0 8px;
  font-size: var(--fs-20);
  font-weight: 700;
}
.back {
  display: inline-block;
  padding: 8px 14px;
  border-radius: var(--r-pill);
  background: var(--brand-weak);
  color: var(--brand-strong);
  text-decoration: none;
}
</style>
