<script setup lang="ts">
import { computed } from 'vue'
import { RouterView, useRoute } from 'vue-router'
import AppShell from '@/components/AppShell.vue'

// The console pages share one shell. The login page and the forbidden page
// stand alone: they have no session or no navigation to show.
const route = useRoute()
const bare = computed(() => route.meta.public === true || route.name === 'forbidden')
</script>

<template>
  <RouterView v-if="bare" />
  <AppShell v-else />
</template>
