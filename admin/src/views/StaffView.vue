<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  ElAlert,
  ElButton,
  ElCard,
  ElInput,
  ElOption,
  ElSelect,
  ElTable,
  ElTableColumn,
  ElTag,
  ElMessage,
} from 'element-plus'
import {
  ApiError,
  createStaff,
  listStaff,
  resetPassword,
  updateStaff,
  type Staff,
  type StaffRole,
} from '@/api/session'

const staff = ref<Staff[]>([])
const total = ref(0)
const loading = ref(false)
const error = ref('')
const roleFilter = ref('')

const username = ref('')
const displayName = ref('')
const role = ref<StaffRole>('operator')
const password = ref('')
const creating = ref(false)
const formError = ref('')

function describeError(err: unknown): string {
  return err instanceof ApiError ? `${err.reason}：${err.message}` : String(err)
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const set = await listStaff({ role: roleFilter.value || undefined })
    staff.value = set.items ?? []
    total.value = set.total
  } catch (err) {
    error.value = describeError(err)
  } finally {
    loading.value = false
  }
}

async function submit() {
  formError.value = ''
  if (!username.value.trim() || !role.value) {
    formError.value = '用户名与角色是必填项'
    return
  }
  creating.value = true
  try {
    await createStaff({
      username: username.value.trim(),
      displayName: displayName.value.trim(),
      role: role.value,
      password: password.value,
    })
    ElMessage.success('账号已创建')
    username.value = ''
    displayName.value = ''
    password.value = ''
    await load()
  } catch (err) {
    formError.value = describeError(err)
  } finally {
    creating.value = false
  }
}

async function toggle(row: unknown) {
  const target = row as Staff
  const enabled = target.enabled === true
  try {
    await updateStaff(target.id, target.version, { enabled: !enabled })
    ElMessage.success(enabled ? '账号已停用，其会话已撤销' : '账号已启用')
    await load()
  } catch (err) {
    error.value = describeError(err)
  }
}

async function changeRole(row: unknown, next: string) {
  const target = row as Staff
  try {
    await updateStaff(target.id, target.version, { role: next as StaffRole })
    ElMessage.success('角色已更新')
    await load()
  } catch (err) {
    error.value = describeError(err)
  }
}

async function reset(row: unknown) {
  const target = row as Staff
  const secret = window.prompt(`为 ${target.username} 设置新密码（至少12位，旧会话会立即失效）`)
  if (!secret) return
  try {
    await resetPassword(target.id, target.version, secret)
    ElMessage.success('密码已重置，旧会话已失效')
    await load()
  } catch (err) {
    error.value = describeError(err)
  }
}

onMounted(load)
</script>

<template>
  <main class="media">
    <section class="heading">
      <div>
        <h1>人员管理</h1>
        <p>
          管理员创建、停用账号并重置密码；停用或重置后该账号的会话立即失效。角色边界由服务端判定。
        </p>
      </div>
      <SessionBar />
    </section>

    <ElCard shadow="never" class="panel">
      <template #header>
        <div class="panel-title">
          <strong>01 新增账号</strong>
          <span>密码至少12位；系统没有默认密码</span>
        </div>
      </template>
      <ElAlert v-if="formError" type="error" :closable="false" :title="formError" show-icon />
      <div class="submit-row">
        <label>用户名<ElInput v-model="username" placeholder="zhangsan" /></label>
        <label>显示名<ElInput v-model="displayName" placeholder="张三" /></label>
        <label>
          角色
          <ElSelect v-model="role" class="channel">
            <ElOption label="管理员" value="admin" />
            <ElOption label="运营" value="operator" />
            <ElOption label="审核员" value="reviewer" />
          </ElSelect>
        </label>
        <label>初始密码<ElInput v-model="password" type="password" show-password /></label>
        <ElButton type="primary" :loading="creating" @click="submit">创建账号</ElButton>
      </div>
    </ElCard>

    <ElCard shadow="never" class="panel">
      <template #header>
        <div class="panel-title">
          <strong>02 账号列表</strong>
          <span>停用后旧会话立即失效</span>
        </div>
      </template>
      <ElAlert v-if="error" type="error" :closable="false" :title="error" show-icon />
      <div class="toolbar">
        <ElSelect v-model="roleFilter" placeholder="全部角色" clearable size="small">
          <ElOption label="管理员" value="admin" />
          <ElOption label="运营" value="operator" />
          <ElOption label="审核员" value="reviewer" />
        </ElSelect>
        <ElButton size="small" :loading="loading" @click="load">查询</ElButton>
        <span class="hint">共 {{ total }} 个账号</span>
      </div>
      <ElTable :data="staff" empty-text="暂无账号">
        <ElTableColumn prop="id" label="ID" width="70" />
        <ElTableColumn prop="username" label="用户名" min-width="140" />
        <ElTableColumn prop="display_name" label="显示名" min-width="140" />
        <ElTableColumn label="角色" width="150">
          <template #default="{ row }">
            <ElSelect
              :model-value="row.role"
              size="small"
              @change="changeRole(row, String($event))"
            >
              <ElOption label="管理员" value="admin" />
              <ElOption label="运营" value="operator" />
              <ElOption label="审核员" value="reviewer" />
            </ElSelect>
          </template>
        </ElTableColumn>
        <ElTableColumn label="状态" width="100">
          <template #default="{ row }">
            <ElTag size="small" :type="row.enabled === true ? 'success' : 'danger'">
              {{ row.enabled === true ? '启用' : '停用' }}
            </ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn prop="version" label="版本" width="70" />
        <ElTableColumn label="操作" width="220">
          <template #default="{ row }">
            <ElButton size="small" @click="toggle(row)">{{
              row.enabled === true ? '停用' : '启用'
            }}</ElButton>
            <ElButton size="small" @click="reset(row)">重置密码</ElButton>
          </template>
        </ElTableColumn>
      </ElTable>
    </ElCard>
  </main>
</template>

<style scoped>
.hint {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
</style>
