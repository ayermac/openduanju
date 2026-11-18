<script setup lang="ts">
import { RouterLink } from 'vue-router'
import type { WatchProgress } from '@/stores/history'

// The device history has no total duration, so the row states the episode and
// the position reached, not a percentage of an unknown whole.
defineProps<{ items: WatchProgress[] }>()
defineEmits<{ (event: 'remove', seriesId: string): void }>()

function clock(positionSeconds: number): string {
  const totalSeconds = Math.max(0, Math.floor(positionSeconds))
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  const seconds = totalSeconds % 60
  const minuteText = String(minutes).padStart(hours ? 2 : 1, '0')
  const secondText = String(seconds).padStart(2, '0')
  return hours ? `${hours}:${minuteText}:${secondText}` : `${minuteText}:${secondText}`
}
</script>

<template>
  <div class="row">
    <div v-for="item in items" :key="`${item.seriesId}:${item.episodeId}`" class="card">
      <RouterLink
        class="card-link"
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
      <button
        class="remove"
        type="button"
        :aria-label="`删除 ${item.seriesTitle || '未命名作品'} 的观看记录`"
        @click="$emit('remove', item.seriesId)"
      >
        删除
      </button>
    </div>
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
  flex: 0 0 320px;
  scroll-snap-align: start;
  display: flex;
  align-items: stretch;
  background: #ffffff10;
  border: 1px solid var(--border);
  border-radius: var(--r-lg);
  min-height: 76px;
}
.card-link {
  display: flex;
  align-items: center;
  gap: var(--sp-3);
  flex: 1;
  min-width: 0;
  padding: var(--sp-2) var(--sp-2) var(--sp-2) var(--sp-2);
  min-height: 76px;
}
.remove {
  flex: none;
  width: var(--touch);
  min-height: var(--touch);
  margin: 8px 8px 8px 0;
  border-radius: var(--r-md);
  border: 1px solid var(--border-strong);
  background: #111214;
  color: var(--ink-700);
  font-size: var(--fs-12);
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
    radial-gradient(120% 90% at 20% 10%, rgba(255, 255, 255, 0.22), transparent 60%), #12343a;
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
  background: var(--brand);
  color: var(--brand-ink);
  font-size: var(--fs-12);
  font-weight: 700;
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
