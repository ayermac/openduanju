<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  ElAlert,
  ElButton,
  ElCard,
  ElCheckbox,
  ElCheckboxGroup,
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
  cancelRun,
  createSeries,
  createPlan,
  getCandidate,
  listRunItems,
  listSourceEntries,
  listChannels,
  listSources,
  getSeries,
  listCandidates,
  listPlans,
  listRuns,
  retryRun,
  setSelection,
  triggerPlan,
  updatePlan,
  type Candidate,
  type ChannelConfig,
  type Plan,
  type RunItem,
  type SourceEntry,
  type SourceEpisode,
  type PlanEdit,
  type RevisionEpisode,
  type Run,
  type SeriesDetail,
} from '@/api/source'

const plans = ref<Plan[]>([])
const activePlanId = ref<number | null>(null)
const triggering = ref(false)
const currentRun = ref<Run | null>(null)
const runs = ref<Run[]>([])
const planError = ref('')
const planEditing = ref(false)
// planCreating switches the dialog between a new plan and an edited one.
const planCreating = ref(false)
const planSource = ref({ code: '', source_code: 'hongguo', channel: 'hot-drama' })
// channels and sources come from the server configuration: the form offers
// only what the server registered and can really run.
const configuredSources = ref<{ code: string; name: string; status: string }[]>([])
const configuredChannels = ref<ChannelConfig[]>([])
const planSaving = ref(false)
const planForm = ref<PlanEdit>({
  name: '',
  page_start: 1,
  page_end: 1,
  request_budget: 80,
  item_budget: 200,
  rate_per_minute: 30,
  fetch_details: true,
  fetch_episodes: true,
  enabled: true,
  cron: '',
  timezone: 'Asia/Shanghai',
})
const retryRequests = ref(20)
const retryItems = ref(0)
const retryReason = ref('')
const retrying = ref(false)

const candidates = ref<Candidate[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(10)
const keyword = ref('')
// entries are the web entry points the server can really execute.
const entries = ref<SourceEntry[]>([])
// candidateView is the read-only snapshot of one candidate.
const candidateView = ref<{ candidate: Candidate; episodes: SourceEpisode[] } | null>(null)
const snapshotLoadingId = ref<number | null>(null)
const snapshotError = ref('')
const snapshotPage = ref(1)
const snapshotPageSize = 20
const snapshotCard = ref<HTMLElement | null>(null)
const snapshotEpisodes = computed(() => {
  const episodes = candidateView.value?.episodes ?? []
  const start = (snapshotPage.value - 1) * snapshotPageSize
  return episodes.slice(start, start + snapshotPageSize)
})
// runRows are the stored rows one collection run produced.
const runRows = ref<RunItem[]>([])
const runRowsTotal = ref(0)
const runRowsPage = ref(1)
const runRowsSize = ref(20)
// runRowsRequest guards against a slow answer of a previous run overwriting
// the rows of the current one.
const runRowsRequest = ref(0)
const completeness = ref('')
// imported filters the candidates by whether a series already exists.
const imported = ref<'' | 'true' | 'false'>('')
const loadingCandidates = ref(false)
const listError = ref('')

const series = ref<SeriesDetail | null>(null)
const selectedIds = ref<string[]>([])
const loadingSeries = ref(false)
const saving = ref(false)
const seriesError = ref('')

let pollTimer: number | undefined

const selectedCount = computed(() => selectedIds.value.length)
const scheduleHint = computed(() => {
  const plan = activePlan()
  if (!plan || !plan.cron) return '未设置定时：该计划仅手动触发'
  const next = plan.next_run_at ? `，下次触发 ${formatTime(plan.next_run_at)}` : ''
  return `定时 ${plan.cron}（${plan.timezone}）${next}`
})
const metricsLabel: Record<string, string> = {
  heat: '热度',
  score: '评分',
  favorite: '收藏',
  like: '点赞',
  share: '分享',
}
const stageLabel: Record<string, string> = {
  summary: '榜单条目',
  detail: '作品详情',
  episodes: '分集快照',
}

const completenessLabel: Record<string, string> = {
  summary: '仅榜单',
  detail: '缺分集',
  complete: '完整',
}
const runStatusLabel: Record<string, string> = {
  queued: '排队',
  running: '执行中',
  waiting: '等待分钟额度',
  budget_exhausted: '总预算耗尽',
  succeeded: '成功',
  failed: '失败',
  cancelled: '已取消',
}

function activePlan(): Plan | undefined {
  return plans.value.find((plan) => plan.id === activePlanId.value)
}

function describeError(error: unknown): string {
  if (error instanceof ApiError) return `${error.reason}：${error.message}`
  return error instanceof Error ? error.message : String(error)
}

function isActive(run: Run | null): boolean {
  return run?.status === 'queued' || run?.status === 'running' || run?.status === 'waiting'
}

async function loadEntries() {
  try {
    const [entrySet, sourceSet, channelSet] = await Promise.all([
      listSourceEntries(),
      listSources(),
      listChannels(),
    ])
    entries.value = entrySet.items ?? []
    configuredSources.value = sourceSet.items ?? []
    configuredChannels.value = channelSet.items ?? []
    const firstSource = configuredSources.value[0]
    if (
      firstSource &&
      !configuredSources.value.some((item) => item.code === planSource.value.source_code)
    ) {
      planSource.value.source_code = firstSource.code
    }
    const available = channelsOf(planSource.value.source_code)
    const firstChannel = available[0]
    if (firstChannel && !available.some((item) => item.code === planSource.value.channel)) {
      planSource.value.channel = firstChannel.code
    }
  } catch (error) {
    planError.value = describeError(error)
  }
}

// channelsOf returns the configured channels of one source.
function channelsOf(sourceCode: string): ChannelConfig[] {
  return configuredChannels.value.filter((item) => item.source_code === sourceCode)
}

// effectiveRate is the rate a run would use: the plan override, or the
// channel value when the plan inherits it.
function effectiveRate(): string {
  const channel = channelsOf(planSource.value.source_code).find(
    (item) => item.code === planSource.value.channel,
  )
  if (planForm.value.rate_per_minute > 0) {
    return `计划限频 ${planForm.value.rate_per_minute} 次/分钟`
  }
  return channel ? `继承通道限频 ${channel.rate_per_minute} 次/分钟` : '继承通道限频'
}

async function loadWorkspace() {
  planError.value = ''
  try {
    const [planSet, runSet] = await Promise.all([listPlans(), listRuns(1, 8)])
    plans.value = planSet.items ?? []
    runs.value = runSet.items ?? []
    const firstPlan = plans.value[0]
    if (activePlanId.value === null && firstPlan) {
      activePlanId.value = firstPlan.id
    }
    const active = runs.value.find((run) => isActive(run))
    if (active) {
      currentRun.value = active
      schedulePoll()
    } else if (runs.value[0]) {
      currentRun.value = runs.value[0]
    }
  } catch (error) {
    planError.value = describeError(error)
  }
}

function schedulePoll() {
  if (pollTimer) window.clearInterval(pollTimer)
  pollTimer = window.setInterval(async () => {
    if (!currentRun.value || !isActive(currentRun.value)) {
      window.clearInterval(pollTimer)
      pollTimer = undefined
      return
    }
    try {
      const runSet = await listRuns(1, 8)
      runs.value = runSet.items ?? []
      const latest = runs.value.find((run) => run.id === currentRun.value?.id)
      if (latest) currentRun.value = latest
      if (!isActive(currentRun.value)) {
        window.clearInterval(pollTimer)
        pollTimer = undefined
        await loadCandidates(true)
        ElMessage.success(`采集运行 #${currentRun.value?.id} ${currentRun.value?.status}`)
      }
    } catch {
      // Polling errors are transient; the next tick retries.
    }
  }, 2000)
}

async function trigger() {
  if (!activePlanId.value) return
  triggering.value = true
  planError.value = ''
  try {
    const plan = activePlan()
    if (!plan) return
    currentRun.value = await triggerPlan(activePlanId.value, plan.version)
    ElMessage.success(`已提交运行 #${currentRun.value.id}`)
    schedulePoll()
    await loadWorkspace()
  } catch (error) {
    planError.value = describeError(error)
  } finally {
    triggering.value = false
  }
}

// openPlanCreate opens the dialog for a new plan. Only the web entry points
// are offered; other sources keep an extension slot.
function openPlanCreate() {
  planCreating.value = true
  planSource.value = {
    code: '',
    source_code: configuredSources.value[0]?.code ?? 'hongguo',
    channel: channelsOf(configuredSources.value[0]?.code ?? 'hongguo')[0]?.code ?? 'hot-drama',
  }
  planForm.value = {
    name: '',
    page_start: 1,
    page_end: 1,
    request_budget: 20,
    item_budget: 100,
    // Zero means "inherit the channel rate", so the plan never carries a
    // second copy of it.
    rate_per_minute: 0,
    fetch_details: true,
    fetch_episodes: true,
    enabled: true,
    cron: '',
    timezone: 'Asia/Shanghai',
  }
  planEditing.value = true
}

function openPlanEdit() {
  const plan = activePlan()
  if (!plan) return
  planCreating.value = false
  planForm.value = {
    name: plan.name,
    page_start: plan.page_start,
    page_end: plan.page_end,
    request_budget: plan.request_budget,
    item_budget: plan.item_budget,
    rate_per_minute: plan.rate_per_minute,
    fetch_details: plan.fetch_details,
    fetch_episodes: plan.fetch_episodes,
    enabled: plan.enabled,
    cron: plan.cron ?? '',
    timezone: plan.timezone || 'Asia/Shanghai',
  }
  planEditing.value = true
}

async function savePlan() {
  planSaving.value = true
  planError.value = ''
  try {
    if (planCreating.value) {
      const created = await createPlan({ ...planForm.value, ...planSource.value })
      plans.value = [...plans.value, created]
      activePlanId.value = created.id
      ElMessage.success('计划已创建')
    } else {
      const plan = activePlan()
      if (!plan) return
      const updated = await updatePlan(plan.id, plan.version, planForm.value)
      plans.value = plans.value.map((item) => (item.id === updated.id ? updated : item))
      ElMessage.success('计划已更新')
    }
    planEditing.value = false
  } catch (error) {
    planError.value = describeError(error)
  } finally {
    planSaving.value = false
  }
}

async function retryCurrent() {
  if (!currentRun.value) return
  retrying.value = true
  planError.value = ''
  try {
    currentRun.value = await retryRun(
      currentRun.value.id,
      currentRun.value.version,
      retryRequests.value,
      retryItems.value,
      retryReason.value,
    )
    retryReason.value = ''
    ElMessage.success(`已恢复运行 #${currentRun.value.id}，已消耗预算保留`)
    schedulePoll()
    await loadWorkspace()
  } catch (error) {
    planError.value = describeError(error)
  } finally {
    retrying.value = false
  }
}

async function cancelCurrent() {
  if (!currentRun.value) return
  try {
    currentRun.value = await cancelRun(currentRun.value.id, currentRun.value.version)
    ElMessage.info('已请求取消，工作进程会在检查点停止')
  } catch (error) {
    planError.value = describeError(error)
  }
}

async function loadCandidates(resetPage = false) {
  if (resetPage) page.value = 1
  loadingCandidates.value = true
  listError.value = ''
  try {
    const data = await listCandidates(
      page.value,
      pageSize.value,
      keyword.value,
      completeness.value,
      imported.value === '' ? undefined : imported.value === 'true',
    )
    candidates.value = data.items ?? []
    total.value = data.total ?? 0
    page.value = data.page ?? page.value
    pageSize.value = data.page_size ?? pageSize.value
  } catch (error) {
    listError.value = describeError(error)
  } finally {
    loadingCandidates.value = false
  }
}

function applySeries(data: SeriesDetail) {
  series.value = data
  selectedIds.value = (data.episodes ?? [])
    .filter((episode) => episode.selected)
    .map((episode) => episode.external_id)
}

// presentSnapshot shows the local candidate without creating a series.
async function presentSnapshot(detail: { candidate: Candidate; episodes: SourceEpisode[] }) {
  snapshotError.value = ''
  snapshotPage.value = 1
  candidateView.value = { candidate: detail.candidate, episodes: detail.episodes ?? [] }
  await nextTick()
  snapshotCard.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

// loadSnapshot reads one candidate. Failures stay on this page instead of the
// selection card, which is hidden until a series exists.
async function loadSnapshot(candidateId: number) {
  snapshotError.value = ''
  snapshotLoadingId.value = candidateId
  try {
    await presentSnapshot(await getCandidate(candidateId))
  } catch (error) {
    snapshotError.value = describeError(error)
    ElMessage.error(snapshotError.value)
  } finally {
    snapshotLoadingId.value = null
  }
}

// viewCandidateById opens the current snapshot of one candidate, read-only.
function viewCandidateById(candidateId: number) {
  return loadSnapshot(candidateId)
}

// openRunCandidate jumps from an execution detail to the candidate it
// produced.
async function openRunCandidate(runId: number) {
  const run = runs.value.find((item) => item.id === runId)
  if (!run || !run.candidate_id) {
    ElMessage.info('该运行没有关联候选')
    return
  }
  await loadSnapshot(run.candidate_id)
}

// viewCandidate opens a candidate read-only: the snapshot and its source
// episodes. It never creates a series.
function viewCandidate(candidate: Candidate) {
  return loadSnapshot(candidate.id)
}

// loadRunRows reads the stored rows of one run with their results. The rows
// belong to the run, not to the current candidate snapshot.
async function loadRunRows(runId: number, page = 1) {
  seriesError.value = ''
  runRowsPage.value = page
  const request = ++runRowsRequest.value
  try {
    const data = await listRunItems(runId, page, runRowsSize.value)
    if (request !== runRowsRequest.value) return
    runRows.value = data.items ?? []
    runRowsTotal.value = data.total ?? 0
  } catch (error) {
    if (request !== runRowsRequest.value) return
    runRows.value = []
    runRowsTotal.value = 0
    seriesError.value = describeError(error)
  }
}

// importCandidate is the explicit action that creates or fills a series from
// a candidate.
async function importCandidate(candidate: Candidate) {
  seriesError.value = ''
  loadingSeries.value = true
  try {
    const data = candidate.series_id
      ? await getSeries(candidate.series_id)
      : await createSeries(candidate.id, candidate.content_hash)
    applySeries(data)
    ElMessage.success(`已载入 ${data.episodes.length} 集`)
    await loadCandidates()
  } catch (error) {
    seriesError.value = describeError(error)
    if (error instanceof ApiError && error.reason === 'CANDIDATE_INCOMPLETE') {
      await loadWorkspace()
    }
  } finally {
    loadingSeries.value = false
  }
}

async function saveSelection() {
  if (!series.value) return
  saving.value = true
  seriesError.value = ''
  try {
    const result = await setSelection(
      series.value.draft.id,
      series.value.draft.version,
      'explicit',
      selectedIds.value,
    )
    ElMessage.success(`已保存 ${result.selected_count} 集`)
    applySeries(await getSeries(series.value.id))
    await loadCandidates()
  } catch (error) {
    seriesError.value = describeError(error)
  } finally {
    saving.value = false
  }
}

function selectAllEpisodes() {
  if (!series.value) return
  selectedIds.value = (series.value.episodes ?? []).map((episode) => episode.external_id)
}

function clearEpisodes() {
  selectedIds.value = []
}

function openRow(row: unknown) {
  void viewCandidate(row as Candidate)
}

function formatMetrics(metrics: Record<string, string> | undefined): string {
  if (!metrics) return '—'
  const entries = Object.entries(metrics).slice(0, 3)
  if (entries.length === 0) return '—'
  return entries.map(([key, value]) => `${metricsLabel[key] ?? key} ${value}`).join(' · ')
}

function formatTime(value: string | undefined): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString('zh-CN', { hour12: false })
}

function episodeLabel(episode: RevisionEpisode): string {
  return `第 ${episode.number} 集`
}

onMounted(async () => {
  await Promise.all([loadWorkspace(), loadEntries(), loadCandidates()])
})

onBeforeUnmount(() => {
  if (pollTimer) window.clearInterval(pollTimer)
})
</script>

<template>
  <main class="catalog">
    <section class="heading">
      <div>
        <h1>来源采集与本地选剧</h1>
        <p>采集任务持久化异步执行；详情与完整分集在采集阶段入库，选剧和勾选只读本地快照。</p>
      </div>
    </section>

    <ElCard shadow="never" class="panel">
      <template #header>
        <div class="panel-title">
          <strong>01 持久化采集运行</strong>
          <span>提交立即返回 run_id，页面轮询检查点</span>
        </div>
      </template>
      <ElAlert v-if="planError" type="error" :closable="false" :title="planError" show-icon />
      <div class="ingest-row">
        <label>
          采集计划
          <ElSelect v-model="activePlanId" class="channel" placeholder="暂无计划">
            <ElOption
              v-for="plan in plans"
              :key="plan.id"
              :label="`${plan.name}（${plan.channel}）`"
              :value="plan.id"
            />
          </ElSelect>
        </label>
        <ElButton type="primary" :loading="triggering" :disabled="!activePlanId" @click="trigger"
          >提交采集</ElButton
        >
        <ElButton v-if="isActive(currentRun)" @click="cancelCurrent">取消当前运行</ElButton>
        <ElButton :disabled="!activePlanId" @click="openPlanEdit">编辑计划</ElButton>
        <ElButton @click="openPlanCreate">新建计划</ElButton>
      </div>
      <div class="schedule-hint">{{ scheduleHint }}</div>
      <div v-if="currentRun" class="run-card">
        <div class="run-line">
          <strong>运行 #{{ currentRun.id }}</strong>
          <ElTag
            :type="
              currentRun.status === 'succeeded'
                ? 'success'
                : currentRun.status === 'failed' || currentRun.status === 'budget_exhausted'
                  ? 'danger'
                  : 'warning'
            "
          >
            {{ runStatusLabel[currentRun.status] ?? currentRun.status }}
          </ElTag>
          <span>检查点 {{ currentRun.page_cursor }} / {{ currentRun.page_end }}</span>
          <span>请求 {{ currentRun.requests_used }} / {{ currentRun.request_budget }}</span>
          <span>候选 {{ currentRun.items_upserted }} / {{ currentRun.items_seen }}</span>
        </div>
        <div v-if="currentRun.error_code" class="run-error">
          {{ currentRun.error_code }} · {{ currentRun.error_reason }}
        </div>
        <div class="run-hint">
          详情补齐 {{ currentRun.detail_fetched }}，分集补齐 {{ currentRun.episodes_fetched }}
          <template v-if="currentRun.status === 'waiting'">
            · 分钟额度用尽，到点自动继续；也可立即重试</template
          >
          <template v-if="currentRun.status === 'budget_exhausted'">
            · 已消耗预算保留，追加后从检查点继续</template
          >
        </div>
        <div class="retry-row">
          <ElButton size="small" @click="loadRunRows(currentRun.id)">查看逐条结果</ElButton>
          <ElButton
            v-if="currentRun.candidate_id"
            size="small"
            @click="openRunCandidate(currentRun.id)"
          >
            查看关联候选
          </ElButton>
        </div>
        <ElTable
          v-if="runRows.length"
          :data="runRows"
          size="small"
          class="run-rows"
          empty-text="该运行没有候选"
        >
          <ElTableColumn prop="candidate_external_id" label="候选" min-width="160" />
          <ElTableColumn label="阶段" width="100">
            <template #default="{ row }">{{ stageLabel[row.stage] ?? row.stage }}</template>
          </ElTableColumn>
          <ElTableColumn label="结果" width="100">
            <template #default="{ row }">
              <ElTag v-if="row.result === 'success'" type="success" size="small">成功</ElTag>
              <ElTag v-else type="danger" size="small">失败</ElTag>
            </template>
          </ElTableColumn>
          <ElTableColumn prop="attempt" label="尝试" width="70" />
          <ElTableColumn label="错误" min-width="180">
            <template #default="{ row }">
              <span v-if="row.error_code">{{ row.error_code }} · {{ row.error_message }}</span>
              <span v-else class="muted">—</span>
            </template>
          </ElTableColumn>
          <ElTableColumn label="操作" width="100">
            <template #default="{ row }">
              <ElButton size="small" plain @click="viewCandidateById(row.candidate_id)"
                >查看候选</ElButton
              >
            </template>
          </ElTableColumn>
        </ElTable>
        <div v-if="runRowsTotal" class="pager">
          <span>共 {{ runRowsTotal }} 条</span>
          <ElPagination
            v-model:current-page="runRowsPage"
            :page-size="runRowsSize"
            :total="runRowsTotal"
            layout="prev, pager, next"
            @current-change="loadRunRows(currentRun.id, $event)"
          />
        </div>
        <div
          v-if="['waiting', 'budget_exhausted', 'failed', 'cancelled'].includes(currentRun.status)"
          class="retry-row"
        >
          <label
            >追加请求<ElInputNumber v-model="retryRequests" :min="0" :max="1000" size="small"
          /></label>
          <label
            >追加条目<ElInputNumber v-model="retryItems" :min="0" :max="1000" size="small"
          /></label>
          <ElInput
            v-model="retryReason"
            placeholder="追加原因（追加预算必填）"
            class="retry-reason"
          />
          <ElButton type="primary" size="small" :loading="retrying" @click="retryCurrent"
            >追加预算并重试</ElButton
          >
        </div>
      </div>
      <ElTable :data="runs" size="small" empty-text="暂无运行记录" class="run-table">
        <ElTableColumn prop="id" label="运行" width="80" />
        <ElTableColumn prop="kind" label="类型" width="100" />
        <ElTableColumn prop="status" label="状态" width="100" />
        <ElTableColumn label="进度" min-width="160">
          <template #default="{ row }"
            >页 {{ row.page_cursor }}/{{ row.page_end }} · 候选 {{ row.items_upserted }}</template
          >
        </ElTableColumn>
        <ElTableColumn label="预算" width="120">
          <template #default="{ row }">{{ row.requests_used }}/{{ row.request_budget }}</template>
        </ElTableColumn>
        <ElTableColumn label="开始" width="170">
          <template #default="{ row }">{{ formatTime(row.started_at) }}</template>
        </ElTableColumn>
      </ElTable>
    </ElCard>

    <ElCard shadow="never" class="panel">
      <template #header>
        <div class="panel-title">
          <strong>02 本地候选</strong>
          <span>本地分页查询，不访问来源</span>
        </div>
      </template>
      <div class="toolbar">
        <ElInput
          v-model="keyword"
          placeholder="按标题搜索"
          clearable
          class="search"
          @keyup.enter="loadCandidates(true)"
          @clear="loadCandidates(true)"
        />
        <ElSelect
          v-model="completeness"
          placeholder="全部完整度"
          clearable
          class="channel"
          @change="loadCandidates(true)"
        >
          <ElOption label="完整" value="complete" />
          <ElOption label="缺分集" value="detail" />
          <ElOption label="仅榜单" value="summary" />
        </ElSelect>
        <ElSelect
          v-model="imported"
          placeholder="全部导入状态"
          clearable
          class="channel"
          @change="loadCandidates(true)"
        >
          <ElOption label="未导入" value="false" />
          <ElOption label="已导入" value="true" />
        </ElSelect>
        <ElButton :loading="loadingCandidates" @click="loadCandidates(true)">刷新</ElButton>
      </div>
      <ElAlert v-if="listError" type="error" :closable="false" :title="listError" show-icon />
      <ElTable
        v-loading="loadingCandidates"
        :data="candidates"
        empty-text="暂无候选，先提交采集运行"
        stripe
      >
        <ElTableColumn prop="rank" label="排名" width="70" />
        <ElTableColumn label="剧集" min-width="300">
          <template #default="{ row }">
            <div class="title-cell">
              <img v-if="row.cover_url" :src="row.cover_url" alt="" class="cover" loading="lazy" />
              <div>
                <div class="title">{{ row.title }}</div>
                <div class="tags">
                  <ElTag v-for="tag in row.tags" :key="tag" size="small" effect="plain">{{
                    tag
                  }}</ElTag>
                </div>
                <div class="metrics">{{ formatMetrics(row.metrics) }}</div>
              </div>
            </div>
          </template>
        </ElTableColumn>
        <ElTableColumn prop="episode_count" label="总集数" width="90" />
        <ElTableColumn label="快照" width="100">
          <template #default="{ row }">
            <ElTag :type="row.completeness === 'complete' ? 'success' : 'warning'" size="small">
              {{ completenessLabel[row.completeness] ?? row.completeness }}
            </ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn label="状态" width="100">
          <template #default="{ row }">
            <ElTag v-if="row.series_id" type="success" size="small">已选剧</ElTag>
            <ElTag v-else size="small">未选择</ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn label="最近采集" width="170">
          <template #default="{ row }">{{ formatTime(row.last_seen_at) }}</template>
        </ElTableColumn>
        <ElTableColumn label="操作" width="220" fixed="right">
          <template #default="{ row }">
            <ElButton
              size="small"
              plain
              :loading="snapshotLoadingId === row.id"
              @click="openRow(row)"
              >查看快照</ElButton
            >
            <ElButton
              size="small"
              type="primary"
              plain
              :loading="loadingSeries"
              @click="importCandidate(row as Candidate)"
            >
              {{ row.series_id ? '选剧回填' : '创建内容' }}
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
          @current-change="loadCandidates()"
          @size-change="loadCandidates(true)"
        />
      </div>
    </ElCard>

    <div ref="snapshotCard">
      <ElCard v-if="candidateView" shadow="never" class="panel">
        <template #header>
          <div class="panel-title">
            <strong>候选快照 · {{ candidateView.candidate.title || '未命名' }}</strong>
            <span>只读本地快照，不会创建内容</span>
          </div>
        </template>
        <div class="series-meta">
          <ElTag
            :type="candidateView.candidate.completeness === 'complete' ? 'success' : 'warning'"
            effect="plain"
          >
            {{
              completenessLabel[candidateView.candidate.completeness] ??
              candidateView.candidate.completeness
            }}
          </ElTag>
          <span>
            {{ candidateView.candidate.source_code }} / {{ candidateView.candidate.channel }} · 来源
            {{ candidateView.candidate.external_id }} · 总集数
            {{ candidateView.candidate.episode_count }} · 快照
            {{ candidateView.episodes.length }} 集
          </span>
        </div>
        <p class="snapshot-copy">{{ candidateView.candidate.description || '简介未记录' }}</p>
        <p class="snapshot-copy">
          最近观察 {{ formatTime(candidateView.candidate.last_seen_at) }} · 详情
          {{ formatTime(candidateView.candidate.detail_fetched_at) }} · 分集
          {{ formatTime(candidateView.candidate.episodes_fetched_at) }} · 指纹
          {{ candidateView.candidate.content_hash || '未记录' }}
        </p>
        <ElTable :data="snapshotEpisodes" size="small" empty-text="没有分集快照">
          <ElTableColumn prop="number" label="集号" width="80" />
          <ElTableColumn label="标题" min-width="180">
            <template #default="{ row }">{{ row.title || '—' }}</template>
          </ElTableColumn>
          <ElTableColumn prop="external_id" label="来源分集" min-width="180" />
        </ElTable>
        <div v-if="candidateView.episodes.length > snapshotPageSize" class="pager">
          <span>共 {{ candidateView.episodes.length }} 集</span>
          <ElPagination
            v-model:current-page="snapshotPage"
            :page-size="snapshotPageSize"
            :total="candidateView.episodes.length"
            layout="prev, pager, next"
          />
        </div>
      </ElCard>
    </div>

    <ElCard v-if="series" shadow="never" class="panel">
      <template #header>
        <div class="panel-title">
          <strong>03 分集勾选</strong>
          <span
            >{{ series.draft.title || series.external_id }} · {{ series.episodes.length }} 集 · 已选
            {{ selectedCount }}</span
          >
        </div>
      </template>
      <ElAlert v-if="seriesError" type="error" :closable="false" :title="seriesError" show-icon />
      <div class="series-meta">
        <ElTag type="success" effect="plain">{{ series.status }}</ElTag>
        <span
          >来源 {{ series.source_code }} · 修订 #{{ series.draft.revision_no }} · 版本
          {{ series.draft.version }}</span
        >
      </div>
      <div class="selection-toolbar">
        <ElButton size="small" @click="selectAllEpisodes">全选</ElButton>
        <ElButton size="small" @click="clearEpisodes">清空</ElButton>
        <ElButton type="primary" size="small" :loading="saving" @click="saveSelection"
          >保存勾选（{{ selectedCount }}）</ElButton
        >
      </div>
      <div class="episodes" role="group" aria-label="分集选择">
        <ElCheckboxGroup v-model="selectedIds" class="episode-grid">
          <ElCheckbox
            v-for="episode in series.episodes"
            :key="episode.external_id"
            :value="episode.external_id"
            class="episode"
          >
            {{ episodeLabel(episode) }}
          </ElCheckbox>
        </ElCheckboxGroup>
      </div>
    </ElCard>
    <ElDialog
      v-model="planEditing"
      :title="planCreating ? '新建采集计划' : '编辑采集计划'"
      width="520px"
    >
      <div class="plan-form">
        <div v-if="planCreating" class="plan-inline">
          <label
            >计划代号<ElInput v-model="planSource.code" placeholder="如 hongguo-rank-daily"
          /></label>
          <label
            >来源<ElSelect v-model="planSource.source_code" @change="loadEntries">
              <ElOption
                v-for="item in configuredSources"
                :key="item.code"
                :label="`${item.name}（${item.code}）`"
                :value="item.code"
              /> </ElSelect
          ></label>
          <label
            >入口<ElSelect v-model="planSource.channel">
              <ElOption
                v-for="item in channelsOf(planSource.source_code)"
                :key="item.code"
                :label="`${item.name}（${item.code}）`"
                :value="item.code"
              /> </ElSelect
          ></label>
        </div>
        <label>名称<ElInput v-model="planForm.name" /></label>
        <div class="plan-inline">
          <label>起始页<ElInputNumber v-model="planForm.page_start" :min="1" :max="5" /></label>
          <label>结束页<ElInputNumber v-model="planForm.page_end" :min="1" :max="5" /></label>
        </div>
        <div class="plan-inline">
          <label
            >请求预算<ElInputNumber v-model="planForm.request_budget" :min="1" :max="10000"
          /></label>
          <label
            >条目预算<ElInputNumber v-model="planForm.item_budget" :min="1" :max="10000"
          /></label>
          <label class="rate-note"
            >生效限频：{{ effectiveRate()
            }}<ElInputNumber v-model="planForm.rate_per_minute" :min="0" :max="600"
          /></label>
        </div>
        <div class="plan-inline">
          <label
            >定时表达式<ElInput
              v-model="planForm.cron"
              placeholder="如 0 3 * * *（留空则仅手动触发）"
          /></label>
          <label>时区<ElInput v-model="planForm.timezone" placeholder="Asia/Shanghai" /></label>
        </div>
        <div class="plan-inline">
          <ElCheckbox v-model="planForm.fetch_details">抓详情</ElCheckbox>
          <ElCheckbox v-model="planForm.fetch_episodes">抓分集</ElCheckbox>
          <ElCheckbox v-model="planForm.enabled">启用</ElCheckbox>
        </div>
      </div>
      <template #footer>
        <ElButton @click="planEditing = false">取消</ElButton>
        <ElButton type="primary" :loading="planSaving" @click="savePlan">保存</ElButton>
      </template>
    </ElDialog>
  </main>
</template>

<style scoped>
.catalog {
  max-width: none;
  margin: 0;
  padding: 24px 20px 64px;
}
.token-box {
  display: flex;
  gap: 8px;
  align-items: center;
  max-width: 300px;
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
.panel {
  margin-bottom: 20px;
}
.snapshot-copy {
  margin: 0 0 12px;
  color: var(--el-text-color-regular);
  line-height: 1.6;
  white-space: pre-wrap;
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
.ingest-row {
  display: flex;
  align-items: flex-end;
  gap: 16px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
.ingest-row label {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 13px;
  color: var(--el-text-color-secondary);
}
.channel {
  width: 200px;
}
.run-card {
  padding: 12px 14px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 8px;
  margin-bottom: 12px;
  background: var(--el-fill-color-lighter);
}
.run-line {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
}
.run-error {
  margin-top: 6px;
  color: var(--el-color-danger);
  font-size: 13px;
}
.retry-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px dashed var(--el-border-color-lighter);
}
.retry-row label {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.retry-reason {
  max-width: 260px;
}
.plan-form {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.plan-form label {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.plan-inline {
  display: flex;
  gap: 16px;
  align-items: flex-end;
  flex-wrap: wrap;
}
.run-hint {
  margin-top: 4px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.schedule-hint {
  margin: -4px 0 12px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.run-table {
  margin-top: 8px;
}
.toolbar {
  display: flex;
  gap: 12px;
  margin-bottom: 12px;
}
.search {
  max-width: 320px;
}
.title-cell {
  display: flex;
  gap: 12px;
}
.cover {
  width: 52px;
  height: 72px;
  object-fit: cover;
  border-radius: 6px;
  background: var(--el-fill-color);
}
.title {
  font-weight: 600;
  margin-bottom: 6px;
}
.tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 4px;
}
.metrics {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.pager {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 16px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
.series-meta {
  display: flex;
  align-items: center;
  gap: 12px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
  margin-bottom: 12px;
}
.selection-toolbar {
  display: flex;
  gap: 8px;
  margin-bottom: 12px;
}
.episodes {
  max-height: 320px;
  overflow: auto;
  padding: 12px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 8px;
}
.episode-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(120px, 1fr));
  gap: 6px 12px;
}
.episode {
  margin-right: 0;
}
</style>
