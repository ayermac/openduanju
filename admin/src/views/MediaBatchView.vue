<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import {
  ElAlert,
  ElButton,
  ElCard,
  ElDialog,
  ElInput,
  ElInputNumber,
  ElMessage,
  ElOption,
  ElPagination,
  ElSelect,
  ElTable,
  ElTableColumn,
  ElTag,
} from 'element-plus'
import {
  ApiError,
  cancelMediaBatch,
  getMediaBatch,
  listMediaBatches,
  listMediaItems,
  retryMediaBatch,
  submitMediaBatch,
  type MediaBatch,
  type MediaItem,
} from '@/api/media'

const batches = ref<MediaBatch[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(10)
const statusFilter = ref('')
const loading = ref(false)
const listError = ref('')

const route = useRoute()
const revisionId = ref<number | null>(null)
const revisionVersion = ref<number | null>(null)
const profileKey = ref('web-mp4-h264-aac-v1')
const inputKind = ref('sdk')
const authorizationGrantId = ref<number | null>(null)
const authorizationVersion = ref<number | null>(null)
const submitting = ref(false)
const submitError = ref('')

const detailVisible = ref(false)
const detailBatch = ref<MediaBatch | null>(null)
const items = ref<MediaItem[]>([])
const itemTotal = ref(0)
const itemPage = ref(1)
const itemPageSize = ref(20)
const itemStatus = ref('')
const loadingItems = ref(false)
const cancelReason = ref('')

const batchStatusLabel: Record<string, string> = {
  queued: '排队中',
  running: '处理中',
  waiting: '等待重试',
  partially_failed: '部分失败',
  failed: '失败',
  succeeded: '全部就绪',
  cancelling: '取消中',
  cancelled: '已取消',
  pending_review: '待审核',
}

const itemStatusLabel: Record<string, string> = {
  queued: '排队中',
  resolving: '解析源',
  acquiring: '获取文件',
  validating: '校验',
  processing: '转码',
  uploading: '上传',
  verifying: '复核对象',
  ready: '就绪',
  waiting_retry: '等待重试',
  failed: '失败',
  cancelled: '已取消',
}

const coverStatusLabel: Record<string, string> = {
  pending: '待上传',
  ready: '已就绪',
  failed: '失败',
}

let pollTimer: number | undefined

function describeError(error: unknown): string {
  if (error instanceof ApiError) return `${error.reason}：${error.message}`
  return error instanceof Error ? error.message : String(error)
}

function formatTime(value: string | undefined): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString('zh-CN', { hour12: false })
}

function formatBytes(value: number): string {
  if (!value) return '—'
  const units = ['B', 'KB', 'MB', 'GB']
  let size = value
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024
    unit += 1
  }
  return `${size.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`
}

function isActive(batch: MediaBatch): boolean {
  return ['queued', 'running', 'waiting', 'partially_failed'].includes(batch.status)
}

async function loadBatches(resetPage = false) {
  if (resetPage) page.value = 1
  loading.value = true
  listError.value = ''
  try {
    const data = await listMediaBatches(
      page.value,
      pageSize.value,
      undefined,
      undefined,
      statusFilter.value,
    )
    batches.value = data.items ?? []
    total.value = data.total ?? 0
    page.value = data.page ?? page.value
    pageSize.value = data.page_size ?? pageSize.value
    schedulePoll()
  } catch (error) {
    listError.value = describeError(error)
  } finally {
    loading.value = false
  }
}

function schedulePoll() {
  if (pollTimer) window.clearInterval(pollTimer)
  pollTimer = undefined
  if (!batches.value.some(isActive)) return
  pollTimer = window.setInterval(() => {
    void loadBatches()
  }, 3000)
}

async function submit() {
  if (!revisionId.value || !revisionVersion.value) {
    submitError.value = '修订 ID 与修订版本都是必填项'
    return
  }
  if (!authorizationGrantId.value || !authorizationVersion.value) {
    submitError.value = '必须选择授权依据并填写其版本；提交不会自动采用最新依据'
    return
  }
  submitting.value = true
  submitError.value = ''
  try {
    const batch = await submitMediaBatch(
      revisionId.value,
      revisionVersion.value,
      profileKey.value.trim(),
      inputKind.value,
      authorizationGrantId.value,
      authorizationVersion.value,
    )
    ElMessage.success(`已提交素材批次 #${batch.id}，处理在后台执行`)
    revisionId.value = null
    revisionVersion.value = null
    await loadBatches(true)
  } catch (error) {
    submitError.value = describeError(error)
  } finally {
    submitting.value = false
  }
}

function openRow(row: unknown) {
  void openBatch(row as MediaBatch)
}

async function openBatch(batch: MediaBatch) {
  listError.value = ''
  try {
    const detail = await getMediaBatch(batch.id)
    detailBatch.value = detail.batch
    items.value = detail.items ?? []
    itemTotal.value = detail.item_total ?? 0
    itemPage.value = 1
    detailVisible.value = true
  } catch (error) {
    listError.value = describeError(error)
  }
}

async function loadItems() {
  if (!detailBatch.value) return
  loadingItems.value = true
  try {
    const data = await listMediaItems(
      detailBatch.value.id,
      itemPage.value,
      itemPageSize.value,
      itemStatus.value,
    )
    items.value = data.items ?? []
    itemTotal.value = data.total ?? 0
  } catch (error) {
    listError.value = describeError(error)
  } finally {
    loadingItems.value = false
  }
}

function retryRow(row: unknown) {
  void retryBatch(row as MediaBatch)
}

function cancelRow(row: unknown) {
  void cancelBatch(row as MediaBatch)
}

async function retryBatch(batch: MediaBatch) {
  listError.value = ''
  try {
    await retryMediaBatch(batch.id, batch.version)
    ElMessage.success(`已重新排队批次 #${batch.id} 的可重试任务`)
    await loadBatches()
    if (detailBatch.value?.id === batch.id) await loadItems()
  } catch (error) {
    listError.value = describeError(error)
  }
}

async function cancelBatch(batch: MediaBatch) {
  listError.value = ''
  try {
    await cancelMediaBatch(batch.id, batch.version, cancelReason.value.trim() || '运营取消')
    ElMessage.info(`已请求取消批次 #${batch.id}，任务会在检查点停止`)
    cancelReason.value = ''
    await loadBatches()
    if (detailBatch.value?.id === batch.id) await loadItems()
  } catch (error) {
    listError.value = describeError(error)
  }
}

function filterItems() {
  itemPage.value = 1
  void loadItems()
}

onMounted(async () => {
  // The content page opens this screen with the draft it wants submitted, so
  // the operator does not copy identifiers between pages.
  const revision = Number(route.query.revision)
  const version = Number(route.query.version)
  if (Number.isInteger(revision) && revision > 0) revisionId.value = revision
  if (Number.isInteger(version) && version > 0) revisionVersion.value = version
  await loadBatches()
})

onBeforeUnmount(() => {
  if (pollTimer) window.clearInterval(pollTimer)
})
</script>

<template>
  <main class="media">
    <section class="heading">
      <div>
        <h1>素材批次</h1>
        <p>
          批次固定修订与选集，每个分集一个任务；全部就绪后才会创建唯一审核任务，当前切片尚未开放审核入口。
        </p>
      </div>
      <SessionBar />
    </section>

    <ElCard shadow="never" class="panel">
      <template #header>
        <div class="panel-title">
          <strong>01 提交素材批次</strong>
          <span>提交立即返回批次，处理在后台任务中执行</span>
        </div>
      </template>
      <ElAlert v-if="submitError" type="error" :closable="false" :title="submitError" show-icon />
      <div class="submit-row">
        <label>
          修订 ID
          <ElInputNumber v-model="revisionId" :min="1" size="small" placeholder="草稿修订" />
        </label>
        <label>
          修订版本
          <ElInputNumber v-model="revisionVersion" :min="1" size="small" placeholder="期望版本" />
        </label>
        <label>
          处理配置版本
          <ElInput v-model="profileKey" size="small" placeholder="web-mp4-h264-aac-v1" />
        </label>
        <label>
          输入来源
          <ElSelect v-model="inputKind" size="small" class="channel">
            <ElOption label="SDK 取源" value="sdk" />
            <ElOption label="人工上传" value="manual" />
          </ElSelect>
        </label>
        <label>
          授权依据 ID
          <ElInputNumber v-model="authorizationGrantId" :min="1" size="small" placeholder="必填" />
        </label>
        <label>
          授权版本
          <ElInputNumber
            v-model="authorizationVersion"
            :min="1"
            size="small"
            placeholder="expected"
          />
        </label>
        <ElButton type="primary" :loading="submitting" @click="submit">提交批次</ElButton>
      </div>
    </ElCard>

    <ElCard shadow="never" class="panel">
      <template #header>
        <div class="panel-title">
          <strong>02 批次列表</strong>
          <span>处理中的批次会自动刷新</span>
        </div>
      </template>
      <div class="toolbar">
        <ElSelect
          v-model="statusFilter"
          placeholder="全部状态"
          clearable
          class="channel"
          @change="loadBatches(true)"
        >
          <ElOption
            v-for="(label, key) in batchStatusLabel"
            :key="key"
            :label="label"
            :value="key"
          />
        </ElSelect>
        <ElButton :loading="loading" @click="loadBatches()">刷新</ElButton>
      </div>
      <ElAlert v-if="listError" type="error" :closable="false" :title="listError" show-icon />
      <ElTable v-loading="loading" :data="batches" empty-text="暂无素材批次" stripe>
        <ElTableColumn prop="id" label="批次" width="80" />
        <ElTableColumn prop="revision_id" label="修订" width="80" />
        <ElTableColumn label="状态" width="120">
          <template #default="{ row }">
            <ElTag
              :type="
                row.status === 'succeeded' || row.status === 'pending_review'
                  ? 'success'
                  : row.status === 'failed' || row.status === 'partially_failed'
                    ? 'danger'
                    : row.status === 'cancelled'
                      ? 'info'
                      : 'warning'
              "
              size="small"
            >
              {{ batchStatusLabel[row.status] ?? row.status }}
            </ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn label="进度" min-width="140">
          <template #default="{ row }">
            就绪 {{ row.item_ready }}/{{ row.item_total }} · 失败 {{ row.item_failed }}
          </template>
        </ElTableColumn>
        <ElTableColumn label="封面" width="100">
          <template #default="{ row }">
            {{ coverStatusLabel[row.cover_status] ?? row.cover_status }}
          </template>
        </ElTableColumn>
        <ElTableColumn label="审核任务" width="100">
          <template #default="{ row }">
            {{ row.review_task_id ? `#${row.review_task_id}` : '未创建' }}
          </template>
        </ElTableColumn>
        <ElTableColumn label="更新时间" width="170">
          <template #default="{ row }">{{ formatTime(row.updated_at) }}</template>
        </ElTableColumn>
        <ElTableColumn label="操作" width="220" fixed="right">
          <template #default="{ row }">
            <ElButton size="small" type="primary" plain @click="openRow(row)">查看</ElButton>
            <ElButton
              size="small"
              :disabled="
                !['failed', 'partially_failed', 'waiting', 'succeeded'].includes(row.status)
              "
              @click="retryRow(row)"
            >
              重试
            </ElButton>
            <ElButton
              size="small"
              :disabled="!['queued', 'running', 'waiting', 'partially_failed'].includes(row.status)"
              @click="cancelRow(row)"
            >
              取消
            </ElButton>
          </template>
        </ElTableColumn>
      </ElTable>
      <div class="pager">
        <span>共 {{ total }} 条</span>
        <ElPagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :total="total"
          :page-sizes="[10, 20, 50]"
          layout="prev, pager, next, sizes"
          @current-change="loadBatches()"
          @size-change="loadBatches(true)"
        />
      </div>
    </ElCard>

    <ElDialog v-model="detailVisible" title="批次任务" width="900px">
      <div v-if="detailBatch" class="detail-meta">
        <ElTag size="small">{{ batchStatusLabel[detailBatch.status] ?? detailBatch.status }}</ElTag>
        <span>修订 #{{ detailBatch.revision_id }}</span>
        <span>配置 {{ detailBatch.profile_version }}</span>
        <span>选集指纹 {{ detailBatch.selection_hash.slice(0, 12) }}</span>
      </div>
      <div class="toolbar">
        <ElSelect
          v-model="itemStatus"
          placeholder="全部任务状态"
          clearable
          class="channel"
          @change="filterItems"
        >
          <ElOption
            v-for="(label, key) in itemStatusLabel"
            :key="key"
            :label="label"
            :value="key"
          />
        </ElSelect>
        <ElButton :loading="loadingItems" @click="loadItems">刷新</ElButton>
      </div>
      <ElTable v-loading="loadingItems" :data="items" size="small" empty-text="暂无任务">
        <ElTableColumn prop="episode_no" label="集" width="70" />
        <ElTableColumn prop="external_id" label="外部 ID" min-width="140" />
        <ElTableColumn label="阶段" width="110">
          <template #default="{ row }">
            <ElTag size="small" effect="plain">{{
              itemStatusLabel[row.status] ?? row.status
            }}</ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn prop="attempt" label="尝试" width="70" />
        <ElTableColumn label="可重试" width="80">
          <template #default="{ row }">{{ row.retryable ? '是' : '否' }}</template>
        </ElTableColumn>
        <ElTableColumn label="大小" width="110">
          <template #default="{ row }">{{ formatBytes(row.size_bytes) }}</template>
        </ElTableColumn>
        <ElTableColumn label="错误" min-width="200">
          <template #default="{ row }">
            <span v-if="row.error_code">{{ row.error_code }} · {{ row.error_message }}</span>
            <span v-else>—</span>
          </template>
        </ElTableColumn>
        <ElTableColumn label="下次可执行" width="170">
          <template #default="{ row }">{{ formatTime(row.next_eligible_at) }}</template>
        </ElTableColumn>
      </ElTable>
      <div class="pager">
        <span>共 {{ itemTotal }} 个任务</span>
        <ElPagination
          v-model:current-page="itemPage"
          v-model:page-size="itemPageSize"
          :total="itemTotal"
          :page-sizes="[20, 50, 100]"
          layout="prev, pager, next, sizes"
          @current-change="loadItems()"
          @size-change="loadItems()"
        />
      </div>
      <template #footer>
        <ElInput v-model="cancelReason" placeholder="取消原因" class="cancel-reason" />
        <ElButton @click="detailVisible = false">关闭</ElButton>
      </template>
    </ElDialog>
  </main>
</template>

<style scoped>
.media {
  max-width: none;
  margin: 0;
  padding: 24px 20px 64px;
}
.topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding-bottom: 20px;
  border-bottom: 1px solid var(--el-border-color-lighter);
}
.brand {
  font-weight: 700;
  color: var(--el-text-color-primary);
  text-decoration: none;
}
.brand span {
  margin-left: 8px;
  color: var(--el-text-color-secondary);
  font-weight: 500;
}
.nav {
  display: flex;
  gap: 18px;
}
.nav a {
  color: var(--el-text-color-secondary);
  text-decoration: none;
}
.nav a.active {
  color: var(--el-color-primary);
  font-weight: 600;
}
.heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 24px;
  margin: 28px 0 20px;
}
.heading h1 {
  margin: 0 0 8px;
  font-size: 26px;
}
.heading p {
  margin: 0;
  color: var(--el-text-color-secondary);
}
.token-box {
  display: flex;
  gap: 8px;
  align-items: center;
  max-width: 300px;
}
.panel {
  margin-bottom: 20px;
}
.panel-title {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 16px;
}
.panel-title span {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
.submit-row {
  display: flex;
  align-items: flex-end;
  gap: 16px;
  flex-wrap: wrap;
}
.submit-row label {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 13px;
  color: var(--el-text-color-secondary);
}
.channel {
  width: 180px;
}
.toolbar {
  display: flex;
  gap: 12px;
  margin-bottom: 12px;
}
.pager {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 16px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
.detail-meta {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 12px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
.cancel-reason {
  max-width: 320px;
  margin-right: 12px;
}
</style>
