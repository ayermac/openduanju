<script setup lang="ts">
import { ref } from 'vue'
import {
  ElAlert,
  ElButton,
  ElCard,
  ElCheckbox,
  ElDatePicker,
  ElInput,
  ElInputNumber,
  ElMessage,
  ElSelect,
  ElOption,
  ElTable,
  ElTableColumn,
  ElTag,
} from 'element-plus'
import {
  ApiError,
  createGrant,
  listGrants,
  revokeGrant,
  type AuthorizationGrant,
} from '@/api/authorization'

const seriesId = ref<number | null>(null)
const reference = ref('')
const allowProcess = ref(false)
const allowDisplay = ref(false)
const allowPlayback = ref(false)
const validFrom = ref<Date | null>(new Date())
const validUntil = ref<Date | null>(null)
const creating = ref(false)
const formError = ref('')

const grants = ref<AuthorizationGrant[]>([])
const total = ref(0)
const listSeries = ref<number | null>(null)
const statusFilter = ref('')
const listError = ref('')
const loading = ref(false)

function describeError(error: unknown): string {
  if (error instanceof ApiError) return `${error.reason}：${error.message}`
  return error instanceof Error ? error.message : String(error)
}

function formatTime(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString('zh-CN', { hour12: false })
}

function permissionLabel(value: boolean): string {
  return value ? '允许' : '不允许'
}

async function submit() {
  formError.value = ''
  if (!seriesId.value || !reference.value.trim()) {
    formError.value = '作品 ID 与授权材料编号都是必填项'
    return
  }
  if (!validFrom.value) {
    formError.value = '生效时间是必填项'
    return
  }
  if (validUntil.value && validUntil.value <= validFrom.value) {
    formError.value = '结束时间必须晚于生效时间'
    return
  }
  creating.value = true
  try {
    await createGrant(seriesId.value, {
      reference: reference.value.trim(),
      allowProcess: allowProcess.value,
      allowDisplay: allowDisplay.value,
      allowPlayback: allowPlayback.value,
      validFrom: validFrom.value.toISOString(),
      validUntil: validUntil.value ? validUntil.value.toISOString() : undefined,
    })
    ElMessage.success('授权依据已创建')
    reference.value = ''
    allowProcess.value = false
    allowDisplay.value = false
    allowPlayback.value = false
    await load()
  } catch (error) {
    formError.value = describeError(error)
  } finally {
    creating.value = false
  }
}

async function load() {
  loading.value = true
  listError.value = ''
  try {
    const set = await listGrants(listSeries.value ?? undefined, statusFilter.value || undefined)
    grants.value = set.items ?? []
    total.value = set.total
  } catch (error) {
    listError.value = describeError(error)
  } finally {
    loading.value = false
  }
}

async function revoke(row: unknown) {
  const grant = row as AuthorizationGrant
  const reason = window.prompt(`撤销依据 #${grant.id} 的原因（必填，将写入审计）`)?.trim()
  if (!reason) return
  try {
    await revokeGrant(grant.id, grant.version, reason)
    ElMessage.success('授权依据已撤销')
    await load()
  } catch (error) {
    listError.value = describeError(error)
  }
}
</script>

<template>
  <main class="media">
    <section class="heading">
      <div>
        <h1>授权依据</h1>
        <p>
          作品级授权，用途位默认关闭。依据创建后不可原地修改：修正用途或期限请新增一条；旧依据只能撤销。提交批次必须显式选择依据与版本。
        </p>
      </div>
      <SessionBar />
    </section>

    <ElCard shadow="never" class="panel">
      <template #header>
        <div class="panel-title">
          <strong>01 新增依据</strong>
          <span>结束时间为空表示系统未设置结束时间，不等于永久授权</span>
        </div>
      </template>
      <ElAlert v-if="formError" type="error" :closable="false" :title="formError" show-icon />
      <div class="submit-row">
        <label>作品 ID<ElInputNumber v-model="seriesId" :min="1" :controls="false" /></label>
        <label>材料编号<ElInput v-model="reference" placeholder="AUTH-2026-001" /></label>
        <label
          >生效时间<ElDatePicker v-model="validFrom" type="datetime" placeholder="UTC 生效"
        /></label>
        <label
          >结束时间<ElDatePicker v-model="validUntil" type="datetime" placeholder="可留空"
        /></label>
      </div>
      <div class="submit-row">
        <ElCheckbox v-model="allowProcess">允许处理（获取/下载/处理/存储）</ElCheckbox>
        <ElCheckbox v-model="allowDisplay">允许展示（元数据/封面）</ElCheckbox>
        <ElCheckbox v-model="allowPlayback">允许播放（签发播放凭证）</ElCheckbox>
        <ElButton type="primary" :loading="creating" @click="submit">创建依据</ElButton>
      </div>
    </ElCard>

    <ElCard shadow="never" class="panel">
      <template #header>
        <div class="panel-title">
          <strong>02 依据列表</strong>
          <span>撤销使用版本 CAS；重复撤销同一版本幂等</span>
        </div>
      </template>
      <ElAlert v-if="listError" type="error" :closable="false" :title="listError" show-icon />
      <div class="toolbar">
        <ElInputNumber
          v-model="listSeries"
          :min="1"
          :controls="false"
          placeholder="作品 ID"
          size="small"
        />
        <ElSelect v-model="statusFilter" placeholder="全部状态" size="small" clearable>
          <ElOption label="生效中" value="active" />
          <ElOption label="已撤销" value="revoked" />
        </ElSelect>
        <ElButton size="small" :loading="loading" @click="load">查询</ElButton>
        <span class="hint">共 {{ total }} 条</span>
      </div>
      <ElTable :data="grants" empty-text="暂无授权依据">
        <ElTableColumn prop="id" label="ID" width="70" />
        <ElTableColumn prop="series_id" label="作品" width="80" />
        <ElTableColumn prop="reference" label="材料编号" min-width="150" />
        <ElTableColumn label="用途" min-width="220">
          <template #default="{ row }">
            <ElTag size="small" :type="row.allow_process ? 'success' : 'info'"
              >处理 {{ permissionLabel(row.allow_process) }}</ElTag
            >
            <ElTag size="small" :type="row.allow_display ? 'success' : 'info'"
              >展示 {{ permissionLabel(row.allow_display) }}</ElTag
            >
            <ElTag size="small" :type="row.allow_playback ? 'success' : 'info'"
              >播放 {{ permissionLabel(row.allow_playback) }}</ElTag
            >
          </template>
        </ElTableColumn>
        <ElTableColumn label="有效期（UTC）" min-width="210">
          <template #default="{ row }">
            {{ formatTime(row.valid_from) }} →
            {{ row.valid_until ? formatTime(row.valid_until) : '未设置结束时间' }}
          </template>
        </ElTableColumn>
        <ElTableColumn label="状态" width="100">
          <template #default="{ row }">
            <ElTag size="small" :type="row.status === 'active' ? 'success' : 'danger'">
              {{ row.status === 'active' ? '生效中' : '已撤销' }}
            </ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn prop="version" label="版本" width="70" />
        <ElTableColumn label="确认 / 撤销" min-width="180">
          <template #default="{ row }">
            <div class="hint">{{ row.confirmed_by }} · {{ formatTime(row.confirmed_at) }}</div>
            <div v-if="row.status === 'revoked'" class="hint">
              {{ row.revoked_by }} · {{ formatTime(row.revoked_at) }} · {{ row.revoke_reason }}
            </div>
          </template>
        </ElTableColumn>
        <ElTableColumn label="操作" width="100">
          <template #default="{ row }">
            <ElButton size="small" :disabled="row.status === 'revoked'" @click="revoke(row)"
              >撤销</ElButton
            >
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
