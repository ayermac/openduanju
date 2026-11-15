<script setup lang="ts">
import { ElButton, ElResult } from 'element-plus'
import { useRouter } from 'vue-router'
import { currentSession, roleLabel } from '@/api/session'

const router = useRouter()
const session = currentSession()
</script>

<template>
  <main class="forbidden">
    <ElResult
      icon="warning"
      title="当前角色无权访问"
      :sub-title="`你是${session ? roleLabel(session.staff.role) : '未登录用户'}，该页面需要更高权限。接口同样会拒绝越权请求。`"
    >
      <template #extra>
        <ElButton type="primary" @click="router.replace('/')">返回工作台</ElButton>
      </template>
    </ElResult>
  </main>
</template>

<style scoped>
.forbidden {
  min-height: 100vh;
  display: grid;
  place-items: center;
}
</style>
