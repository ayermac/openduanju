<script setup lang="ts">
import { ElButton, ElTag } from 'element-plus'
import { useRouter } from 'vue-router'
import { currentSession, logout, roleLabel } from '@/api/session'

const router = useRouter()
const session = currentSession()

async function signOut() {
  await logout()
  await router.replace({ name: 'login' })
}
</script>

<template>
  <div class="session-bar">
    <span class="who">{{
      session?.staff.display_name || session?.staff.username || '未登录'
    }}</span>
    <ElTag v-if="session" size="small" type="info">{{ roleLabel(session.staff.role) }}</ElTag>
    <ElButton v-if="session" link size="small" @click="signOut">退出</ElButton>
  </div>
</template>

<style scoped>
.session-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
}
.who {
  color: var(--el-text-color-regular);
}
</style>
