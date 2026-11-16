<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  ElAlert,
  ElButton,
  ElCard,
  ElCheckbox,
  ElInput,
  ElInputNumber,
  ElOption,
  ElPagination,
  ElSelect,
  ElTable,
  ElTableColumn,
  ElTag,
  ElMessage,
} from 'element-plus'
import { can, currentSession } from '@/api/session'
import {
  ApiError,
  claimReview,
  decideReview,
  getReview,
  listReviews,
  publishRevision,
  type ReviewCase,
  type ReviewDetail,
} from '@/api/review'

const cases = ref<ReviewCase[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const statusFilter = ref('')
const loading = ref(false)
const error = ref('')
const detail = ref<ReviewDetail | null>(null)
const detailLoading = ref(false)
let detailRequest = 0
const reason = ref('')
const checklist = ref(emptyChecklist())
const busy = ref(false)
const publicationNote = ref('')

function emptyChecklist() {
  return {
    title_cover_intro: false,
    episode_mapping: false,
    audio_video: false,
    authorization_public: false,
    reviewed_episodes: 1,
  }
}

const session = currentSession()
const reviewerName = session?.staff.username ?? ''
const canReview = can('review')
const canPublish = can('publish')

// The server still re-checks every action; these flags only decide which
// controls are worth showing to the current operator.
const reviewReadonlyReason = computed(() => {
  if (!detail.value) return ''
  if (!canReview) return '审核裁决需要审核权限'
  const item = detail.value.case
  if (item.status === 'claimed' && item.assignee !== reviewerName) {
    return '该任务由其他认领人处理'
  }
  if (item.submitted_by === reviewerName) return '不能审核自己提交的内容'
  return ''
})

function describeError(err: unknown): string {
  if (err instanceof ApiError) return `${err.reason}：${err.message}`
  if (err instanceof Error) return err.message
  return '请求失败'
}

function statusLabel(status: string): string {
  switch (status) {
    case 'open':
      return '待认领'
    case 'claimed':
      return '已认领'
    case 'approved':
      return '已通过'
    case 'rejected':
      return '已驳回'
    default:
      return status
  }
}

function coverLabel(status: string | undefined): string {
  switch (status) {
    case 'ready':
      return '封面就绪'
    case 'missing':
      return '封面缺失'
    case 'pending':
      return '封面待处理'
    default:
      return '封面未记录'
  }
}

async function load(resetPage = false) {
  if (resetPage) page.value = 1
  loading.value = true
  error.value = ''
  try {
    const set = await listReviews(statusFilter.value || undefined, page.value, pageSize.value)
    cases.value = set.items ?? []
    total.value = set.total
  } catch (err) {
    error.value = describeError(err)
  } finally {
    loading.value = false
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

async function open(row: unknown) {
  const target = row as ReviewCase
  const requestId = ++detailRequest
  detail.value = null
  reason.value = ''
  publicationNote.value = ''
  checklist.value = emptyChecklist()
  error.value = ''
  detailLoading.value = true
  try {
    const next = await getReview(target.task_id)
    if (requestId !== detailRequest) return
    detail.value = next
  } catch (err) {
    if (requestId === detailRequest) error.value = describeError(err)
  } finally {
    if (requestId === detailRequest) detailLoading.value = false
  }
}

async function claim() {
  if (!detail.value || detailLoading.value || busy.value) return
  const target = detail.value.case
  const requestId = detailRequest
  busy.value = true
  try {
    await claimReview(target.task_id, target.version)
    ElMessage.success('已认领')
    if (requestId === detailRequest) await open(target)
    await load()
  } catch (err) {
    if (requestId === detailRequest) error.value = describeError(err)
  } finally {
    busy.value = false
  }
}

async function decide(decision: 'approved' | 'rejected') {
  if (!detail.value || detailLoading.value || busy.value) return
  const target = detail.value.case
  const requestId = detailRequest
  if (decision === 'rejected' && !reason.value.trim()) {
    error.value = '驳回必须填写理由'
    return
  }
  if (decision === 'approved') {
    const items = checklist.value
    if (
      !items.title_cover_intro ||
      !items.episode_mapping ||
      !items.audio_video ||
      !items.authorization_public
    ) {
      error.value = '通过前必须确认全部检查项'
      return
    }
    if (items.reviewed_episodes < 1 || items.reviewed_episodes > target.item_total) {
      error.value = '抽查集数必须在 1 到选中集数之间'
      return
    }
  }
  busy.value = true
  try {
    await decideReview(
      target.task_id,
      target.version,
      decision,
      reason.value.trim(),
      // The server re-checks the checklist; an approval without it is refused
      // even when the boxes are ticked, so the answers travel with the call.
      decision === 'approved' ? { ...checklist.value } : undefined,
    )
    ElMessage.success(decision === 'approved' ? '已通过，尚未发布' : '已驳回')
    if (requestId === detailRequest) await open(target)
    await load()
  } catch (err) {
    if (requestId === detailRequest) error.value = describeError(err)
  } finally {
    busy.value = false
  }
}

async function publish() {
  if (!detail.value) return
  busy.value = true
  try {
    const publication = await publishRevision(
      detail.value.case.series_id,
      detail.value.case.revision_id,
    )
    publicationNote.value = `已发布修订 #${publication.revision_id}`
    ElMessage.success('已发布')
  } catch (err) {
    error.value = describeError(err)
  } finally {
    busy.value = false
  }
}

onMounted(load)
</script>

<template>
  <main class="media">
    <section class="heading">
      <div>
        <h1>审核</h1>
        <p>
          认领后只有认领人可以裁决；不能审核自己提交的内容。通过只把修订变为已通过，发布是单独一步，发布后引用被冻结。
        </p>
      </div>
    </section>

    <ElAlert v-if="error" type="error" :closable="false" :title="error" show-icon />

    <ElCard shadow="never" class="panel">
      <template #header>
        <div class="panel-title">
          <strong>01 待审列表</strong>
          <span>共 {{ total }} 个任务</span>
        </div>
      </template>
      <div class="toolbar">
        <ElSelect
          v-model="statusFilter"
          placeholder="全部状态"
          clearable
          size="small"
          @change="load(true)"
        >
          <ElOption label="待认领" value="open" />
          <ElOption label="已认领" value="claimed" />
          <ElOption label="已通过" value="approved" />
          <ElOption label="已驳回" value="rejected" />
        </ElSelect>
        <ElButton size="small" :loading="loading" @click="load(true)">刷新</ElButton>
      </div>
      <ElTable :data="cases" empty-text="暂无审核任务">
        <ElTableColumn prop="task_id" label="任务" width="80" />
        <ElTableColumn label="作品" min-width="160">
          <template #default="{ row }">{{ row.series_title || `作品 #${row.series_id}` }}</template>
        </ElTableColumn>
        <ElTableColumn prop="revision_id" label="修订" width="80" />
        <ElTableColumn label="状态" width="100">
          <template #default="{ row }">
            <ElTag
              size="small"
              :type="
                row.status === 'approved'
                  ? 'success'
                  : row.status === 'rejected'
                    ? 'danger'
                    : 'warning'
              "
            >
              {{ statusLabel(row.status) }}
            </ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn label="素材" width="130">
          <template #default="{ row }"
            >就绪 {{ row.item_ready ?? 0 }}/{{ row.item_total ?? 0 }} ·
            {{ coverLabel(row.cover_status) }}</template
          >
        </ElTableColumn>
        <ElTableColumn prop="submitted_by" label="提交人" width="120" />
        <ElTableColumn prop="assignee" label="认领人" width="120" />
        <ElTableColumn prop="version" label="版本" width="70" />
        <ElTableColumn label="操作" width="100">
          <template #default="{ row }">
            <ElButton size="small" @click="open(row)">查看</ElButton>
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

    <ElCard v-if="detailLoading" shadow="never" class="panel" aria-live="polite">
      正在加载任务详情，加载完成前不能认领或裁决。
    </ElCard>

    <ElCard v-if="detail" shadow="never" class="panel">
      <template #header>
        <div class="panel-title">
          <strong>02 任务详情 #{{ detail.case.task_id }}</strong>
          <span>
            {{ detail.case.series_title }} · 修订 #{{ detail.case.revision_id }} ·
            {{ statusLabel(detail.case.status) }} · 提交人 {{ detail.case.submitted_by }}
          </span>
        </div>
      </template>

      <div class="toolbar">
        <ElButton
          v-if="
            detail.case.status === 'open' && canReview && detail.case.submitted_by !== reviewerName
          "
          type="primary"
          :loading="busy"
          @click="claim"
        >
          认领
        </ElButton>
        <template
          v-if="
            detail.case.status === 'claimed' &&
            canReview &&
            detail.case.assignee === reviewerName &&
            detail.case.submitted_by !== reviewerName
          "
        >
          <ElInput
            v-model="reason"
            placeholder="驳回理由（通过时忽略）"
            size="small"
            style="max-width: 280px"
          />
          <ElButton type="success" :loading="busy" @click="decide('approved')">通过</ElButton>
          <ElButton type="danger" :loading="busy" @click="decide('rejected')">驳回</ElButton>
        </template>
        <span v-if="reviewReadonlyReason" class="hint">{{ reviewReadonlyReason }}</span>
        <ElButton
          v-if="detail.case.status === 'approved' && canPublish"
          type="primary"
          :loading="busy"
          @click="publish"
        >
          发布
        </ElButton>
        <span v-if="publicationNote" class="hint">{{ publicationNote }}</span>
        <span v-if="session" class="hint">当前身份：{{ session.staff.username }}</span>
      </div>

      <div v-if="detail.case.status === 'claimed'" class="checklist">
        <span class="hint">通过前逐项确认（接口同样强制）：</span>
        <ElCheckbox v-model="checklist.title_cover_intro">标题/封面/简介匹配</ElCheckbox>
        <ElCheckbox v-model="checklist.episode_mapping">分集对应</ElCheckbox>
        <ElCheckbox v-model="checklist.audio_video">声音画面正常</ElCheckbox>
        <ElCheckbox v-model="checklist.authorization_public">授权与全年龄可公开</ElCheckbox>
        <span class="hint">已加载抽查集数</span>
        <ElInputNumber
          v-model="checklist.reviewed_episodes"
          :min="1"
          :max="detail.case.item_total"
          size="small"
        />
      </div>

      <ElAlert
        v-if="detail.decision"
        :type="detail.decision.decision === 'approved' ? 'success' : 'error'"
        :closable="false"
        :title="`${detail.decision.decision === 'approved' ? '已通过' : '已驳回'} · ${detail.decision.decided_by}`"
        :description="detail.decision.reason || '决定不可覆盖，修正需要新修订'"
        show-icon
      />

      <div v-if="detail.authorization" class="authz">
        授权依据 #{{ detail.authorization.id }} · {{ detail.authorization.reference }} · 处理
        {{ detail.authorization.allow_process ? '允许' : '不允许' }} / 展示
        {{ detail.authorization.allow_display ? '允许' : '不允许' }} / 播放
        {{ detail.authorization.allow_playback ? '允许' : '不允许' }} ·
        {{ detail.authorization.status }}
      </div>

      <div class="materials">
        <div v-if="detail.cover?.preview_url" class="cover">
          <span class="hint">封面 {{ detail.cover.width }}×{{ detail.cover.height }}</span>
          <img :src="detail.cover.preview_url" alt="封面预览" />
        </div>
        <div v-for="episode in detail.episodes" :key="episode.episode_id" class="episode">
          <span class="hint">
            第 {{ episode.number }} 集 · {{ episode.external_id }}
            <template v-if="episode.asset">
              · {{ episode.asset.width }}×{{ episode.asset.height }} ·
              {{ Math.round((episode.asset.duration_ms || 0) / 1000) }}s
            </template>
          </span>
          <video
            v-if="episode.asset?.preview_url"
            :src="episode.asset.preview_url"
            controls
            preload="metadata"
          />
          <span v-else class="hint">该集没有可用素材</span>
        </div>
      </div>
    </ElCard>
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
.checklist {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  margin: 12px 0;
}
.authz {
  margin: 8px 0 12px;
  font-size: 13px;
  color: var(--el-text-color-regular);
}
.materials {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
  margin-top: 12px;
}
.cover img {
  display: block;
  width: 140px;
  border-radius: 4px;
  border: 1px solid var(--el-border-color);
}
.episode video {
  display: block;
  width: 320px;
  border-radius: 4px;
  background: #000;
}
</style>
