<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import CoverImage from '@/components/CoverImage.vue'
import GameFilters from '@/components/GameFilters.vue'
import Pagination from '@/components/Pagination.vue'
import PostReader from '@/components/PostReader.vue'
import UiIcon from '@/components/UiIcon.vue'
import type { ListResponse, Post, Game } from '@/lib/api'
import { fetchPost, fetchPosts, fetchGames } from '@/lib/api'
import { gameColor, sourceTypeLabel } from '@/lib/games'
import { postTitle } from '@/lib/posts'
import { tzZone } from '@/stores/tz'
import { dayToRFC3339EndExclusive, dayToRFC3339Start, formatDateTime, formatDateShort } from '@/lib/time'

const games = ref<Game[]>([])
const result = ref<ListResponse<Post> | null>(null)
const loading = ref(false)
const loadError = ref('')
const expanded = ref<Post | null>(null)
const detailLoading = ref(false)
const detailError = ref('')
const page = ref(1)
const selectedGames = ref<string[]>([])
const type = ref<'' | 'article' | 'dynamic' | 'video'>('')
const keyword = ref('')
const searchTerm = ref('')
const fromDate = ref('')
const toDate = ref('')
const order = ref<'desc' | 'asc'>('desc')
const onlyNotices = ref(false)
let requestId = 0
let detailRequestId = 0
let keywordTimer: ReturnType<typeof setTimeout> | undefined
const filterCount = computed(() => [type.value, fromDate.value, toDate.value, onlyNotices.value].filter(Boolean).length)
const dateError = computed(() =>
  fromDate.value && toDate.value && fromDate.value > toDate.value ? '开始日期不能晚于结束日期' : ''
)

async function load() {
  const id = ++requestId
  if (dateError.value) {
    loading.value = false
    return
  }
  loading.value = true
  loadError.value = ''
  try {
    const response = await fetchPosts({
      page: page.value,
      pageSize: 18,
      game: selectedGames.value.length ? selectedGames.value : undefined,
      type: type.value || undefined,
      q: searchTerm.value || undefined,
      from: fromDate.value ? dayToRFC3339Start(tzZone.value, fromDate.value) : undefined,
      to: toDate.value ? dayToRFC3339EndExclusive(tzZone.value, toDate.value) : undefined,
      isUpdateNotice: onlyNotices.value ? true : undefined,
      sort: 'publish_time',
      order: order.value,
    })
    // A slower response must not replace a newer filter or page selection.
    if (id === requestId) result.value = response
  } catch (error) {
    if (id === requestId) loadError.value = error instanceof Error ? error.message : String(error)
  } finally {
    if (id === requestId) loading.value = false
  }
}

onMounted(async () => {
  try {
    games.value = (await fetchGames()).data
  } catch {
    /* Posts remain readable without game filters. */
  }
})
const filterKey = computed(() =>
  JSON.stringify([
    selectedGames.value,
    type.value,
    searchTerm.value,
    fromDate.value,
    toDate.value,
    order.value,
    onlyNotices.value,
    tzZone.value,
  ])
)
watch(
  filterKey,
  () => {
    if (page.value !== 1) page.value = 1
    else void load()
  },
  { immediate: true }
)
watch(page, () => {
  void load()
})
watch(keyword, () => {
  clearTimeout(keywordTimer)
  keywordTimer = setTimeout(() => {
    searchTerm.value = keyword.value.trim()
  }, 350)
})
onBeforeUnmount(() => {
  clearTimeout(keywordTimer)
  requestId++
  detailRequestId++
})
function toggleGame(id: string) {
  selectedGames.value = selectedGames.value.includes(id)
    ? selectedGames.value.filter((game) => game !== id)
    : [...selectedGames.value, id]
}
function resetFilters() {
  selectedGames.value = []
  type.value = ''
  keyword.value = ''
  searchTerm.value = ''
  fromDate.value = ''
  toDate.value = ''
  onlyNotices.value = false
}
async function open(post: Post) {
  const id = ++detailRequestId
  expanded.value = post
  detailError.value = ''
  detailLoading.value = !post.content
  if (post.content) return
  try {
    const detail = await fetchPost(post.id)
    if (id === detailRequestId) {
      Object.assign(post, detail.data)
      expanded.value = detail.data
    }
  } catch (error) {
    if (id === detailRequestId) detailError.value = error instanceof Error ? error.message : String(error)
  } finally {
    if (id === detailRequestId) detailLoading.value = false
  }
}
function closeReader() {
  detailRequestId++
  expanded.value = null
  detailLoading.value = false
}
</script>

<template>
  <div class="posts">
    <div class="posts-heading">
      <h1>
        官方公告<span v-if="result">{{ result.pagination.total }}</span>
      </h1>
      <label class="search-box"
        ><UiIcon name="search" :size="17" /><input
          v-model="keyword"
          type="search"
          placeholder="搜索公告"
          aria-label="搜索公告"
      /></label>
    </div>
    <div class="posts-filters">
      <GameFilters :games="games" :selected="selectedGames" @toggle="toggleGame" @clear="selectedGames = []" />
      <div class="filter-actions">
        <select v-model="order" aria-label="公告排序">
          <option value="desc">最新发布</option>
          <option value="asc">最早发布</option>
        </select>
        <details class="advanced-filters">
          <summary>
            <UiIcon name="filter" :size="15" />筛选<span v-if="filterCount" class="filter-count">{{
              filterCount
            }}</span>
          </summary>
          <div class="advanced-content">
            <label
              >公告类型<select v-model="type">
                <option value="">全部类型</option>
                <option value="article">专栏公告</option>
                <option value="dynamic">官方动态</option>
                <option value="video">官方视频</option>
              </select></label
            >
            <label>开始日期<input v-model="fromDate" type="date" :max="toDate || undefined" /></label>
            <label>结束日期<input v-model="toDate" type="date" :min="fromDate || undefined" /></label>
            <label class="notice-toggle"><input v-model="onlyNotices" type="checkbox" />仅版本更新公告</label>
            <button class="reset-button" @click="resetFilters">重置筛选</button>
          </div>
        </details>
      </div>
    </div>
    <div v-if="dateError" class="error-state" role="alert">{{ dateError }}</div>
    <div v-else-if="loadError" class="error-state" role="alert">
      <span>公告加载失败：{{ loadError }}</span
      ><button @click="load">重试</button>
    </div>
    <div v-else class="gallery-area" :aria-busy="loading">
      <div v-if="loading && !result" class="post-grid" aria-label="加载公告中">
        <div v-for="i in 6" :key="i" class="post-skeleton" />
      </div>
      <template v-else-if="result">
        <div v-if="!result.data.length" class="panel empty-state">
          <UiIcon name="search" :size="32" /><span>没有找到相关公告</span
          ><button @click="resetFilters">清除筛选</button>
        </div>
        <div v-else class="post-grid" :class="{ refreshing: loading }">
          <article
            v-for="post in result.data"
            :key="post.id"
            class="post-card"
            :style="{ '--game': gameColor(post.game_id) }"
          >
            <button class="post-open" @click="open(post)" :aria-label="`阅读：${postTitle(post)}`">
              <div class="post-visual">
                <CoverImage
                  :src="post.cover_path || post.cover_url"
                  :secondary="games.find((game) => game.id === post.game_id)?.cover"
                  :fallback="`/fallback/${post.game_id}.svg`"
                  :alt="postTitle(post)"
                />
                <span class="source-type" :title="sourceTypeLabel(post.source_type)"
                  ><UiIcon
                    :name="
                      post.source_type === 'video' ? 'play' : post.source_type === 'article' ? 'article' : 'message'
                    "
                    :size="14"
                  /><span>{{ sourceTypeLabel(post.source_type) }}</span></span
                >
                <span v-if="post.is_update_notice" class="update-notice"
                  ><UiIcon name="update" :size="11" />版本更新</span
                >
                <span class="read-arrow"><UiIcon name="arrow" :size="19" /></span>
              </div>
              <div class="post-info">
                <div class="post-meta">
                  <span class="post-game"><i />{{ post.game_name }}</span
                  ><time
                    :datetime="post.publish_time.utc"
                    :title="formatDateTime(tzZone, Date.parse(post.publish_time.utc) / 1000)"
                    >{{ formatDateShort(tzZone, Date.parse(post.publish_time.utc) / 1000) }}</time
                  >
                </div>
                <h2 :title="post.title">{{ postTitle(post) }}</h2>
              </div>
            </button>
          </article>
        </div>
        <Pagination :pagination="result.pagination" @go="page = $event" />
      </template>
    </div>
    <PostReader
      :post="expanded"
      :loading="detailLoading"
      :error="detailError"
      :game-cover="games.find((game) => game.id === expanded?.game_id)?.cover"
      @close="closeReader"
      @retry="expanded && open(expanded)"
    />
  </div>
</template>

<style scoped>
.posts {
  padding: 36px 0 52px;
}
.posts-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
  margin-bottom: 27px;
}
.posts-heading h1 {
  display: inline-flex;
  align-items: center;
  gap: 14px;
  margin: 0;
  font-size: 24px;
  font-weight: 550;
  letter-spacing: 0.03em;
}
.posts-heading h1 > span {
  font-size: 12px;
  font-weight: 400;
  letter-spacing: 0;
  color: var(--text-faint);
  padding: 3px 8px;
  border-radius: 5px;
  border: 1px solid var(--border);
}
.search-box {
  display: flex;
  align-items: center;
  gap: 9px;
  width: 260px;
  color: var(--text-faint);
  border-bottom: 1px solid #4a5463;
}
.search-box input {
  flex: 1;
  border: 0;
  background: transparent;
  padding-inline: 0;
  border-radius: 0;
  font-size: 12px;
}
.posts-filters {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding-bottom: 20px;
  border-bottom: 1px solid var(--border);
  margin-bottom: 26px;
}
.filter-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-shrink: 0;
}
.filter-actions > select {
  font-size: 11px;
  background: transparent;
  border: 0;
}
.advanced-filters {
  position: relative;
}
.advanced-filters summary {
  display: flex;
  align-items: center;
  gap: 7px;
  list-style: none;
  cursor: pointer;
  font-size: 12px;
  color: var(--text-dim);
  border: 1px solid var(--border);
  padding: 7px 12px;
  border-radius: 7px;
  min-height: 38px;
}
.advanced-filters summary::-webkit-details-marker {
  display: none;
}
.advanced-filters[open] summary {
  color: var(--accent);
  border-color: #a9d5f560;
}
.filter-count {
  display: grid;
  place-items: center;
  min-width: 16px;
  height: 16px;
  font-size: 10px;
  background: var(--accent-soft);
  border-radius: 3px;
  color: var(--accent);
}
.advanced-content {
  position: absolute;
  right: 0;
  top: calc(100% + 10px);
  z-index: 20;
  width: 270px;
  padding: 20px;
  background: #252d38;
  border: 1px solid #465364;
  border-radius: 11px;
  box-shadow: 0 16px 50px #0006;
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.advanced-content label {
  display: flex;
  flex-direction: column;
  gap: 7px;
  color: var(--text-dim);
  font-size: 12px;
}
.advanced-content .notice-toggle {
  flex-direction: row;
  align-items: center;
  gap: 8px;
}
.reset-button {
  font-size: 12px;
  background: transparent;
  border: 1px solid var(--border);
}
.post-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 28px 22px;
}
.post-grid.refreshing {
  opacity: 0.55;
  pointer-events: none;
}
.post-card {
  min-width: 0;
}
.post-open {
  display: block;
  width: 100%;
  text-align: left;
  background: transparent;
  border: 0;
  padding: 0;
  border-radius: 10px;
}
.post-open:hover {
  background: transparent;
}
.post-visual {
  position: relative;
  aspect-ratio: 16 / 10;
  border-radius: 10px;
  overflow: hidden;
  background: var(--panel);
}
.post-visual::after {
  content: '';
  position: absolute;
  inset: 0;
  border: 1px solid #ffffff12;
  border-radius: inherit;
  pointer-events: none;
}
.post-visual > img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.source-type {
  position: absolute;
  left: 13px;
  top: 13px;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 5px 8px;
  background: #101821a6;
  color: #fff;
  border: 1px solid #ffffff26;
  border-radius: 5px;
  backdrop-filter: blur(12px);
}
.source-type > span {
  font-size: 10px;
}
.update-notice {
  position: absolute;
  bottom: 13px;
  left: 13px;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: #d7f3e8;
  background: #172b29de;
  border: 1px solid #9bd3bc59;
  border-radius: 4px;
  padding: 4px 7px;
  font-size: 10px;
}
.read-arrow {
  position: absolute;
  right: 13px;
  bottom: 13px;
  display: grid;
  place-items: center;
  width: 33px;
  height: 33px;
  border-radius: 50%;
  color: #fff;
  background: #10182180;
  border: 1px solid #ffffff4d;
  opacity: 0;
  transition: opacity 160ms;
}
.post-open:is(:hover, :focus-visible) .read-arrow {
  opacity: 1;
}
.post-info {
  padding: 14px 2px 0;
}
.post-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  font-size: 10px;
  color: var(--text-faint);
}
.post-game {
  display: flex;
  align-items: center;
  gap: 6px;
}
.post-game i {
  height: 4px;
  width: 4px;
  border-radius: 50%;
  background: var(--game);
}
.post-meta time {
  font-variant-numeric: tabular-nums;
}
.post-info h2 {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  height: 48px;
  margin: 8px 0 0;
  font-size: 14px;
  font-weight: 450;
  line-height: 1.7;
  overflow-wrap: anywhere;
}
.post-open:hover h2 {
  color: var(--accent);
}
.post-skeleton {
  height: 320px;
  border-radius: 10px;
  background: linear-gradient(110deg, #252d38, #303a48, #252d38);
  background-size: 200% 100%;
  animation: loading 2s infinite;
}
@keyframes loading {
  to {
    background-position: -200% 0;
  }
}
@media (max-width: 1100px) {
  .posts-filters {
    align-items: stretch;
    flex-direction: column;
  }
  .filter-actions {
    align-self: flex-end;
  }
}
@media (max-width: 900px) {
  .post-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 640px) {
  .posts {
    padding-top: 25px;
    padding-bottom: 36px;
  }
  .posts-heading {
    flex-wrap: wrap;
    gap: 18px;
    margin-bottom: 20px;
  }
  .posts-heading h1 {
    font-size: 22px;
  }
  .search-box {
    width: 100%;
  }
  .posts-filters {
    margin-bottom: 20px;
    padding-bottom: 15px;
    gap: 10px;
  }
  .post-grid {
    grid-template-columns: 1fr;
    gap: 24px;
  }
  .post-info h2 {
    font-size: 15px;
    height: auto;
    min-height: 26px;
  }
  .post-visual {
    aspect-ratio: 16 / 10;
  }
  .post-info {
    padding-top: 13px;
  }
  .post-meta {
    font-size: 11px;
  }
  .read-arrow {
    opacity: 1;
  }
}
</style>
