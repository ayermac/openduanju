<script setup lang="ts">
import type { EpisodeCard } from '@/api/web'

// The selection grid tells three things apart: the episode playing now, the
// episodes that can be played, and the ones whose material is not ready (the
// public projection leaves the asset empty). A switch in flight shows on the
// episode that was asked for, not on the one still playing.
const props = defineProps<{
  episodes: EpisodeCard[]
  currentId: string
  loadingId: string
}>()
defineEmits<{ select: [episode: EpisodeCard] }>()

function unavailable(episode: EpisodeCard): boolean {
  return !episode.asset_id
}
</script>

<template>
  <ul class="grid" aria-label="选集">
    <li v-for="episode in props.episodes" :key="episode.episode_id">
      <button
        type="button"
        class="ep"
        :class="{ loading: loadingId === episode.episode_id }"
        :aria-label="`第 ${episode.number} 集`"
        :aria-current="currentId === episode.episode_id ? 'true' : undefined"
        :disabled="unavailable(episode)"
        :title="unavailable(episode) ? '该集暂不可播放' : `第 ${episode.number} 集`"
        @click="$emit('select', episode)"
      >
        <span>{{ episode.number }}</span>
      </button>
    </li>
  </ul>
</template>

<style scoped>
.grid {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: var(--sp-2);
}
.ep {
  width: 100%;
  min-height: 36px;
  border-radius: var(--r-sm);
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--ink-700);
  font-size: var(--fs-13);
  font-variant-numeric: tabular-nums;
}
.ep:hover:not(:disabled) {
  border-color: var(--ink-300);
}
.ep[aria-current='true'] {
  border-color: var(--brand);
  background: var(--brand-weak);
  color: var(--brand-strong);
  font-weight: 600;
}
.ep:disabled {
  color: var(--ink-300);
  background: var(--surface-2);
  cursor: not-allowed;
}
.ep.loading {
  position: relative;
  color: transparent;
}
.ep.loading::after {
  content: '';
  position: absolute;
  inset: 0;
  margin: auto;
  width: 12px;
  height: 12px;
  border-radius: 50%;
  border: 2px solid var(--border-strong);
  border-top-color: var(--brand);
  animation: spin 0.8s linear infinite;
}
@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

@media (max-width: 720px) {
  .grid {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }
  /* 触控目标至少 44px；图标与字号保持不变 */
  .ep {
    min-height: var(--touch);
  }
}
@media (prefers-reduced-motion: reduce) {
  .ep.loading::after {
    animation: none;
    border-top-color: var(--brand);
  }
}
</style>
