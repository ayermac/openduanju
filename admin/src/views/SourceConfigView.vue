<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  ElAlert,
  ElButton,
  ElCard,
  ElDialog,
  ElInput,
  ElInputNumber,
  ElMessage,
  ElOption,
  ElSelect,
  ElTable,
  ElTableColumn,
  ElTag,
} from 'element-plus'
import {
  ApiError,
  listChannels,
  listSourceEntries,
  listSources,
  setChannelStatus,
  testChannel,
  updateChannel,
  updateSource,
  type ChannelConfig,
  type ChannelProbe,
  type SourceConfig,
  type SourceEntry,
} from '@/api/source'
import { can } from '@/api/session'

const sources = ref<SourceConfig[]>([])
const channels = ref<ChannelConfig[]>([])
const entries = ref<SourceEntry[]>([])
const loading = ref(false)
const error = ref('')
const notice = ref('')

// The selected channel and its editable form.
const selected = ref<ChannelConfig | null>(null)
const editing = ref(false)
const saving = ref(false)
const form = ref({
  name: '',
  entryKind: 'rank',
  entryCode: '',
  baseUrl: '',
  allowedHosts: '',
  ratePerMinute: 30,
  timeoutSeconds: 20,
})
const formError = ref('')

const probe = ref<ChannelProbe | null>(null)
const probing = ref(false)

const canWrite = ref(false)

// asSource and asChannel read the table row without losing its type.
function asSource(row: unknown): SourceConfig {
  return row as SourceConfig
}

function asChannel(row: unknown): ChannelConfig {
  return row as ChannelConfig
}

function describe(err: unknown): string {
  if (err instanceof ApiError) return `${err.reason}：${err.message}`
  return err instanceof Error ? err.message : '请求失败'
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [sourceSet, channelSet, entrySet] = await Promise.all([
      listSources(),
      listChannels(),
      listSourceEntries(),
    ])
    sources.value = sourceSet.items ?? []
    channels.value = channelSet.items ?? []
    entries.value = entrySet.items ?? []
  } catch (err) {
    error.value = describe(err)
  } finally {
    loading.value = false
  }
}

function openChannel(channel: ChannelConfig) {
  selected.value = channel
  probe.value = null
  formError.value = ''
  form.value = {
    name: channel.name,
    entryKind: channel.entry_kind,
    entryCode: channel.entry_code,
    baseUrl: channel.base_url,
    allowedHosts: channel.allowed_hosts,
    ratePerMinute: channel.rate_per_minute,
    timeoutSeconds: channel.timeout_seconds,
  }
  editing.value = true
}

// availableEntries are the entries the server registered for this channel.
function availableEntries() {
  if (!selected.value) return []
  return entries.value.filter((entry) => entry.adapter_key === selected.value?.adapter_key)
}

async function save() {
  if (!selected.value) return
  saving.value = true
  formError.value = ''
  try {
    const updated = await updateChannel({
      id: selected.value.id,
      expectedVersion: selected.value.version,
      name: form.value.name,
      entryKind: form.value.entryKind,
      entryCode: form.value.entryCode,
      baseUrl: form.value.baseUrl,
      allowedHosts: form.value.allowedHosts,
      ratePerMinute: form.value.ratePerMinute,
      timeoutSeconds: form.value.timeoutSeconds,
    })
    selected.value = updated
    editing.value = false
    ElMessage.success('通道配置已保存，新的配置只对之后创建的运行生效')
    await load()
  } catch (err) {
    // A stale version keeps the operator's input so nothing has to be typed
    // again after a refresh.
    formError.value = describe(err)
  } finally {
    saving.value = false
  }
}

async function togglePause(channel: ChannelConfig) {
  error.value = ''
  try {
    const next = channel.status === 'paused' ? 'enabled' : 'paused'
    await setChannelStatus({ id: channel.id, expectedVersion: channel.version, status: next })
    ElMessage.success(next === 'paused' ? '通道已暂停：新的触发与定时触发都会被拒绝' : '通道已启用')
    await load()
  } catch (err) {
    error.value = describe(err)
  }
}

async function pauseSource(source: SourceConfig) {
  error.value = ''
  try {
    const next = source.status === 'paused' ? 'enabled' : 'paused'
    await updateSource({
      id: source.id,
      expectedVersion: source.version,
      name: source.name,
      status: next,
    })
    ElMessage.success(next === 'paused' ? '来源已暂停' : '来源已启用')
    await load()
  } catch (err) {
    error.value = describe(err)
  }
}

// runProbe performs one explicit request. Saving the configuration never
// calls the source.
async function runProbe(channel: ChannelConfig) {
  probing.value = true
  probe.value = null
  error.value = ''
  try {
    probe.value = await testChannel(channel.id)
  } catch (err) {
    error.value = describe(err)
  } finally {
    probing.value = false
  }
}

onMounted(async () => {
  canWrite.value = can('ingestion_write')
  await load()
})
</script>

<template>
  <main class="sources">
    <p v-if="error" class="error">{{ error }}</p>
    <p v-if="notice" class="notice">{{ notice }}</p>

    <ElCard shadow="never" class="panel">
      <template #header>
        <div class="panel-title">
          <strong>01 来源</strong>
          <span>来源代表内容归属；暂停只停新的采集，不影响已入库候选与在架内容</span>
        </div>
      </template>
      <ElTable :data="sources" v-loading="loading">
        <ElTableColumn prop="code" label="标识" width="140" />
        <ElTableColumn prop="name" label="名称" min-width="140" show-overflow-tooltip />
        <ElTableColumn label="状态" width="110">
          <template #default="{ row }">
            <ElTag :type="row.status === 'paused' ? 'warning' : 'success'">
              {{ row.status === 'paused' ? '已暂停' : '启用中' }}
            </ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn prop="version" label="版本" width="80" />
        <ElTableColumn label="操作" width="120">
          <template #default="{ row }">
            <ElButton v-if="canWrite" size="small" @click="pauseSource(asSource(row))">
              {{ row.status === 'paused' ? '启用' : '暂停' }}
            </ElButton>
          </template>
        </ElTableColumn>
      </ElTable>
    </ElCard>

    <ElCard shadow="never" class="panel">
      <template #header>
        <div class="panel-title">
          <strong>02 通道</strong>
          <span>入口由服务端声明；保存配置不会自动外呼，测试连接是显式操作</span>
        </div>
      </template>
      <ElTable :data="channels" v-loading="loading">
        <ElTableColumn prop="source_code" label="来源" width="120" />
        <ElTableColumn prop="code" label="标识" width="140" />
        <ElTableColumn prop="name" label="名称" min-width="140" show-overflow-tooltip />
        <ElTableColumn prop="adapter_key" label="适配器" width="140" />
        <ElTableColumn label="入口" width="160">
          <template #default="{ row }">{{ row.entry_kind }} / {{ row.entry_code }}</template>
        </ElTableColumn>
        <ElTableColumn label="限频/超时" width="130">
          <template #default="{ row }"
            >{{ row.rate_per_minute }} 次/分 · {{ row.timeout_seconds }}s</template
          >
        </ElTableColumn>
        <ElTableColumn label="状态" width="120">
          <template #default="{ row }">
            <ElTag :type="row.paused ? 'warning' : 'success'">
              {{
                row.paused ? (row.paused_cause === 'source' ? '来源已暂停' : '已暂停') : '启用中'
              }}
            </ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn prop="version" label="版本" width="80" />
        <ElTableColumn label="操作" width="210">
          <template #default="{ row }">
            <ElButton size="small" @click="openChannel(asChannel(row))">查看</ElButton>
            <ElButton
              v-if="canWrite"
              size="small"
              :loading="probing"
              @click="runProbe(asChannel(row))"
            >
              测试连接
            </ElButton>
            <ElButton v-if="canWrite" size="small" @click="togglePause(asChannel(row))">
              {{ row.status === 'paused' ? '启用' : '暂停' }}
            </ElButton>
          </template>
        </ElTableColumn>
      </ElTable>
    </ElCard>

    <ElCard v-if="probe" shadow="never" class="panel">
      <div class="probe">
        <strong>测试连接</strong>
        <p v-if="probe.ok">
          成功：入口 {{ probe.channel }}，耗时 {{ probe.latency_ms }}ms，返回
          {{ probe.items }} 条，共 {{ probe.total_pages }} 页。
        </p>
        <p v-else>失败：{{ probe.error_code }} {{ probe.error_message }}</p>
      </div>
    </ElCard>

    <ElDialog v-model="editing" title="通道配置" width="620px">
      <div v-if="selected" class="form">
        <label>
          名称
          <ElInput v-model="form.name" maxlength="80" />
        </label>
        <label>
          入口（服务端声明）
          <ElSelect v-model="form.entryCode">
            <ElOption
              v-for="entry in availableEntries()"
              :key="`${entry.kind}-${entry.channel}`"
              :label="`${entry.label}（${entry.kind}/${entry.channel}）`"
              :value="entry.channel"
            />
          </ElSelect>
        </label>
        <label>
          源站地址（HTTPS，不含路径）
          <ElInput v-model="form.baseUrl" placeholder="https://hongguoduanju.com" />
        </label>
        <label>
          允许的主机（逗号分隔）
          <ElInput v-model="form.allowedHosts" placeholder="hongguoduanju.com" />
        </label>
        <div class="inline">
          <label>
            限频（次/分钟）
            <ElInputNumber v-model="form.ratePerMinute" :min="1" :max="600" />
          </label>
          <label>
            超时（秒）
            <ElInputNumber v-model="form.timeoutSeconds" :min="5" :max="120" />
          </label>
        </div>
        <p class="muted">
          私有配置只保存引用名，页面不回显凭据。当前：{{
            selected?.has_secret ? '已配置引用' : '未配置引用'
          }}。
        </p>
      </div>
      <ElAlert v-if="formError" type="error" :closable="false" :title="formError" show-icon />
      <template #footer>
        <ElButton @click="editing = false">关闭</ElButton>
        <ElButton v-if="canWrite" type="primary" :loading="saving" @click="save">保存</ElButton>
      </template>
    </ElDialog>
  </main>
</template>

<style scoped>
.sources {
  max-width: 1080px;
  margin: 0 auto;
  padding: 16px;
}
.topbar {
  display: flex;
  align-items: baseline;
  gap: 12px;
  margin-bottom: 16px;
}
.brand {
  font-weight: 700;
}
.tagline {
  color: #777;
  font-size: 13px;
}
.panel {
  margin-bottom: 16px;
}
.panel-title {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.panel-title span {
  color: #777;
  font-size: 12px;
}
.form {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.inline {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
}
.muted {
  color: #777;
  font-size: 12px;
}
.error {
  color: #c0392b;
}
.notice {
  color: #2f6fed;
}
.probe {
  margin-top: 12px;
  padding: 8px 10px;
  border: 1px solid #eee;
  border-radius: 4px;
  background: #fafafa;
}
</style>
