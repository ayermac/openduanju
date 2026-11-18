<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import ContinueRow from '@/components/ContinueRow.vue'
import SeriesCard from '@/components/SeriesCard.vue'
import SiteHeader from '@/components/SiteHeader.vue'
import StatePanel from '@/components/StatePanel.vue'
import {
  WebError,
  fetchCategories,
  fetchSeries,
  type SeriesCard as SeriesCardData,
} from '@/api/web'
import {
  clearWatchHistory,
  recentSeries,
  removeSeriesProgress,
  type WatchProgress,
} from '@/stores/history'

const PAGE_SIZE = 24

const items = ref<SeriesCardData[]>([])
const total = ref(0)
const page = ref(1)
const categories = ref<string[]>([])
const keyword = ref('')
const query = ref('')
const category = ref('')
const loading = ref(false)
const error = ref('')
const history = ref<WatchProgress[]>([])
let listRequest = 0

const hasFilters = computed(() => keyword.value.trim() !== '' || category.value !== '')
const heading = computed(() => (category.value ? category.value : '最近发布'))
const hasMore = computed(() => items.value.length < total.value)

function describe(err: unknown): string {
  if (err instanceof WebError) return err.message
  return err instanceof Error ? err.message : '请求失败'
}

// load fetches one page of the public list. The same call serves the landing
// list, a search and a category filter, so the total and the paging stay
// honest no matter how the visitor arrived. Every call carries a request id;
// only the newest query may write the list.
async function load(reset: boolean) {
  const requestId = ++listRequest
  if (reset) {
    page.value = 1
  }
  loading.value = true
  error.value = ''
  try {
    const set = await fetchSeries({
      q: keyword.value.trim() || undefined,
      category: category.value || undefined,
      page: page.value,
      pageSize: PAGE_SIZE,
    })
    if (requestId !== listRequest) return
    const pageItems = set.items ?? []
    items.value = reset ? pageItems : [...items.value, ...pageItems]
    // A zero total is omitted from the JSON (proto3 default), so an absent
    // field means "none", not "unknown".
    total.value = set.total ?? 0
  } catch (err) {
    if (requestId !== listRequest) return
    error.value = describe(err)
    if (reset) {
      items.value = []
      total.value = 0
    } else {
      // The failed page stays current, so a retry does not silently skip it.
      page.value = Math.max(1, page.value - 1)
    }
  } finally {
    if (requestId === listRequest) loading.value = false
  }
}

async function loadCategories() {
  try {
    const set = await fetchCategories()
    categories.value = set.categories ?? []
  } catch {
    // The filter row is an aid, not the content: a failure leaves it empty and
    // the list still loads.
    categories.value = []
  }
}

watch(keyword, (next) => {
  if (next !== query.value) query.value = next
})

function submitSearch() {
  onSearch(query.value.trim())
}

function onSearch(value: string) {
  // The form is the only submit path; an unchanged query that is still in
  // flight does not need another request, while a changed query may supersede it.
  if (loading.value && keyword.value === value && category.value === '') return
  keyword.value = value
  void load(true)
}

function pickCategory(value: string) {
  category.value = category.value === value ? '' : value
  void load(true)
}

function clearFilters() {
  keyword.value = ''
  category.value = ''
  void load(true)
}

function removeHistorySeries(seriesId: string) {
  removeSeriesProgress(seriesId)
  history.value = recentSeries()
}

function clearHistory() {
  // A confirm is enough here: the action is device-local and reversible only
  // by watching again.
  if (!window.confirm('清空本机观看记录？作品和服务器记录不会被删除。')) return
  clearWatchHistory()
  history.value = recentSeries()
}

function loadMore() {
  if (loading.value || !hasMore.value) return
  page.value += 1
  void load(false)
}

onMounted(() => {
  document.title = '映刻 · 已发布短剧'
  history.value = recentSeries()
  void loadCategories()
  void load(true)
})
</script>

<template>
  <div class="home with-rail">
    <SiteHeader />

    <main class="wrap">
      <section class="hero" aria-labelledby="hero-heading">
        <p class="eyebrow">映刻</p>
        <h1 id="hero-heading">今天看哪一部</h1>
        <p class="hero-sub">已发布短剧，打开就能看</p>
        <form class="prompt" role="search" @submit.prevent="submitSearch">
          <label class="visually-hidden" for="site-search">搜索片名或简介</label>
          <input
            id="site-search"
            v-model="query"
            type="search"
            placeholder="搜索片名或简介"
            autocomplete="off"
          />
          <button type="submit">搜索</button>
        </form>
        <nav v-if="categories.length" class="tabs" aria-label="分类">
          <button
            class="tab"
            type="button"
            :aria-selected="category === ''"
            @click="category && pickCategory(category)"
          >
            全部
          </button>
          <button
            v-for="item in categories"
            :key="item"
            class="tab"
            type="button"
            :aria-selected="category === item"
            @click="pickCategory(item)"
          >
            {{ item }}
          </button>
        </nav>
      </section>

      <section
        v-if="history.length"
        id="continue"
        class="section"
        aria-labelledby="continue-heading"
      >
        <div class="section-head">
          <h2 id="continue-heading">继续观看</h2>
          <div class="history-tools">
            <span class="count">仅本设备</span>
            <button class="clear" type="button" @click="clearHistory">清空</button>
          </div>
        </div>
        <ContinueRow :items="history" @remove="removeHistorySeries" />
      </section>

      <section class="section" aria-labelledby="list-heading">
        <div class="section-head">
          <h2 id="list-heading">{{ heading }}</h2>
          <span v-if="!loading && !error" class="count">共 {{ total }} 部</span>
          <button v-if="hasFilters" class="clear" type="button" @click="clearFilters">
            清空筛选
          </button>
        </div>

        <StatePanel v-if="loading && !items.length" variant="loading" />
        <StatePanel
          v-else-if="error"
          variant="error"
          title="内容暂时取不到"
          hint="网络或服务异常，稍后重试。"
          :error="error"
          @retry="load(true)"
        />
        <StatePanel
          v-else-if="!items.length"
          :variant="hasFilters ? 'no-result' : 'empty'"
          :title="
            hasFilters
              ? `没有找到${keyword ? `「${keyword}」` : '符合筛选的内容'}`
              : '还没有已发布的作品'
          "
          :hint="hasFilters ? '换个片名或清空筛选再试一次。' : '作品通过审核并发布后会出现在这里。'"
          @clear="clearFilters"
        />
        <template v-else>
          <div class="grid">
            <SeriesCard v-for="item in items" :key="item.series_id" :series="item" />
          </div>
          <div v-if="hasMore" class="more">
            <button class="more-btn" type="button" :disabled="loading" @click="loadMore">
              {{ loading ? '加载中…' : '加载更多' }}
            </button>
          </div>
        </template>
      </section>
    </main>

    <footer class="site-foot wrap">
      <span>映刻 OpenDuanju · 首版</span>
      <span>内容来自已审核发布的自有素材</span>
    </footer>
  </div>
</template>

<style scoped>
.hero {
  padding: var(--sp-7) 0 var(--sp-2);
  text-align: center;
}
.eyebrow {
  color: var(--brand);
  font-size: var(--fs-12);
  font-weight: 700;
  letter-spacing: 0.12em;
}
.hero h1 {
  margin-top: var(--sp-2);
  font-size: var(--fs-34);
  letter-spacing: -0.4px;
}
.hero-sub {
  margin-top: var(--sp-2);
  color: var(--ink-600);
  font-size: var(--fs-14);
}
.prompt {
  display: flex;
  align-items: center;
  gap: var(--sp-2);
  max-width: 760px;
  margin: var(--sp-5) auto 0;
  padding: 8px;
  border: 1px solid var(--border-strong);
  border-radius: 18px;
  background: #0c1418d6;
  box-shadow: var(--shadow-2);
}
.prompt input {
  flex: 1;
  min-width: 0;
  height: var(--touch);
  padding: 0 var(--sp-4);
  border: 0;
  background: transparent;
  color: var(--ink-900);
  font-size: var(--fs-15, 15px);
}
.prompt input::placeholder {
  color: var(--ink-400);
}
.prompt input:focus {
  outline: none;
}
.prompt button {
  height: var(--touch);
  padding: 0 var(--sp-5);
  border: 0;
  border-radius: var(--r-pill);
  background: var(--brand);
  color: var(--brand-ink);
  font-weight: 700;
}
.tabs {
  display: flex;
  justify-content: center;
  flex-wrap: wrap;
  gap: var(--sp-2);
  max-width: 760px;
  margin: var(--sp-4) auto 0;
}
.tab {
  min-height: 36px;
  padding: 0 var(--sp-4);
  border: 0;
  border-radius: var(--r-pill);
  background: #ffffff12;
  color: var(--ink-700);
  font-size: var(--fs-13);
  white-space: nowrap;
}
.tab[aria-selected='true'] {
  background: var(--brand);
  color: var(--brand-ink);
  font-weight: 700;
}
.section {
  padding: var(--sp-6) 0 0;
}
.section-head {
  display: flex;
  align-items: baseline;
  gap: var(--sp-3);
  margin-bottom: var(--sp-4);
}
.section-head h2 {
  font-size: var(--fs-22);
  letter-spacing: -0.2px;
}
.count {
  color: var(--ink-400);
  font-size: var(--fs-12);
}
.clear {
  margin-left: auto;
  min-height: var(--touch);
  padding: 0 var(--sp-3);
  border: 1px solid var(--border-strong);
  border-radius: var(--r-pill);
  background: #ffffff12;
  color: var(--ink-700);
  font-size: var(--fs-13);
}
.clear:hover {
  border-color: var(--ink-400);
  color: var(--ink-900);
}
.grid {
  display: grid;
  gap: var(--sp-4);
  grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
}
.more {
  display: flex;
  justify-content: center;
  padding: var(--sp-5) 0 var(--sp-2);
}
.more-btn {
  min-height: var(--touch);
  padding: 0 var(--sp-5);
  border: 0;
  border-radius: var(--r-pill);
  background: var(--brand);
  color: var(--brand-ink);
  font-size: var(--fs-14);
  font-weight: 700;
}
.more-btn:disabled {
  color: var(--ink-400);
  cursor: default;
}
.site-foot {
  display: flex;
  justify-content: space-between;
  gap: var(--sp-4);
  padding: var(--sp-7) var(--sp-5) var(--sp-6);
  color: var(--ink-400);
  font-size: var(--fs-12);
}

@media (max-width: 900px) {
  .hero {
    padding-top: var(--sp-5);
  }
  .hero h1 {
    font-size: var(--fs-28);
  }
  .grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: var(--sp-3);
  }
  .tab {
    min-height: var(--touch);
  }
  .section {
    padding: var(--sp-5) 0 0;
  }
  .site-foot {
    flex-direction: column;
    padding: var(--sp-6) var(--sp-4) var(--sp-5);
  }
}

@media (max-width: 560px) {
  .grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
