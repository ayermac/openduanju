<script setup lang="ts">
import { ref, watch } from 'vue'
import { RouterLink } from 'vue-router'

// The header owns the search field; the page decides what a search means.
const props = defineProps<{ keyword?: string; back?: boolean }>()
const emit = defineEmits<{ search: [value: string] }>()
const value = ref(props.keyword ?? '')

watch(
  () => props.keyword,
  (next) => {
    if ((next ?? '') !== value.value) value.value = next ?? ''
  },
)

function submit() {
  emit('search', value.value.trim())
}
</script>

<template>
  <header class="site-head">
    <div class="wrap inner">
      <RouterLink v-if="back" class="back" to="/" aria-label="返回首页">← 首页</RouterLink>
      <RouterLink v-else class="logo" to="/" aria-label="映刻首页">
        <span class="mark" aria-hidden="true">映</span>
        <span class="name">映刻</span>
        <small>OpenDuanju</small>
      </RouterLink>
      <form v-if="!back" class="search" role="search" @submit.prevent="submit">
        <label class="visually-hidden" for="site-search">搜索片名或简介</label>
        <input
          id="site-search"
          v-model="value"
          type="search"
          placeholder="搜索片名或简介"
          autocomplete="off"
          @keyup.enter="submit"
        />
        <button type="submit">搜索</button>
      </form>
      <div v-else class="title-holder" />
      <span class="head-note">已发布内容免费观看</span>
    </div>
  </header>
</template>

<style scoped>
.site-head {
  position: sticky;
  top: 0;
  z-index: 20;
  background: rgba(255, 255, 255, 0.94);
  backdrop-filter: saturate(160%) blur(12px);
  border-bottom: 1px solid var(--border);
}
.inner {
  display: flex;
  align-items: center;
  gap: var(--sp-5);
  height: var(--head-h);
}
.logo {
  display: inline-flex;
  align-items: center;
  gap: var(--sp-2);
  font-size: var(--fs-18);
  font-weight: 700;
  letter-spacing: 0.5px;
  white-space: nowrap;
}
.mark {
  width: 26px;
  height: 26px;
  border-radius: 8px;
  display: grid;
  place-items: center;
  color: #fff;
  font-size: 13px;
  font-weight: 800;
  background: linear-gradient(135deg, var(--brand), #ff7a5c);
}
.logo small {
  color: var(--ink-400);
  font-size: var(--fs-11);
  font-weight: 500;
  letter-spacing: 1px;
}
.back {
  display: inline-flex;
  align-items: center;
  height: 36px;
  padding: 0 var(--sp-3);
  border-radius: var(--r-md);
  color: var(--ink-600);
  font-size: var(--fs-14);
  white-space: nowrap;
}
.back:hover {
  background: var(--surface-2);
  color: var(--ink-900);
}
.search {
  display: flex;
  flex: 1;
  max-width: 520px;
}
.search input {
  flex: 1;
  min-width: 0;
  height: 40px;
  padding: 0 var(--sp-4);
  border: 1px solid var(--border-strong);
  border-right: 0;
  border-radius: var(--r-pill) 0 0 var(--r-pill);
  background: var(--surface-2);
  color: var(--ink-900);
  font-size: var(--fs-14);
}
.search input::placeholder {
  color: var(--ink-400);
}
.search input:focus {
  background: var(--surface);
}
.search button {
  height: 40px;
  padding: 0 var(--sp-5);
  border: 0;
  border-radius: 0 var(--r-pill) var(--r-pill) 0;
  background: var(--brand);
  color: #fff;
  font-weight: 600;
}
.search button:hover {
  background: var(--brand-strong);
}
.title-holder {
  flex: 1;
}
.head-note {
  margin-left: auto;
  color: var(--ink-600);
  font-size: var(--fs-13);
  white-space: nowrap;
}

@media (max-width: 720px) {
  .inner {
    height: 56px;
    gap: var(--sp-3);
  }
  .logo small,
  .head-note {
    display: none;
  }
  .search {
    max-width: none;
  }
  .search button {
    padding: 0 var(--sp-4);
  }
}
</style>
