<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  ElAlert,
  ElButton,
  ElDialog,
  ElInput,
  ElInputNumber,
  ElMessage,
  ElPagination,
  ElRadio,
  ElRadioGroup,
  ElTable,
  ElTableColumn,
} from 'element-plus'
import { ApiError } from '@/api/client'
import { can } from '@/api/session'
import { getPublication, unpublishSeries } from '@/api/review'
import {
  createManualSeries,
  createRevision,
  getSeries,
  listSeries,
  updateRevision,
  type SeriesDetail,
  type SeriesItem,
} from '@/api/content'

const items = ref<SeriesItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const keyword = ref('')
const loading = ref(false)
const listError = ref('')

const detail = ref<SeriesDetail | null>(null)
// The detail payload has no series-level title, so the page keeps the title
// the list row carried.
const detailTitle = ref('')
const detailError = ref('')
const detailLoading = ref(false)
const saving = ref(false)
// The draft the form was filled with; the version travels with the save.
const form = ref({ title: '', description: '' })
const formVersion = ref(0)
const conflict = ref('')

// The new-revision dialog. A series that still has an editable draft cannot
// get a second one: the operator sees that draft and confirms its replacement.
const creating = ref(false)
const showCreate = ref(false)
const createError = ref('')
const baseRevisionId = ref('')
const blockedDraft = ref<{ id: string; revisionNo: number; version: number } | null>(null)
const canWrite = ref(false)
const canPublish = ref(false)

// Taking a work down is deliberate: the dialog names the work, asks why, and
// carries the version the operator saw. A stale version refreshes the dialog
// instead of retrying on its own.
const showUnpublish = ref(false)
const unpublishing = ref(false)
const unpublishReason = ref('')
const unpublishError = ref('')
const unpublishVersion = ref(0)
const unpublishTarget = ref('')

// The new-work dialog declares a manual work: no provider, no external id,
// only what the operator writes. The request id is created with the form so a
// double click returns the work that already exists.
const showNewWork = ref(false)
const showCloseConfirm = ref(false)
const newWork = ref({ title: '', description: '' })
const newWorkEpisodes = ref<number[]>([1])
const newWorkError = ref('')
const creatingWork = ref(false)
let newWorkRequestID = ''

// The contract names the working draft "draft" and the revision on the
// shelf "published_revision".
const draft = computed(() => detail.value?.draft ?? null)
const published = computed(() => detail.value?.published_revision ?? null)
// A rejected revision keeps the material the reviewer judged: it stays on
// record and is never edited in place.
const editableDraft = computed(() => (draft.value?.state === 'draft' ? draft.value : null))

interface BaseOption {
  id: string
  label: string
}

// baseOptions are the revisions a new draft may inherit from.
const baseOptions = computed<BaseOption[]>(() => {
  const options: BaseOption[] = []
  if (draft.value) {
    options.push({
      id: draft.value.id,
      label: `当前修订 #${draft.value.revision_no}（${draft.value.state}）`,
    })
  }
  if (published.value && published.value.id !== draft.value?.id) {
    options.push({
      id: published.value.id,
      label: `在架版本 #${published.value.revision_no}（${published.value.state}）`,
    })
  }
  return options
})

async function load(resetPage = false) {
  if (resetPage) page.value = 1
  loading.value = true
  listError.value = ''
  try {
    const data = await listSeries({
      q: keyword.value,
      page: page.value,
      pageSize: pageSize.value,
    })
    items.value = data.items ?? []
    total.value = data.total ?? 0
  } catch (error) {
    listError.value = describeError(error)
  } finally {
    loading.value = false
  }
}

function describeError(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.reason === 'REVISION_CONFLICT') {
      return '版本冲突：草稿已被他人修改，请刷新后在最新版本上继续修改'
    }
    if (error.reason === 'DRAFT_EXISTS') {
      return '该作品已有可编辑草稿，新修订不会自动覆盖它'
    }
    if (error.reason === 'FORBIDDEN') {
      return '当前角色无权编辑草稿'
    }
    return `${error.reason}：${error.message}`
  }
  return error instanceof Error ? error.message : '请求失败'
}

async function open(item: SeriesItem) {
  detailError.value = ''
  conflict.value = ''
  createError.value = ''
  blockedDraft.value = null
  detailLoading.value = true
  detailTitle.value = item.title
  try {
    const data = await getSeries(item.series_id)
    detail.value = data
    form.value = {
      title: data.draft?.title ?? '',
      description: data.draft?.description ?? '',
    }
    formVersion.value = data.draft?.version ?? 0
  } catch (error) {
    detailError.value = describeError(error)
  } finally {
    detailLoading.value = false
  }
}

// save edits the working draft. A conflict keeps the form content so the
// operator can compare and retry.
async function save() {
  if (!detail.value || !editableDraft.value) return
  saving.value = true
  conflict.value = ''
  detailError.value = ''
  try {
    const updated = await updateRevision(
      detail.value.id,
      editableDraft.value.id,
      formVersion.value,
      {
        title: form.value.title,
        description: form.value.description,
      },
    )
    detail.value = updated
    detailTitle.value = updated.draft?.title || detailTitle.value
    form.value = {
      title: updated.draft?.title ?? '',
      description: updated.draft?.description ?? '',
    }
    formVersion.value = updated.draft?.version ?? formVersion.value
    ElMessage.success('草稿已保存')
    await load()
  } catch (error) {
    // The input stays as it is; the operator decides what to do.
    if (error instanceof ApiError && error.reason === 'REVISION_CONFLICT') {
      conflict.value = describeError(error)
    } else {
      detailError.value = describeError(error)
    }
  } finally {
    saving.value = false
  }
}

// openNewWork opens the manual creation form with a fresh idempotency key.
function openNewWork() {
  newWork.value = { title: '', description: '' }
  newWorkEpisodes.value = [1]
  newWorkError.value = ''
  newWorkRequestID = requestID()
  showNewWork.value = true
}

// requestID generates the key of this form. crypto.randomUUID is available in
// every browser the console supports; the fallback keeps a private window
// working.
function requestID(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) return crypto.randomUUID()
  return `manual-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`
}

const newWorkDirty = computed(
  () =>
    newWork.value.title.trim() !== '' ||
    newWork.value.description.trim() !== '' ||
    newWorkEpisodes.value.length !== 1 ||
    newWorkEpisodes.value[0] !== 1,
)

// Element Plus asks before closing on Esc, mask and the close button. We keep
// the form open until the operator explicitly chooses to continue or discard.
function guardNewWorkClose(done: () => void) {
  if (!newWorkDirty.value && !creatingWork.value) {
    done()
    return
  }
  showCloseConfirm.value = true
}

function discardNewWork() {
  showCloseConfirm.value = false
  showNewWork.value = false
  newWork.value = { title: '', description: '' }
  newWorkEpisodes.value = [1]
  newWorkError.value = ''
}

function addEpisode() {
  const last = newWorkEpisodes.value[newWorkEpisodes.value.length - 1] ?? 0
  newWorkEpisodes.value = [...newWorkEpisodes.value, last + 1]
}

function removeEpisode(index: number) {
  if (newWorkEpisodes.value.length <= 1) return
  newWorkEpisodes.value = newWorkEpisodes.value.filter((_, position) => position !== index)
}

// validateNewWork mirrors the server rules so the operator sees the problem
// before the request; the server still decides.
function validateNewWork(): string {
  const title = newWork.value.title.trim()
  if (!title) return '请填写作品标题'
  if ([...title].length > 120) return '作品标题不能超过120字'
  if ([...newWork.value.description.trim()].length > 5000) return '简介不能超过5000字'
  const numbers = newWorkEpisodes.value
  if (!numbers.length) return '至少需要一集'
  if (numbers.some((number) => !Number.isInteger(number) || number < 1 || number > 9999)) {
    return '分集序号需为1~9999'
  }
  if (new Set(numbers).size !== numbers.length) return '分集序号重复，请检查'
  return ''
}

async function submitNewWork() {
  const invalid = validateNewWork()
  newWorkError.value = invalid
  if (invalid) return
  creatingWork.value = true
  try {
    const created = await createManualSeries({
      title: newWork.value.title.trim(),
      description: newWork.value.description.trim(),
      episodeNumbers: [...newWorkEpisodes.value],
      requestId: newWorkRequestID,
    })
    detail.value = created
    detailTitle.value = created.draft?.title ?? ''
    form.value = {
      title: created.draft?.title ?? '',
      description: created.draft?.description ?? '',
    }
    formVersion.value = created.draft?.version ?? 0
    showNewWork.value = false
    ElMessage.success(
      `已创建《${created.draft?.title ?? ''}》草稿（${created.episodes?.length ?? 0}集），可继续上传分集`,
    )
    void load()
  } catch (error) {
    if (error instanceof ApiError) {
      if (error.reason === 'DUPLICATE_EPISODE_NUMBER') {
        newWorkError.value = '分集序号重复，请检查'
      } else if (error.reason === 'IDEMPOTENCY_CONFLICT') {
        newWorkError.value = '本次提交号已用于其他内容，请关闭后重新打开表单'
      } else {
        newWorkError.value = describeError(error)
      }
    } else {
      newWorkError.value = describeError(error)
    }
  } finally {
    creatingWork.value = false
  }
}

// openCreate opens the new-revision dialog on the current revision.
function openCreate() {
  if (!detail.value) return
  createError.value = ''
  blockedDraft.value = null
  baseRevisionId.value = baseOptions.value[0]?.id ?? ''
  showCreate.value = true
}

// applyCreated shows the new draft the server returned.
function applyCreated(data: SeriesDetail) {
  detail.value = data
  detailTitle.value = data.draft?.title || detailTitle.value
  form.value = {
    title: data.draft?.title ?? '',
    description: data.draft?.description ?? '',
  }
  formVersion.value = data.draft?.version ?? 0
  blockedDraft.value = null
  showCreate.value = false
  ElMessage.success(`已创建修订 #${data.draft?.revision_no ?? '—'}，可以继续修改后重新提交`)
  void load()
}

// createRevisionDraft asks for a new draft without naming a draft to replace.
// A series that still has one answers DRAFT_EXISTS and the dialog offers the
// explicit replacement instead of replacing anything on its own.
async function createDraft() {
  if (!detail.value) return
  creating.value = true
  createError.value = ''
  try {
    const data = await createRevision(detail.value.id, { baseRevisionId: baseRevisionId.value })
    applyCreated(data)
  } catch (error) {
    if (error instanceof ApiError && error.reason === 'DRAFT_EXISTS' && editableDraft.value) {
      // The draft the operator sees is the one the server refused to
      // overwrite; its identity and version are sent only after a
      // confirmation.
      blockedDraft.value = {
        id: editableDraft.value.id,
        revisionNo: editableDraft.value.revision_no,
        version: editableDraft.value.version,
      }
    } else if (error instanceof ApiError && error.reason === 'REVISION_CONFLICT') {
      createError.value = '草稿已被他人修改，请刷新后重新确认要替换的版本'
    } else {
      createError.value = describeError(error)
    }
  } finally {
    creating.value = false
  }
}

// replaceDraft replaces the draft that blocked the creation. The operator
// confirmed this exact draft and version, so both travel with the request.
async function replaceDraft() {
  if (!detail.value || !blockedDraft.value) return
  creating.value = true
  createError.value = ''
  try {
    const data = await createRevision(detail.value.id, {
      baseRevisionId: baseRevisionId.value,
      replaceDraftRevisionId: blockedDraft.value.id,
      replaceDraftVersion: blockedDraft.value.version,
    })
    applyCreated(data)
  } catch (error) {
    if (error instanceof ApiError && error.reason === 'REVISION_CONFLICT') {
      // The draft moved on; the operator refreshes instead of replacing a
      // version they never saw.
      blockedDraft.value = null
      createError.value = '被替换的草稿已变化，已刷新为你看到的最新版本，请重新确认'
      await refreshDetail()
    } else {
      createError.value = describeError(error)
    }
  } finally {
    creating.value = false
  }
}

// refreshDetail reloads the opened series without closing the dialog.
async function refreshDetail() {
  if (!detail.value) return
  try {
    const data = await getSeries(detail.value.id)
    detail.value = data
    formVersion.value = data.draft?.version ?? 0
    baseRevisionId.value = data.draft?.id ?? baseRevisionId.value
  } catch (error) {
    createError.value = describeError(error)
  }
}

// openUnpublish reads the current publication before showing the dialog: the
// version that travels with the request is the one the operator confirms.
async function openUnpublish() {
  if (!detail.value) return
  unpublishError.value = ''
  unpublishReason.value = ''
  try {
    const publication = await getPublication(detail.value.id)
    if (publication.status !== 'published') {
      listError.value = '当前没有在架版本，无法下架'
      return
    }
    unpublishVersion.value = publication.version
    unpublishTarget.value = detailTitle.value
    showUnpublish.value = true
  } catch (error) {
    listError.value = describeError(error)
  }
}

async function confirmUnpublish() {
  if (!detail.value) return
  const reason = unpublishReason.value.trim()
  if (!reason) {
    unpublishError.value = '请填写下架原因（不超过200字）'
    return
  }
  unpublishing.value = true
  unpublishError.value = ''
  try {
    await unpublishSeries(detail.value.id, unpublishVersion.value, reason)
    showUnpublish.value = false
    ElMessage.success('已下架：公开页面不再展示，新的播放会话会被拒绝')
    await load()
    await refreshDetail()
  } catch (error) {
    if (error instanceof ApiError && error.code === 409) {
      // Not retried silently: the operator sees the new version and confirms
      // again, and the reason stays in the form.
      unpublishError.value =
        '版本已变化（可能已被他人下架或重新发布），已刷新在架版本，请核对后再次确认'
      try {
        const publication = await getPublication(detail.value.id)
        unpublishVersion.value = publication.version
      } catch {
        // Keep the version we had; the next attempt will report the conflict.
      }
    } else {
      unpublishError.value = describeError(error)
    }
  } finally {
    unpublishing.value = false
  }
}

onMounted(() => {
  canWrite.value = can('content_write')
  canPublish.value = can('publish')
  void load()
})
</script>

<template>
  <main class="content">
    <h1>内容管理</h1>
    <section class="list">
      <div class="toolbar">
        <ElButton v-if="canWrite" type="primary" :loading="creatingWork" @click="openNewWork">
          新建作品
        </ElButton>
        <ElInput
          v-model="keyword"
          placeholder="按标题或简介搜索"
          clearable
          class="search"
          @keyup.enter="load(true)"
          @clear="load(true)"
        />
        <ElButton :loading="loading" @click="load(true)">搜索</ElButton>
      </div>
      <ElAlert v-if="listError" type="error" :closable="false" :title="listError" show-icon />
      <ElTable v-loading="loading" :data="items" empty-text="还没有内容" @row-click="open">
        <ElTableColumn prop="title" label="标题" min-width="180" show-overflow-tooltip />
        <ElTableColumn prop="revision_no" label="草稿" width="80" />
        <ElTableColumn prop="revision_state" label="草稿状态" width="120" />
        <ElTableColumn
          prop="published"
          label="在架版本"
          width="110"
          :formatter="(row: SeriesItem) => (row.published ? '已发布' : '未发布')"
        />
        <ElTableColumn prop="episode_count" label="集数" width="80" />
      </ElTable>
      <div class="pager">
        <span>共 {{ total }} 条</span>
        <ElPagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :total="total"
          :page-sizes="[10, 20, 50]"
          layout="prev, pager, next"
          @current-change="load()"
        />
      </div>
    </section>

    <section v-if="detail" class="detail">
      <h2>{{ detailTitle }}</h2>
      <p class="muted">
        工作修订 #{{ draft?.revision_no ?? '—' }}（{{ draft?.state ?? '无' }}，版本
        {{ draft?.version ?? '—' }}） · 在架版本 #{{ published?.revision_no ?? '—' }}（{{
          published?.state ?? '未发布'
        }}）
      </p>
      <div class="actions">
        <ElButton v-if="canWrite" type="primary" plain :loading="creating" @click="openCreate">
          新建修订
        </ElButton>
        <!-- The next steps of the same work keep their identifiers in the
             address, so the operator does not copy them between screens. -->
        <RouterLink
          v-if="canWrite && editableDraft"
          :to="{
            path: '/uploads',
            query: { revision: editableDraft.id, version: editableDraft.version },
          }"
        >
          <ElButton>上传分集与封面</ElButton>
        </RouterLink>
        <RouterLink
          v-if="canWrite && editableDraft"
          :to="{
            path: '/media',
            query: { revision: editableDraft.id, version: editableDraft.version },
          }"
        >
          <ElButton>提交素材</ElButton>
        </RouterLink>
        <ElButton
          v-if="canPublish && published"
          type="danger"
          plain
          :loading="unpublishing"
          @click="openUnpublish"
        >
          下架
        </ElButton>
      </div>
      <ElAlert v-if="conflict" type="warning" :closable="false" :title="conflict" show-icon />
      <ElAlert v-if="detailError" type="error" :closable="false" :title="detailError" show-icon />
      <div v-if="editableDraft" class="form">
        <label>标题<ElInput v-model="form.title" /></label>
        <label>简介<ElInput v-model="form.description" type="textarea" :rows="3" /></label>
        <ElButton type="primary" :loading="saving" @click="save">保存草稿</ElButton>
      </div>
      <p v-else class="muted">
        当前修订不能直接编辑：请从它新建修订，修改后重新提交审核。旧修订与审核记录保持不变。
      </p>
    </section>

    <ElDialog v-model="showUnpublish" title="下架作品" width="480px">
      <p class="muted">
        下架对象：<strong>{{ unpublishTarget }}</strong
        >。下架后在架版本退出公开页面，新的播放会话与旧会话刷新都会被拒绝；已发布版本与审核记录保留。
      </p>
      <label class="field">
        下架原因（必填，会写入审计）
        <ElInput
          v-model="unpublishReason"
          type="textarea"
          :rows="3"
          maxlength="200"
          show-word-limit
          placeholder="例如：授权到期、内容有误需要重新处理"
        />
      </label>
      <ElAlert
        v-if="unpublishError"
        type="error"
        :closable="false"
        :title="unpublishError"
        show-icon
      />
      <template #footer>
        <ElButton @click="showUnpublish = false">取消</ElButton>
        <ElButton type="danger" :loading="unpublishing" @click="confirmUnpublish"
          >确认下架</ElButton
        >
      </template>
    </ElDialog>

    <ElDialog
      :model-value="showNewWork"
      title="新建作品（人工创建）"
      width="600px"
      :close-on-click-modal="false"
      :before-close="guardNewWorkClose"
      @update:model-value="guardNewWorkClose(() => {})"
    >
      <p class="muted">
        人工创建不依赖来源，不会写入来源或外部ID，也不会下载、处理或送审。保存后是工作草稿，
        分集需要逐个上传视频，封面单独上传，再按现有流程提交审核与发布。
      </p>
      <div class="form">
        <label>
          标题（必填，最多120字）
          <ElInput v-model="newWork.title" maxlength="120" show-word-limit />
        </label>
        <label>
          简介（最多5000字）
          <ElInput v-model="newWork.description" type="textarea" :rows="3" maxlength="5000" />
        </label>
        <span class="muted">分集原始序号（至少一集，不可重复；按填写顺序保存，不重排）</span>
        <div class="episodes-editor">
          <div v-for="(_, index) in newWorkEpisodes" :key="index" class="episode-row">
            <ElInputNumber
              v-model="newWorkEpisodes[index]"
              :min="1"
              :max="9999"
              :step="1"
              step-strictly
              controls-position="right"
            />
            <ElButton :disabled="newWorkEpisodes.length <= 1" @click="removeEpisode(index)">
              删除
            </ElButton>
          </div>
          <ElButton plain @click="addEpisode">添加一集</ElButton>
        </div>
        <span class="muted">共 {{ newWorkEpisodes.length }} 集</span>
      </div>
      <ElAlert v-if="newWorkError" type="error" :closable="false" :title="newWorkError" show-icon />
      <template #footer>
        <ElButton @click="guardNewWorkClose(() => {})">取消</ElButton>
        <ElButton type="primary" :loading="creatingWork" @click="submitNewWork">保存草稿</ElButton>
      </template>
    </ElDialog>

    <ElDialog
      :model-value="showCloseConfirm"
      title="关闭会丢失未保存内容"
      width="420px"
      @update:model-value="showCloseConfirm = false"
    >
      <p>关闭会丢失未保存内容。继续编辑保留；放弃修改会清空并关闭。</p>
      <template #footer>
        <ElButton @click="showCloseConfirm = false">继续编辑</ElButton>
        <ElButton type="danger" :disabled="creatingWork" @click="discardNewWork">放弃修改</ElButton>
      </template>
    </ElDialog>

    <ElDialog v-model="showCreate" title="新建修订" width="560px">
      <p class="muted">
        新修订继承标题、简介、封面、标签、选集与已冻结素材；不继承审核通过、发布资格和已确认的授权，
        提交处理时需要重新选择授权依据。
      </p>
      <div class="form">
        <span class="muted">继承来源</span>
        <ElRadioGroup v-model="baseRevisionId">
          <ElRadio v-for="option in baseOptions" :key="option.id" :value="option.id">
            {{ option.label }}
          </ElRadio>
        </ElRadioGroup>
      </div>
      <ElAlert v-if="createError" type="error" :closable="false" :title="createError" show-icon />
      <ElAlert v-if="blockedDraft" type="warning" :closable="false" show-icon>
        可编辑草稿 #{{ blockedDraft.revisionNo }}（版本 {{ blockedDraft.version }}）仍在使用中，
        服务器没有覆盖它。确认用新修订替换这条草稿后才会创建。
        <div class="conflict-actions">
          <ElButton type="warning" :loading="creating" @click="replaceDraft">
            替换草稿 #{{ blockedDraft.revisionNo }}（版本 {{ blockedDraft.version }}）
          </ElButton>
        </div>
      </ElAlert>
      <template #footer>
        <ElButton @click="showCreate = false">取消</ElButton>
        <ElButton type="primary" :loading="creating" @click="createDraft">创建修订</ElButton>
      </template>
    </ElDialog>
  </main>
</template>

<style scoped>
.content {
  max-width: none;
  margin: 0;
  padding: var(--sp-5);
  display: grid;
  gap: var(--sp-4);
}
.topbar {
  display: flex;
  gap: 14px;
  margin-bottom: 16px;
  flex-wrap: wrap;
}
.toolbar {
  display: flex;
  align-items: center;
  gap: var(--sp-2);
  padding: var(--sp-3) var(--sp-4);
  border-bottom: 1px solid var(--border);
  margin: 0;
}
.search {
  width: 260px;
}
.muted {
  color: var(--ink-400);
  font-size: var(--fs-12);
}
.pager {
  display: flex;
  gap: var(--sp-3);
  align-items: center;
  padding: var(--sp-3) var(--sp-4);
  border-top: 1px solid var(--border);
  margin: 0;
}
.detail {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--r-lg);
  padding: var(--sp-4);
}
.form {
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-width: 640px;
}
.actions {
  display: flex;
  gap: 8px;
  margin-bottom: 10px;
}
.conflict-actions {
  margin-top: 8px;
}
.episodes-editor {
  display: flex;
  flex-direction: column;
  gap: 8px;
  align-items: flex-start;
}
.episode-row {
  display: flex;
  gap: 8px;
  align-items: center;
}
</style>
