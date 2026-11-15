<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  ElAlert,
  ElButton,
  ElCard,
  ElInput,
  ElOption,
  ElPagination,
  ElSelect,
  ElTable,
  ElTableColumn,
  ElTag,
} from 'element-plus'
import {
  ApiError,
  AUDIT_ACTIONS,
  AUDIT_RESOURCE_TYPES,
  listAuditEvents,
  type AuditEvent,
} from '@/api/audit'

const events = ref<AuditEvent[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const loading = ref(false)
// A failed query is shown as a failure. The page must never answer an
// unreadable trail with an empty table.
const listError = ref('')

const resourceType = ref('')
const action = ref('')
const resourceId = ref('')
const actorName = ref('')
const traceId = ref('')

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

function actionLabel(value: string): string {
  return AUDIT_ACTIONS.find((entry) => entry.value === value)?.label ?? value
}

function resourceLabel(value: string): string {
  return AUDIT_RESOURCE_TYPES.find((entry) => entry.value === value)?.label ?? value
}

async function load(reset = false) {
  if (reset) page.value = 1
  loading.value = true
  listError.value = ''
  try {
    const set = await listAuditEvents({
      page: page.value,
      pageSize,
      resourceType: resourceType.value || undefined,
      action: action.value || undefined,
      resourceId: resourceId.value.trim() || undefined,
      actorName: actorName.value.trim() || undefined,
      traceId: traceId.value.trim() || undefined,
    })
    events.value = set.items ?? []
    total.value = set.total
  } catch (error) {
    // The old rows are cleared: showing them next to an error would let a
    // stale page pass for the answer of this query.
    events.value = []
    total.value = 0
    listError.value = describeError(error)
  } finally {
    loading.value = false
  }
}

function changePage(next: number) {
  page.value = next
  void load()
}

onMounted(() => {
  void load()
})
</script>

<template>
  <main class="media">
    <section class="heading">
      <div>
        <h1>审计记录</h1>
        <p>
          每次关键写入都留下一条记录：操作人、动作、对象与版本、结果与失败码、时间，以及该请求的报障编号。成功记录与业务变更在同一事务提交，业务失败不产生成功记录。
        </p>
      </div>
      <SessionBar />
    </section>

    <ElCard shadow="never" class="panel">
      <template #header>
        <div class="panel-title">
          <strong>01 筛选</strong>
          <span>对象、动作、操作人与报障编号可组合；翻页顺序由服务端固定</span>
        </div>
      </template>
      <div class="toolbar">
        <ElSelect v-model="resourceType" placeholder="全部对象" size="small" clearable>
          <ElOption
            v-for="entry in AUDIT_RESOURCE_TYPES"
            :key="entry.value"
            :label="entry.label"
            :value="entry.value"
          />
        </ElSelect>
        <ElSelect v-model="action" placeholder="全部动作" size="small" clearable>
          <ElOption
            v-for="entry in AUDIT_ACTIONS"
            :key="entry.value"
            :label="entry.label"
            :value="entry.value"
          />
        </ElSelect>
        <ElInput v-model="resourceId" placeholder="对象 ID" size="small" clearable />
        <ElInput v-model="actorName" placeholder="操作人" size="small" clearable />
        <ElInput v-model="traceId" placeholder="报障编号（X-Trace-Id）" size="small" clearable />
        <ElButton size="small" type="primary" :loading="loading" @click="load(true)">
          查询
        </ElButton>
        <span class="hint">共 {{ total }} 条</span>
      </div>
    </ElCard>

    <ElCard shadow="never" class="panel">
      <template #header>
        <div class="panel-title">
          <strong>02 记录</strong>
          <span>失败记录保留稳定失败码，不保存原始错误、请求载荷或凭据</span>
        </div>
      </template>
      <ElAlert v-if="listError" type="error" :closable="false" :title="listError" show-icon />
      <ElTable
        v-else
        :data="events"
        empty-text="暂无审计记录"
        :row-key="(row: AuditEvent) => String(row.id)"
      >
        <ElTableColumn label="时间" width="170">
          <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
        </ElTableColumn>
        <ElTableColumn label="结果" width="90">
          <template #default="{ row }">
            <ElTag size="small" :type="row.result === 'success' ? 'success' : 'danger'">
              {{ row.result === 'success' ? '成功' : '失败' }}
            </ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn label="动作" min-width="130">
          <template #default="{ row }">
            <div>{{ actionLabel(row.action) }}</div>
            <div class="hint">{{ row.action }}</div>
          </template>
        </ElTableColumn>
        <ElTableColumn label="对象" min-width="150">
          <template #default="{ row }">
            <div>{{ resourceLabel(row.resource_type) }}</div>
            <div class="hint">
              {{ row.resource_type }} #{{ row.resource_id || '—' }} · v{{ row.resource_version }}
            </div>
          </template>
        </ElTableColumn>
        <ElTableColumn label="操作人" width="130">
          <template #default="{ row }">
            <div>{{ row.actor_name || '—' }}</div>
            <div class="hint">{{ row.actor_id || '—' }}</div>
          </template>
        </ElTableColumn>
        <ElTableColumn label="失败码" min-width="120">
          <template #default="{ row }">
            <span class="hint">{{ row.reason || '—' }}</span>
          </template>
        </ElTableColumn>
        <ElTableColumn label="报障编号" min-width="220">
          <template #default="{ row }">
            <span class="trace">{{ row.trace_id || '—' }}</span>
          </template>
        </ElTableColumn>
      </ElTable>
      <div class="pager">
        <ElPagination
          layout="prev, pager, next"
          :current-page="page"
          :page-size="pageSize"
          :total="total"
          :disabled="loading"
          @current-change="changePage"
        />
      </div>
    </ElCard>
  </main>
</template>

<style scoped>
.hint {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}

.trace {
  font-family: var(--el-font-family-mono, monospace);
  font-size: 12px;
  word-break: break-all;
}

.pager {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}
</style>
