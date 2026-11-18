<script setup lang="ts">
import { computed } from 'vue'

// One panel for the four states a list can be in. The page decides which one is
// true; the panel never shows several at once.
type Variant = 'loading' | 'empty' | 'no-result' | 'error'

const props = withDefaults(
  defineProps<{ variant: Variant; title?: string; hint?: string; error?: string }>(),
  { title: '', hint: '', error: '' },
)
defineEmits<{ retry: []; clear: [] }>()

const glyph = computed(() => {
  switch (props.variant) {
    case 'empty':
      return '▦'
    case 'no-result':
      return '⌕'
    case 'error':
      return '!'
    default:
      return '…'
  }
})
</script>

<template>
  <div v-if="variant === 'loading'" class="state" role="status" aria-live="polite">
    <div class="skeletons" aria-hidden="true">
      <span v-for="index in 4" :key="index" class="skeleton" />
    </div>
    <p class="sr">加载中</p>
    <span class="visually-hidden">加载中</span>
  </div>
  <div v-else class="state" :class="variant" :role="variant === 'error' ? 'alert' : undefined">
    <span class="glyph" aria-hidden="true">{{ glyph }}</span>
    <h3>{{ title }}</h3>
    <p v-if="hint">{{ hint }}</p>
    <p v-if="variant === 'error' && error" class="detail">{{ error }}</p>
    <div class="actions">
      <slot name="actions" />
      <button v-if="variant === 'error'" class="btn primary" type="button" @click="$emit('retry')">
        重新加载
      </button>
      <button v-if="variant === 'no-result'" class="btn" type="button" @click="$emit('clear')">
        清空筛选
      </button>
    </div>
  </div>
</template>

<style scoped>
.state {
  padding: var(--sp-7) var(--sp-4);
  text-align: center;
  color: var(--ink-600);
}
.glyph {
  width: 48px;
  height: 48px;
  margin: 0 auto var(--sp-3);
  border-radius: 14px;
  display: grid;
  place-items: center;
  background: var(--surface-3);
  color: var(--ink-400);
  font-size: 20px;
}
h3 {
  font-size: var(--fs-16);
  color: var(--ink-900);
  margin-bottom: var(--sp-1);
}
p {
  font-size: var(--fs-13);
}
.detail {
  margin-top: var(--sp-1);
  color: var(--ink-400);
  font-size: var(--fs-12);
}
.actions {
  margin-top: var(--sp-4);
  display: flex;
  gap: var(--sp-2);
  justify-content: center;
}
.btn {
  height: 38px;
  padding: 0 var(--sp-4);
  border-radius: var(--r-md);
  border: 1px solid var(--border-strong);
  background: var(--surface);
  color: var(--ink-900);
  font-size: var(--fs-14);
}
.btn.primary {
  background: var(--brand);
  border-color: var(--brand);
  color: var(--brand-ink);
  font-weight: 700;
}
.skeletons {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
  gap: var(--sp-4);
}
.skeleton {
  aspect-ratio: 2 / 3;
  border-radius: var(--r-md);
  background: linear-gradient(90deg, var(--surface-3) 25%, #2a2d34 37%, var(--surface-3) 63%);
  background-size: 400% 100%;
  animation: shimmer 1.2s ease infinite;
}
.sr {
  margin-top: var(--sp-3);
  font-size: var(--fs-12);
}
@keyframes shimmer {
  0% {
    background-position: 100% 0;
  }
  100% {
    background-position: 0 0;
  }
}
</style>
