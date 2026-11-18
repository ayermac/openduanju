<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import type { SeriesCard } from '@/api/web'

// A published series as a poster. The public card carries a title, a
// description, a cover, a category and the number of published episodes; there
// is no ongoing/completed flag and no popularity, so the badge only states how
// many episodes can be watched.
const props = defineProps<{ series: SeriesCard }>()
const failed = ref(false)

const cover = computed(() => (failed.value ? '' : props.series.cover_url))
const episodes = computed(() =>
  props.series.episode_count > 0 ? `${props.series.episode_count} 集可看` : '暂无分集',
)
</script>

<template>
  <RouterLink class="poster" :to="`/series/${series.series_id}`">
    <span class="frame">
      <img
        v-if="cover"
        :src="cover"
        :alt="`${series.title} 封面`"
        loading="lazy"
        @error="failed = true"
      />
      <span v-else class="fallback" aria-hidden="true">{{ series.title }}</span>
      <span class="badge">{{ episodes }}</span>
    </span>
    <span class="body">
      <span class="title">{{ series.title }}</span>
      <span v-if="series.category" class="meta">{{ series.category }}</span>
    </span>
  </RouterLink>
</template>

<style scoped>
.poster {
  display: block;
  color: var(--ink-900);
  border-radius: var(--r-md);
  overflow: hidden;
  background: transparent;
  transition: transform 160ms ease;
}
.poster:hover,
.poster:focus-visible {
  transform: translateY(-3px);
}
.frame {
  display: block;
  position: relative;
  aspect-ratio: 3 / 4;
  border-radius: var(--r-md);
  overflow: hidden;
  background: var(--surface-3);
}
.frame img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.fallback {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  padding: var(--sp-4);
  color: #fff;
  text-align: center;
  font-size: var(--fs-13);
  font-weight: 600;
  line-height: 1.35;
  background:
    radial-gradient(120% 90% at 18% 8%, rgba(255, 255, 255, 0.18), transparent 62%), #3c4557;
}
.badge {
  position: absolute;
  left: var(--sp-2);
  top: var(--sp-2);
  padding: 2px var(--sp-2);
  border-radius: var(--r-sm);
  background: #111214cc;
  color: #fff;
  font-size: var(--fs-11);
}
.body {
  display: block;
  padding: var(--sp-2) 2px 0;
}
.title {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  font-size: var(--fs-14);
  font-weight: 600;
  line-height: 1.4;
}
.meta {
  display: block;
  margin-top: var(--sp-1);
  color: var(--ink-400);
  font-size: var(--fs-12);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
