<script setup lang="ts">
import { RouterLink } from 'vue-router'
import type { WatchProgress } from '@/stores/history'

// The device history has no total duration, so the row states the episode and
// the position reached, not a percentage of an unknown whole.
defineProps<{ items: WatchProgress[] }>()

function clock(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000))
  const minutes = Math.floor(total / 60)
  const seconds = total % 60
  return `${minutes}:${String(seconds).padStart(2, '0')}`
}
</script>

<template>
  <div class="row">
    <RouterLink
      v-for="item in items"
      :key="`${item.seriesId}:${item.episodeId}`"
      class="card"
      :to="{ path: `/series/${item.seriesId}`, query: { episode: item.episodeId } }"
    >
      <span class="index" aria-hidden="true">{{
        String(item.episodeNumber).padStart(2, '0')
      }}</span>
      <span class="meta">
        <span class="name">{{ item.seriesTitle || '未命名作品' }}</span>
        <span class="sub"
          >看到第 {{ item.episodeNumber }} 集 · 已看 {{ clock(item.position) }}</span
        >
      </span>
      <span class="go" aria-hidden="true">继续</span>
    </RouterLink>
  </div>
</template>

<style scoped>
.row {
  display: flex;
  gap: var(--sp-4);
  overflow-x: auto;
  padding-bottom: var(--sp-2);
  scroll-snap-type: x mandatory;
  scrollbar-width: thin;
}
.card {
  flex: 0 0 300px;
  scroll-snap-align: start;
  display: flex;
  align-items: center;
  gap: var(--sp-3);
  padding: var(--sp-2) var(--sp-3) var(--sp-2) var(--sp-2);
  background: var(--surface);
  border-radius: var(--r-lg);
  box-shadow: var(--shadow-1);
  min-height: 76px;
}
.card:hover {
  box-shadow: var(--shadow-2);
}
.index {
  flex: none;
  width: 52px;
  height: 60px;
  border-radius: var(--r-sm);
  display: grid;
  place-items: center;
  color: #fff;
  font-size: var(--fs-18);
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  background:
    radial-gradient(120% 90% at 20% 10%, rgba(255, 255, 255, 0.2), transparent 60%), #41506b;
}
.meta {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.name {
  font-size: var(--fs-14);
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.sub {
  color: var(--ink-600);
  font-size: var(--fs-12);
}
.go {
  margin-left: auto;
  padding: 2px var(--sp-2);
  border-radius: var(--r-pill);
  background: var(--brand-weak);
  color: var(--brand-strong);
  font-size: var(--fs-12);
  white-space: nowrap;
}

@media (max-width: 720px) {
  .card {
    flex-basis: 260px;
  }
  .go {
    display: none;
  }
}
</style>
