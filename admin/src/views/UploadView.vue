<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import {
  ElAlert,
  ElButton,
  ElCard,
  ElInputNumber,
  ElMessage,
  ElProgress,
  ElTable,
  ElTableColumn,
  ElTag,
} from 'element-plus'
import {
  ApiError,
  cancelUpload,
  completeUpload,
  createUpload,
  listRevisionEpisodes,
  listUploads,
  uploadToStorage,
  type RevisionEpisodeOption,
  type UploadSession,
} from '@/api/upload'

interface EpisodeState {
  file: File | null
  percent: number
  busy: boolean
  error: string
  session: UploadSession | null
}

function describeError(error: unknown): string {
  if (error instanceof ApiError) return `${error.reason}：${error.message}`
  return error instanceof Error ? error.message : String(error)
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

const route = useRoute()
const revisionId = ref<number | null>(null)
const revisionVersion = ref<number | null>(null)
const episodes = ref<RevisionEpisodeOption[]>([])
const loadingEpisodes = ref(false)
const loadError = ref('')
const states = reactive<Record<number, EpisodeState>>({})

function stateOf(episodeId: number): EpisodeState {
  if (!states[episodeId]) {
    states[episodeId] = { file: null, percent: 0, busy: false, error: '', session: null }
  }
  return states[episodeId]
}

function onEpisodeFile(episodeId: number, event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0] ?? null
  const state = stateOf(episodeId)
  state.file = file
  state.error = ''
  state.percent = 0
  // A new file means a new object: the previous session is abandoned on the
  // server side and may be cancelled explicitly.
  state.session = null
}

async function loadEpisodes() {
  if (!revisionId.value) {
    loadError.value = '请填写修订 ID'
    return
  }
  loadingEpisodes.value = true
  loadError.value = ''
  try {
    const set = await listRevisionEpisodes(revisionId.value)
    episodes.value = set.items ?? []
    if (episodes.value.length === 0) loadError.value = '该修订没有分集'
  } catch (error) {
    loadError.value = describeError(error)
  } finally {
    loadingEpisodes.value = false
  }
}

// startEpisodeUpload opens a session, PUTs the bytes to the signed URL and
// asks the server to confirm the object. A failure keeps the chosen file so
// the operator can retry without picking it again.
async function startEpisodeUpload(episode: RevisionEpisodeOption) {
  const state = stateOf(episode.id)
  if (!state.file) {
    state.error = '请选择文件'
    return
  }
  if (!revisionVersion.value) {
    state.error = '请填写修订版本'
    return
  }
  state.busy = true
  state.error = ''
  state.percent = 0
  try {
    const session = await createUpload({
      revisionId: revisionId.value as number,
      expectedRevisionVersion: revisionVersion.value,
      episodeId: episode.id,
      kind: 'episode_input',
      filename: state.file.name,
      sizeBytes: state.file.size,
      contentType: state.file.type,
    })
    state.session = session
    if (!session.authorization) throw new Error('服务端未返回上传授权')
    await uploadToStorage(session.authorization, state.file, (percent) => {
      state.percent = percent
    })
    state.session = await completeUpload(session.id, session.version)
    ElMessage.success(`第 ${episode.number} 集已登记为可信输入`)
    await refreshUploads()
  } catch (error) {
    state.error = describeError(error)
  } finally {
    state.busy = false
  }
}

async function cancelEpisodeUpload(episode: RevisionEpisodeOption) {
  const state = stateOf(episode.id)
  if (!state.session) return
  state.busy = true
  state.error = ''
  try {
    state.session = await cancelUpload(state.session.id, state.session.version)
    ElMessage.info('上传会话已取消')
    await refreshUploads()
  } catch (error) {
    state.error = describeError(error)
  } finally {
    state.busy = false
  }
}

const coverFile = ref<File | null>(null)
const coverState = reactive<{
  percent: number
  busy: boolean
  error: string
  session: UploadSession | null
}>({ percent: 0, busy: false, error: '', session: null })

function onCoverFile(event: Event) {
  const input = event.target as HTMLInputElement
  coverFile.value = input.files?.[0] ?? null
  coverState.percent = 0
  coverState.error = ''
  coverState.session = null
}

async function startCoverUpload() {
  if (!coverFile.value) {
    coverState.error = '请选择封面图片'
    return
  }
  if (!revisionId.value || !revisionVersion.value) {
    coverState.error = '请先填写修订 ID 与版本'
    return
  }
  coverState.busy = true
  coverState.error = ''
  coverState.percent = 0
  try {
    const session = await createUpload({
      revisionId: revisionId.value,
      expectedRevisionVersion: revisionVersion.value,
      kind: 'cover',
      filename: coverFile.value.name,
      sizeBytes: coverFile.value.size,
      contentType: coverFile.value.type,
    })
    coverState.session = session
    if (!session.authorization) throw new Error('服务端未返回上传授权')
    await uploadToStorage(session.authorization, coverFile.value, (percent) => {
      coverState.percent = percent
    })
    coverState.session = await completeUpload(session.id, session.version)
    ElMessage.success('封面已校验并固化')
    await refreshUploads()
  } catch (error) {
    coverState.error = describeError(error)
  } finally {
    coverState.busy = false
  }
}

async function cancelCoverUpload() {
  if (!coverState.session) return
  coverState.busy = true
  try {
    coverState.session = await cancelUpload(coverState.session.id, coverState.session.version)
    await refreshUploads()
  } catch (error) {
    coverState.error = describeError(error)
  } finally {
    coverState.busy = false
  }
}

const sessions = ref<UploadSession[]>([])
const sessionError = ref('')

async function refreshUploads() {
  if (!revisionId.value) return
  sessionError.value = ''
  try {
    const set = await listUploads(revisionId.value)
    sessions.value = set.items ?? []
  } catch (error) {
    sessionError.value = describeError(error)
  }
}

const statusLabel: Record<string, string> = {
  pending: '待上传',
  completed: '已确认',
  cancelled: '已取消',
}

const kindLabel: Record<string, string> = {
  cover: '封面',
  episode_input: '分集视频',
}

const inputLabel = (state: EpisodeState): string => {
  if (!state.session) return state.file ? '已选择，未上传' : '未选择文件'
  return statusLabel[state.session.status] ?? state.session.status
}

const completedCount = computed(
  () => sessions.value.filter((item) => item.status === 'completed').length,
)

// asEpisode narrows the table row type of Element Plus to the episode shape.
function asEpisode(row: unknown): RevisionEpisodeOption {
  return row as RevisionEpisodeOption
}

// The content page opens this screen with the draft it wants uploaded, so the
// operator does not copy identifiers between pages.
onMounted(() => {
  const revision = Number(route.query.revision)
  const version = Number(route.query.version)
  if (Number.isInteger(revision) && revision > 0) revisionId.value = revision
  if (Number.isInteger(version) && version > 0) revisionVersion.value = version
  if (revisionId.value && revisionVersion.value) void loadEpisodes()
})
</script>

<template>
  <main class="media">
    <section class="heading">
      <div>
        <h1>人工上传</h1>
        <p>
          上传只登记为可信输入；视频仍走统一的校验、处理、上传与对象确认，封面经服务端解码校验后固化为私有对象。
        </p>
      </div>
      <SessionBar />
    </section>

    <ElCard shadow="never" class="panel">
      <template #header>
        <div class="panel-title">
          <strong>01 选择修订</strong>
          <span>上传绑定草稿修订；视频要求在草稿阶段，封面可在批次处理中补传</span>
        </div>
      </template>
      <ElAlert v-if="loadError" type="error" :closable="false" :title="loadError" show-icon />
      <div class="submit-row">
        <label>修订 ID<ElInputNumber v-model="revisionId" :min="1" :controls="false" /></label>
        <label
          >修订版本<ElInputNumber v-model="revisionVersion" :min="1" :controls="false"
        /></label>
        <ElButton type="primary" :loading="loadingEpisodes" @click="loadEpisodes"
          >加载分集</ElButton
        >
        <ElButton :disabled="!revisionId" @click="refreshUploads">刷新上传记录</ElButton>
      </div>
      <p v-if="episodes.length" class="hint">
        共 {{ episodes.length }} 个分集，已确认上传 {{ completedCount }} 条会话。
      </p>
    </ElCard>

    <ElCard shadow="never" class="panel">
      <template #header>
        <div class="panel-title">
          <strong>02 分集视频</strong>
          <span>上传完成后由服务端确认对象；客户端回调不等于素材就绪</span>
        </div>
      </template>
      <ElTable :data="episodes" empty-text="先加载修订分集">
        <ElTableColumn prop="number" label="集号" width="80" />
        <ElTableColumn prop="external_id" label="来源分集ID" min-width="180" />
        <ElTableColumn label="已选" width="80">
          <template #default="{ row }">
            <ElTag :type="row.selected ? 'success' : 'info'" size="small">{{
              row.selected ? '是' : '否'
            }}</ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn label="文件 / 状态" min-width="260">
          <template #default="{ row }">
            <div class="upload-cell">
              <input type="file" accept="video/*" @change="onEpisodeFile(row.id, $event)" />
              <span class="hint">{{ inputLabel(stateOf(row.id)) }}</span>
              <ElProgress
                v-if="stateOf(row.id).busy || stateOf(row.id).percent > 0"
                :percentage="stateOf(row.id).percent"
                :stroke-width="6"
              />
            </div>
          </template>
        </ElTableColumn>
        <ElTableColumn label="操作" width="220">
          <template #default="{ row }">
            <div class="upload-actions">
              <ElButton
                size="small"
                type="primary"
                :loading="stateOf(row.id).busy"
                :disabled="!stateOf(row.id).file || stateOf(row.id).session?.status === 'completed'"
                @click="startEpisodeUpload(asEpisode(row))"
              >
                上传并确认
              </ElButton>
              <ElButton
                size="small"
                :disabled="
                  !stateOf(row.id).session || stateOf(row.id).session?.status !== 'pending'
                "
                @click="cancelEpisodeUpload(asEpisode(row))"
              >
                取消会话
              </ElButton>
            </div>
            <p v-if="stateOf(row.id).error" class="error-text">{{ stateOf(row.id).error }}</p>
          </template>
        </ElTableColumn>
      </ElTable>
    </ElCard>

    <ElCard shadow="never" class="panel">
      <template #header>
        <div class="panel-title">
          <strong>03 封面</strong>
          <span>仅接受真实 JPEG/PNG；校验尺寸后写入内容寻址的私有对象</span>
        </div>
      </template>
      <ElAlert
        v-if="coverState.error"
        type="error"
        :closable="false"
        :title="coverState.error"
        show-icon
      />
      <div class="submit-row">
        <input type="file" accept="image/jpeg,image/png" @change="onCoverFile" />
        <ElButton
          type="primary"
          :loading="coverState.busy"
          :disabled="!coverFile"
          @click="startCoverUpload"
        >
          上传并确认封面
        </ElButton>
        <ElButton
          :disabled="!coverState.session || coverState.session?.status !== 'pending'"
          @click="cancelCoverUpload"
        >
          取消会话
        </ElButton>
        <span v-if="coverFile" class="hint"
          >{{ coverFile.name }} · {{ formatBytes(coverFile.size) }}</span
        >
      </div>
      <ElProgress
        v-if="coverState.busy || coverState.percent > 0"
        :percentage="coverState.percent"
        :stroke-width="6"
      />
      <p v-if="coverState.session?.status === 'completed'" class="hint">
        封面已绑定修订，尺寸 {{ coverState.session.width }}×{{ coverState.session.height }}，素材
        #{{ coverState.session.asset_id }}
      </p>
    </ElCard>

    <ElCard shadow="never" class="panel">
      <template #header>
        <div class="panel-title">
          <strong>04 上传记录</strong>
          <span>同一对象续签不会创建第二个绑定</span>
        </div>
      </template>
      <ElAlert v-if="sessionError" type="error" :closable="false" :title="sessionError" show-icon />
      <ElTable :data="sessions" empty-text="暂无上传记录">
        <ElTableColumn prop="id" label="会话" width="80" />
        <ElTableColumn label="用途" width="100">
          <template #default="{ row }">{{ kindLabel[row.kind] ?? row.kind }}</template>
        </ElTableColumn>
        <ElTableColumn label="分集ID" width="100">
          <template #default="{ row }">{{ row.episode_id || '—' }}</template>
        </ElTableColumn>
        <ElTableColumn label="状态" width="100">
          <template #default="{ row }">
            <ElTag
              size="small"
              :type="
                row.status === 'completed'
                  ? 'success'
                  : row.status === 'cancelled'
                    ? 'info'
                    : 'warning'
              "
            >
              {{ statusLabel[row.status] ?? row.status }}
            </ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn prop="filename" label="文件名" min-width="180" />
        <ElTableColumn label="大小" width="100">
          <template #default="{ row }">{{ formatBytes(row.size_bytes) }}</template>
        </ElTableColumn>
        <ElTableColumn label="尺寸" width="110">
          <template #default="{ row }">{{
            row.width ? `${row.width}×${row.height}` : '—'
          }}</template>
        </ElTableColumn>
        <ElTableColumn prop="version" label="版本" width="80" />
      </ElTable>
    </ElCard>
  </main>
</template>

<style scoped>
.upload-cell {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.upload-actions {
  display: flex;
  gap: 8px;
}
.hint {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.error-text {
  margin: 6px 0 0;
  color: var(--el-color-danger);
  font-size: 12px;
}
</style>
