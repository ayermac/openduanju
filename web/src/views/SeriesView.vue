<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import EpisodeGrid from '@/components/EpisodeGrid.vue'
import SiteHeader from '@/components/SiteHeader.vue'
import StatePanel from '@/components/StatePanel.vue'
import {
  WebError,
  fetchDetail,
  openPlayback,
  refreshPlayback,
  type EpisodeCard,
  type PlaybackTicket,
  type SeriesDetail,
} from '@/api/web'
import { chooseEpisode, loadProgress, saveProgress, seriesProgress } from '@/stores/history'

const route = useRoute()
const router = useRouter()
// The series identity is read once: a navigation away clears the route param
// before the page saves its last position, and a record without an identity
// would show up in the device history as a second, unusable entry.
const seriesId = ref('')
const detail = ref<SeriesDetail | null>(null)
const episode = ref<EpisodeCard | null>(null)
const error = ref('')
const errorKind = ref<'failed' | 'unavailable'>('failed')
const loading = ref(true)
const notice = ref('')
const refreshing = ref(false)
// A failed media load is a player-level problem: it must not replace the page.
const mediaError = ref('')
const playing = ref(false)
const position = ref(0)
const duration = ref(0)
const rate = ref(1)
const video = ref<HTMLVideoElement | null>(null)
const switchingId = ref('')

let ticket: PlaybackTicket | null = null
let pending: AbortController | null = null
let lastSaved = 0
let heartbeat: number | undefined
let noticeTimer: number | undefined

const episodes = computed(() => detail.value?.episodes ?? [])
const index = computed(() =>
  episodes.value.findIndex((item) => item.episode_id === episode.value?.episode_id),
)

// The player container follows the media, not a fixed 16:9: a vertical episode
// keeps its own shape and is centred on black instead of being letterboxed into
// a small landscape window.
const portrait = computed(() => {
  const item = episode.value
  return Boolean(item && item.width > 0 && item.height > 0 && item.height > item.width)
})
const aspect = computed(() => {
  const item = episode.value
  if (!item || item.width <= 0 || item.height <= 0) return '16 / 9'
  return `${item.width} / ${item.height}`
})

function describe(err: unknown): string {
  if (err instanceof WebError) return err.message
  return err instanceof Error ? err.message : '请求失败'
}

function fail(err: unknown) {
  error.value = describe(err)
  errorKind.value = err instanceof WebError && err.code === 410 ? 'unavailable' : 'failed'
  if (errorKind.value === 'unavailable') stopPlayback()
}

// showNotice keeps a transient message short-lived: a resume hint or a refresh
// line is not page content and must not stay under the player.
function showNotice(message: string) {
  notice.value = message
  if (noticeTimer !== undefined) window.clearTimeout(noticeTimer)
  noticeTimer = window.setTimeout(() => {
    notice.value = ''
    noticeTimer = undefined
  }, 6000)
}

// abortPending cancels an in-flight session request, so switching episodes
// does not race with the old one.
function abortPending() {
  pending?.abort()
  pending = null
}

async function load() {
  seriesId.value = String(route.params.id)
  error.value = ''
  notice.value = ''
  loading.value = true
  try {
    detail.value = await fetchDetail(seriesId.value)
    document.title = `${detail.value.series.title} · 映刻`
    // The page can render now: the player element must exist before the first
    // session is loaded, otherwise the source has nowhere to go.
    loading.value = false
    const list = detail.value.episodes ?? []
    if (!list.length) {
      error.value = '当前发布版本没有可播放的分集'
      return
    }
    // The link may name the episode to open; otherwise this device continues
    // the episode it watched last. A stored episode that is no longer
    // published is reported and never replaced by another episode.
    const stored = seriesProgress(seriesId.value)
    const choice = chooseEpisode(list, {
      requestedId: typeof route.query.episode === 'string' ? route.query.episode : undefined,
      storedId: stored?.episodeId,
    })
    if (!choice) {
      error.value = '当前发布版本没有可播放的分集'
      return
    }
    if (choice.missing) {
      showNotice('上次观看的分集已不在当前发布版本中，请重新选择分集')
    } else if (choice.resumed) {
      showNotice(`已从第 ${choice.episode.number} 集继续播放`)
    }
    await playEpisode(choice.episode, choice.resumed)
  } catch (err) {
    fail(err)
  } finally {
    loading.value = false
  }
}

// playEpisode opens a session for one episode and loads the signed URL.
async function playEpisode(target: EpisodeCard, restore: boolean) {
  abortPending()
  mediaError.value = ''
  const progress = restore ? loadProgress(seriesId.value, target.episode_id) : null
  const controller = new AbortController()
  pending = controller
  switchingId.value = target.episode_id
  try {
    const issued = await openPlayback(seriesId.value, target.episode_id)
    if (controller.signal.aborted) return
    ticket = issued
    episode.value = target
    // The address keeps the episode, so a reload or a shared link opens the
    // same one instead of falling back to the first.
    if (String(route.query.episode ?? '') !== target.episode_id) {
      void router.replace({ query: { ...route.query, episode: target.episode_id } })
    }
    // The element only exists after the section is rendered.
    await nextTick()
    if (!video.value) return
    video.value.src = issued.url
    video.value.load()
    if (progress) {
      progressWasPaused.value = progress.paused
      rate.value = progress.rate || 1
      video.value.playbackRate = rate.value
      resumeAt.value = progress.position
    }
  } catch (err) {
    if (controller.signal.aborted) return
    fail(err)
  } finally {
    if (pending === controller) pending = null
    if (switchingId.value === target.episode_id) switchingId.value = ''
  }
}

// stopPlayback ends the playback and drops the media source, so the native
// controls cannot restart a stream whose credential is gone.
function stopPlayback() {
  const element = video.value
  if (element) {
    element.pause()
    element.removeAttribute('src')
    element.load()
  }
  ticket = null
}

// resumeAt holds the position the device stored for the current episode.
const resumeAt = ref(0)

function onLoadedMetadata() {
  if (!video.value) return
  duration.value = video.value.duration || 0
  if (resumeAt.value > 0 && (duration.value === 0 || resumeAt.value < duration.value)) {
    video.value.currentTime = resumeAt.value
  }
  // The restored position is the reference the next checkpoint counts from.
  lastSaved = video.value.currentTime
  if (video.value.playbackRate !== rate.value) video.value.playbackRate = rate.value
  if (!progressWasPaused.value) {
    void video.value.play().catch(() => undefined)
  }
  resumeAt.value = 0
}

// progressWasPaused keeps the paused state of the stored progress.
const progressWasPaused = ref(true)

// recoverMedia handles a media error: the usual cause is a credential that
// expired while the player was paused. The source is renewed once and the
// position is restored; a credential that cannot be renewed stops the player
// instead of retrying in a loop.
let recovering = false
async function recoverMedia() {
  if (!video.value || !ticket || recovering) return
  if (!ticket || new Date(ticket.expires_at).getTime() - Date.now() > 60_000) {
    mediaError.value = '视频加载失败，请重新进入本集或刷新页面'
    return
  }
  recovering = true
  try {
    const wasPlaying = !video.value.paused
    const renewed = await ensureFreshTicket(true)
    if (renewed) {
      mediaError.value = ''
      if (wasPlaying) void video.value?.play().catch(() => undefined)
    }
  } finally {
    recovering = false
  }
}

// ensureFreshTicket refreshes the signed URL before it expires.
async function ensureFreshTicket(force = false): Promise<boolean> {
  if (!ticket) return false
  const remaining = new Date(ticket.expires_at).getTime() - Date.now()
  if (!force && remaining > 60_000) return true
  const controller = new AbortController()
  pending = controller
  const element = video.value
  if (!element) return false
  const current = element.currentTime
  const currentRate = element.playbackRate
  const wasPaused = element.paused
  refreshing.value = true
  try {
    const refreshed = await refreshPlayback(ticket.token)
    if (controller.signal.aborted) return false
    ticket = refreshed
    // The new URL replaces the source, so the position, the speed and the
    // paused state are restored once the media is ready again.
    const restore = () => {
      element.currentTime = current
      element.playbackRate = currentRate
      if (!wasPaused) void element.play().catch(() => undefined)
    }
    element.addEventListener('loadedmetadata', restore, { once: true })
    element.src = refreshed.url
    element.load()
    return true
  } catch (err) {
    // A credential that can no longer be refreshed stops the playback
    // instead of retrying in a loop.
    if (err instanceof WebError && err.code === 410) {
      error.value = '播放入口已失效，内容可能已下架'
      errorKind.value = 'unavailable'
      stopPlayback()
    } else {
      error.value = describe(err)
    }
    return false
  } finally {
    refreshing.value = false
    if (pending === controller) pending = null
  }
}

function onTimeUpdate() {
  if (!video.value) return
  position.value = video.value.currentTime
  duration.value = video.value.duration || duration.value
  // Seeking also raises timeupdate: a position change of a paused player is
  // not playback and must not be stored as a playing record.
  if (video.value.paused) return
  if (video.value.currentTime - lastSaved > 5) {
    lastSaved = video.value.currentTime
    persist(false)
  }
}

function onPlay() {
  playing.value = true
}

function onPause() {
  playing.value = false
  persist(true)
}

function onRateChange() {
  if (!video.value) return
  rate.value = video.value.playbackRate
}

function setRate(event: Event) {
  const value = Number((event.target as HTMLSelectElement).value)
  rate.value = value
  if (!video.value) return
  video.value.playbackRate = value
  // The chosen speed is part of what the device remembers, but only a media
  // that has its metadata can report a position: saving earlier would store a
  // zero position for an episode the viewer has not started yet.
  if (video.value.readyState >= HTMLMediaElement.HAVE_METADATA) {
    persist(video.value.paused)
  }
}

function persist(paused: boolean) {
  if (!episode.value || !detail.value || !seriesId.value) return
  saveProgress({
    seriesId: seriesId.value,
    seriesTitle: detail.value.series.title,
    episodeId: episode.value.episode_id,
    episodeNumber: episode.value.number,
    position: video.value?.currentTime ?? position.value,
    paused,
    rate: rate.value,
    updatedAt: Date.now(),
  })
}

async function switchTo(target: EpisodeCard | undefined) {
  if (!target) return
  // A manual switch is not a continuation: the note of the automatic resume
  // no longer describes what is playing.
  notice.value = ''
  persist(!playing.value)
  await playEpisode(target, true)
}

function previous() {
  if (index.value <= 0) return
  void switchTo(episodes.value[index.value - 1])
}

function next() {
  if (index.value < 0 || index.value >= episodes.value.length - 1) return
  void switchTo(episodes.value[index.value + 1])
}

watch(
  () => route.params.id,
  () => {
    void load()
  },
)

onMounted(() => {
  void load()
  // The credential is short lived: the player renews it while it plays, so
  // the session never breaks in the middle of an episode.
  heartbeat = window.setInterval(() => {
    if (ticket && video.value && !video.value.paused) {
      void ensureFreshTicket()
    }
  }, 5000)
})

onBeforeUnmount(() => {
  persist(!playing.value)
  abortPending()
  if (heartbeat !== undefined) window.clearInterval(heartbeat)
  if (noticeTimer !== undefined) window.clearTimeout(noticeTimer)
})
</script>

<template>
  <div class="series with-rail">
    <SiteHeader back />

    <main class="wrap">
      <StatePanel v-if="loading" variant="loading" />
      <StatePanel
        v-else-if="error"
        :variant="errorKind === 'unavailable' ? 'empty' : 'error'"
        :title="errorKind === 'unavailable' ? '这部作品当前不可观看' : '内容暂时取不到'"
        :hint="
          errorKind === 'unavailable'
            ? '作品可能已下架或授权失效，可以回到首页看看其他已发布内容。'
            : '网络或服务异常，稍后重试。'
        "
        :error="errorKind === 'unavailable' ? '' : error"
        @retry="load"
      >
        <template #actions>
          <RouterLink v-if="errorKind === 'unavailable'" class="btn primary" to="/"
            >返回首页</RouterLink
          >
        </template>
      </StatePanel>

      <template v-else-if="detail">
        <div class="watch">
          <section aria-label="播放器">
            <div class="stage" :class="{ portrait }">
              <video
                v-if="episode"
                ref="video"
                class="media"
                :style="{ aspectRatio: aspect }"
                controls
                playsinline
                preload="metadata"
                @error="recoverMedia"
                @loadedmetadata="onLoadedMetadata"
                @timeupdate="onTimeUpdate"
                @play="onPlay"
                @pause="onPause"
                @ratechange="onRateChange"
              />
            </div>

            <!-- 传输控件只有原生一套；这里只放原生控件没有的剧集导航与倍速。 -->
            <div class="nav-row">
              <button class="nav-btn" type="button" :disabled="index <= 0" @click="previous">
                上一集
              </button>
              <button
                class="nav-btn"
                type="button"
                :disabled="index < 0 || index >= episodes.length - 1"
                @click="next"
              >
                下一集
              </button>
              <span v-if="episode" class="ep-label">第 {{ episode.number }} 集</span>
              <label class="rate">
                <span>倍速</span>
                <select :value="rate" @change="setRate">
                  <option :value="0.5">0.5x</option>
                  <option :value="1">1x</option>
                  <option :value="1.25">1.25x</option>
                  <option :value="1.5">1.5x</option>
                  <option :value="2">2x</option>
                </select>
              </label>
            </div>

            <p v-if="mediaError" class="notice error-note" role="alert">
              {{ mediaError }}
              <button class="link-btn" type="button" @click="recoverMedia">重试</button>
            </p>
            <p v-if="refreshing" class="notice" role="status">播放入口即将过期，正在重新获取…</p>
            <p v-if="notice" class="notice" role="status">{{ notice }}</p>
          </section>

          <aside class="side">
            <div class="panel info">
              <h1>{{ detail.series.title }}</h1>
              <div class="tags">
                <span v-if="detail.series.category" class="chip">{{ detail.series.category }}</span>
                <span class="chip plain">{{ episodes.length }} 集可看</span>
              </div>
              <p v-if="detail.series.description" class="desc">{{ detail.series.description }}</p>
            </div>
            <div class="panel">
              <div class="panel-head">
                <h2>选集</h2>
                <span>共 {{ episodes.length }} 集</span>
              </div>
              <EpisodeGrid
                :episodes="episodes"
                :current-id="episode?.episode_id ?? ''"
                :loading-id="switchingId"
                @select="switchTo"
              />
            </div>
          </aside>
        </div>
      </template>
    </main>

    <footer class="site-foot wrap">
      <RouterLink to="/">← 返回首页</RouterLink>
      <span>播放地址短时有效，进度只保存在本设备</span>
    </footer>
  </div>
</template>

<style scoped>
.wrap {
  padding-top: var(--sp-5);
}
.watch {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 340px;
  gap: var(--sp-5);
  align-items: start;
}
.stage {
  background: #0d0f14;
  border-radius: var(--r-lg);
  overflow: hidden;
  box-shadow: var(--shadow-2);
  display: flex;
  align-items: center;
  justify-content: center;
}
.media {
  width: 100%;
  max-height: min(78vh, 720px);
  background: #0d0f14;
  display: block;
}
/* 竖屏：容器限高，画面按自身比例居中，左右留黑但不裁切 */
.stage.portrait .media {
  width: auto;
  height: min(78vh, 720px);
  max-width: 100%;
}
.nav-row {
  display: flex;
  align-items: center;
  gap: var(--sp-2);
  margin-top: var(--sp-3);
  flex-wrap: wrap;
}
.nav-btn {
  min-height: var(--touch);
  padding: 0 var(--sp-4);
  border-radius: var(--r-pill);
  border: 1px solid var(--border-strong);
  background: #ffffff12;
  color: var(--ink-900);
  font-size: var(--fs-14);
}
.nav-btn:hover:not(:disabled) {
  border-color: var(--ink-300);
}
.nav-btn:disabled {
  color: var(--ink-300);
  background: var(--surface-2);
  cursor: not-allowed;
}
.ep-label {
  color: var(--ink-600);
  font-size: var(--fs-13);
  margin-left: var(--sp-1);
}
.rate {
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  gap: var(--sp-2);
  color: var(--ink-600);
  font-size: var(--fs-13);
}
.rate select {
  height: 38px;
  padding: 0 var(--sp-2);
  border-radius: var(--r-md);
  border: 1px solid var(--border-strong);
  background: var(--surface);
  color: var(--ink-900);
  font-size: var(--fs-13);
}
.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: var(--touch);
  padding: 0 var(--sp-4);
  border-radius: var(--r-pill);
  border: 1px solid var(--brand);
  background: var(--brand);
  color: var(--brand-ink);
  font-size: var(--fs-14);
  font-weight: 700;
}
.error-note {
  background: var(--danger-weak);
  color: var(--danger);
}
.link-btn {
  margin-left: var(--sp-2);
  border: 0;
  background: none;
  color: inherit;
  font-size: var(--fs-13);
  text-decoration: underline;
}
.notice {
  margin-top: var(--sp-3);
  padding: var(--sp-2) var(--sp-3);
  border-radius: var(--r-md);
  background: var(--warn-weak);
  color: var(--warn);
  font-size: var(--fs-13);
}
.side {
  display: flex;
  flex-direction: column;
  gap: var(--sp-4);
}
.panel {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--r-lg);
  padding: var(--sp-4);
}
.info h1 {
  font-size: var(--fs-22);
}
.tags {
  display: flex;
  gap: var(--sp-2);
  margin-top: var(--sp-2);
  flex-wrap: wrap;
}
.chip {
  padding: 2px var(--sp-2);
  border-radius: var(--r-pill);
  background: var(--brand-weak);
  color: var(--brand-strong);
  font-size: var(--fs-12);
}
.chip.plain {
  background: var(--surface-2);
  color: var(--ink-600);
}
.desc {
  margin-top: var(--sp-3);
  color: var(--ink-600);
  font-size: var(--fs-13);
}
.panel-head {
  display: flex;
  align-items: baseline;
  gap: var(--sp-2);
  margin-bottom: var(--sp-3);
}
.panel-head h2 {
  font-size: var(--fs-16);
}
.panel-head span {
  color: var(--ink-400);
  font-size: var(--fs-12);
}
.site-foot {
  display: flex;
  justify-content: space-between;
  gap: var(--sp-4);
  padding: var(--sp-6) var(--sp-5) var(--sp-7);
  color: var(--ink-400);
  font-size: var(--fs-12);
}
.site-foot a:hover {
  color: var(--brand);
}

@media (max-width: 1080px) {
  .watch {
    grid-template-columns: minmax(0, 1fr);
    gap: var(--sp-4);
  }
  .stage.portrait .media {
    height: min(62vh, 560px);
  }
}
@media (max-width: 720px) {
  .wrap {
    padding-top: var(--sp-3);
  }
  .nav-btn {
    min-height: var(--touch);
    padding: 0 var(--sp-4);
  }
  .rate select {
    height: var(--touch);
  }
  .media {
    max-height: 56vh;
  }
  .stage.portrait .media {
    height: min(58vh, 480px);
  }
  .site-foot {
    flex-direction: column;
    padding: var(--sp-5) var(--sp-4);
  }
}
</style>
