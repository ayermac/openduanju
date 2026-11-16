<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  ElAlert,
  ElButton,
  ElCard,
  ElDialog,
  ElInput,
  ElOption,
  ElPagination,
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
const page = ref(1)
const pageSize = ref(20)
const loading = ref(false)
const error = ref('')
const roleFilter = ref('')

// Dangerous account actions are confirmed against the exact account. The
// dialog is not a substitute for the server-side version and permission checks.
const confirmAction = ref<{ target: Staff; nextEnabled: boolean } | null>(null)
const roleAction = ref<{ target: Staff; next: StaffRole } | null>(null)
const resetAction = ref<Staff | null>(null)
const newPassword = ref('')
const resetBusy = ref(false)

const username = ref('')
const displayName = ref('')
const role = ref<StaffRole>('operator')
const password = ref('')
const creating = ref(false)
const formError = ref('')

function describeError(err: unknown): string {
  return err instanceof ApiError ? `${err.reason}：${err.message}` : String(err)
}

async function load(resetPage = false) {
  if (resetPage) page.value = 1
  loading.value = true
  error.value = ''
  try {
    const set = await listStaff({
      page: page.value,
      pageSize: pageSize.value,
      role: roleFilter.value || undefined,
    })
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

async function confirmToggle() {
  if (!confirmAction.value) return
  const target = confirmAction.value.target
  const enabled = target.enabled === true
  try {
    await updateStaff(target.id, target.version, { enabled: !enabled })
    ElMessage.success(enabled ? '账号已停用，其会话已撤销' : '账号已启用')
    confirmAction.value = null
    await load()
  } catch (err) {
    confirmAction.value = null
    error.value = describeError(err)
  }
}

async function confirmReset() {
  if (!resetAction.value) return
  const secret = newPassword.value
  if (secret.length < 12) {
    error.value = '新密码至少12位'
    return
  }
  resetBusy.value = true
  try {
    await resetPassword(resetAction.value.id, resetAction.value.version, secret)
    ElMessage.success('密码已重置，旧会话已失效')
    resetAction.value = null
    newPassword.value = ''
    await load()
  } catch (err) {
    error.value = describeError(err)
  } finally {
    resetBusy.value = false
  }
}

async function confirmRole() {
  if (!roleAction.value) return
  const target = roleAction.value.target
  try {
    await updateStaff(target.id, target.version, { role: roleAction.value.next })
    ElMessage.success('角色已更新')
    roleAction.value = null
    await load()
  } catch (err) {
    roleAction.value = null
    error.value = describeError(err)
  }
}

function changePage(next: number) {
  page.value = next
  void load()
}

function changePageSize(next: number) {
  pageSize.value = next
  void load(true)
}

onMounted(() => {
  void load()
})
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
        <ElSelect
          v-model="roleFilter"
          placeholder="全部角色"
          clearable
          size="small"
          @change="load(true)"
        >
          <ElOption label="管理员" value="admin" />
          <ElOption label="运营" value="operator" />
          <ElOption label="审核员" value="reviewer" />
        </ElSelect>
        <ElButton size="small" :loading="loading" @click="load(true)">查询</ElButton>
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
              @change="roleAction = { target: row as Staff, next: String($event) as StaffRole }"
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
            <ElButton
              size="small"
              @click="confirmAction = { target: row as Staff, nextEnabled: row.enabled !== true }"
              >{{ row.enabled === true ? '停用' : '启用' }}</ElButton
            >
            <ElButton size="small" @click="resetAction = row as Staff">重置密码</ElButton>
          </template>
        </ElTableColumn>
      </ElTable>
      <div class="pager">
        <ElPagination
          layout="prev, pager, next, sizes"
          :current-page="page"
          :page-size="pageSize"
          :page-sizes="[10, 20, 50]"
          :total="total"
          :disabled="loading"
          @current-change="changePage"
          @size-change="changePageSize"
        />
      </div>
    </ElCard>

    <ElDialog
      :model-value="Boolean(confirmAction)"
      :title="
        confirmAction
          ? `${confirmAction.target.enabled === true ? '停用账号' : '启用账号'} ${confirmAction.target.username}`
          : ''
      "
      width="460px"
      @update:model-value="confirmAction = null"
    >
      <template v-if="confirmAction">
        <p>
          对象：<strong>{{ confirmAction.target.username }}</strong
          >（ID #{{ confirmAction.target.id }}，版本 v{{ confirmAction.target.version }}）。
        </p>
        <p v-if="confirmAction.target.enabled === true">该账号已有会话会立即失效。</p>
        <p v-else>启用后该账号可重新登录，权限仍由服务端判定。</p>
      </template>
      <template #footer>
        <ElButton @click="confirmAction = null">取消</ElButton>
        <ElButton type="primary" :loading="loading" @click="confirmToggle">{{
          confirmAction?.target.enabled === true ? '确认停用' : '确认启用'
        }}</ElButton>
      </template>
    </ElDialog>

    <ElDialog
      :model-value="Boolean(roleAction)"
      title="修改角色"
      width="460px"
      @update:model-value="roleAction = null"
    >
      <p v-if="roleAction">
        将 <strong>{{ roleAction.target.username }}</strong> 从 {{ roleAction.target.role }} 改为
        {{ roleAction.next }}；新角色在确认后生效。
      </p>
      <template #footer>
        <ElButton @click="roleAction = null">取消</ElButton>
        <ElButton type="primary" :loading="loading" @click="confirmRole">确认修改</ElButton>
      </template>
    </ElDialog>

    <ElDialog
      :model-value="Boolean(resetAction)"
      :title="resetAction ? `重置 ${resetAction.username} 的密码` : ''"
      width="460px"
      @update:model-value="resetAction = null"
    >
      <label class="field">
        新密码
        <ElInput
          v-model="newPassword"
          type="password"
          show-password
          autocomplete="new-password"
          aria-label="新密码"
        />
      </label>
      <p class="hint">至少12位；确认后该账号旧会话立即失效。</p>
      <template #footer>
        <ElButton @click="resetAction = null">取消</ElButton>
        <ElButton type="primary" :loading="resetBusy" @click="confirmReset">确认重置</ElButton>
      </template>
    </ElDialog>
  </main>
</template>

<style scoped>
.hint {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.pager {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}
.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 10px;
}
</style>
