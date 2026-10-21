<script setup lang="ts">
// Raw official posts browser ("原始推文"): pagination + game/type/time-range
// filters + sort, with an expandable full-text drawer.
import { computed, onMounted, ref, watch } from 'vue'

import CoverImage from '@/components/CoverImage.vue'
import Pagination from '@/components/Pagination.vue'
import type { ListResponse, Post, Game } from '@/lib/api'
import { fetchPost, fetchPosts, fetchGames } from '@/lib/api'
import { gameColor, sourceTypeLabel } from '@/lib/games'
import { tzZone } from '@/stores/tz'
import { dayToRFC3339EndExclusive, dayToRFC3339Start, formatDateTime } from '@/lib/time'

const games = ref<Game[]>([])
const result = ref<ListResponse<Post> | null>(null)
const loading = ref(false)
const loadError = ref('')
const expanded = ref<Post | null>(null)

const page = ref(1)
const selectedGames = ref<string[]>([])
const type = ref<'' | 'article' | 'dynamic' | 'video'>('')
const keyword = ref('')
const fromDate = ref('')
const toDate = ref('')
const order = ref<'desc' | 'asc'>('desc')
const onlyNotices = ref(false)

async function load() {
  loading.value = true
  try {
    result.value = await fetchPosts({
      page: page.value,
      pageSize: 20,
      game: selectedGames.value.length ? selectedGames.value : undefined,
      type: type.value || undefined,
      q: keyword.value.trim() || undefined,
      from: fromDate.value ? dayToRFC3339Start(tzZone.value, fromDate.value) : undefined,
      to: toDate.value ? dayToRFC3339EndExclusive(tzZone.value, toDate.value) : undefined,
      isUpdateNotice: onlyNotices.value ? true : undefined,
      sort: 'publish_time',
      order: order.value,
    })
    loadError.value = ''
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : String(error)
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  void load()
  try {
    games.value = (await fetchGames()).data
  } catch { /* games list is decorative here */ }
})

watch([page, type, order, onlyNotices, fromDate, toDate], () => { void load() })

let keywordTimer: number | undefined
watch(keyword, () => {
  if (keywordTimer) window.clearTimeout(keywordTimer)
  keywordTimer = window.setTimeout(() => { page.value = 1; void load() }, 350)
})

function toggleGame(id: string) {
  const index = selectedGames.value.indexOf(id)
  if (index >= 0) selectedGames.value.splice(index, 1)
  else selectedGames.value.push(id)
  page.value = 1
  void load()
}

async function open(post: Post) {
  if (expanded.value?.id === post.id) {
    expanded.value = null
    return
  }
  if (post.content) {
    expanded.value = post
    return
  }
  try {
    const detail = await fetchPost(post.id)
    Object.assign(post, detail.data)
    expanded.value = detail.data
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : String(error)
  }
}

const hint = computed(() =>
  `日期按所选时区（${tzZone.value}）的当天 00:00 转换为 UTC，区间左闭右开`,
)
</script>

<template>
  <div class="posts">
    <div class="panel panel-pad block">
      <h2 class="section-title">官方公告/动态/视频 <span class="hint">原始数据 · 供人工核对与 Agent 接入</span></h2>

      <div class="toolbar">
        <div class="chips">
          <button class="chip" :class="{ active: selectedGames.length === 0 }" @click="selectedGames = []; page = 1; load()">
            全部游戏
          </button>
          <button
            v-for="game in games" :key="game.id"
            class="chip" :class="{ active: selectedGames.includes(game.id) }"
            :style="{ '--chip-color': gameColor(game.id) }" @click="toggleGame(game.id)"
          >
            <span class="dot" :style="{ background: gameColor(game.id) }" />{{ game.name }}
          </button>
        </div>
        <div class="controls">
          <label>类型
            <select :value="type" @change="type = ($event.target as HTMLSelectElement).value as typeof type; page = 1">
              <option value="">全部</option>
              <option value="article">专栏公告</option>
              <option value="dynamic">官方动态</option>
              <option value="video">官方视频</option>
            </select>
          </label>
          <label>从
            <input v-model="fromDate" type="date" />
          </label>
          <label>至
            <input v-model="toDate" type="date" />
          </label>
          <label>排序
            <select :value="order" @change="order = ($event.target as HTMLSelectElement).value as 'desc' | 'asc'">
              <option value="desc">新→旧</option>
              <option value="asc">旧→新</option>
            </select>
          </label>
          <input v-model="keyword" type="search" placeholder="搜索标题/摘要…" class="search" />
          <label class="notice-toggle">
            <input v-model="onlyNotices" type="checkbox" />
            仅版本更新公告
          </label>
        </div>
        <div class="faint">{{ hint }}</div>
      </div>

      <div v-if="loadError" class="error">加载失败：{{ loadError }}</div>
      <div v-else-if="loading && !result" class="faint">加载中…</div>

      <template v-if="result">
        <div v-if="result.data.length === 0" class="faint empty">没有匹配的公告。</div>
        <article
          v-for="post in result.data" :key="post.id"
          class="post-row" :style="{ '--game': gameColor(post.game_id) }"
        >
          <CoverImage
            :src="post.cover_path || post.cover_url" :fallback="`/fallback/${post.game_id}.svg`"
            :alt="post.title" class="cover"
          />
          <div class="post-main">
            <div class="post-title">
              <span class="kind" :class="post.source_type">{{ sourceTypeLabel(post.source_type) }}</span>
              <span v-if="post.is_update_notice" class="kind notice">更新公告</span>
              <a :href="post.url" target="_blank" rel="noreferrer" class="title-link">{{ post.title || '（无标题动态）' }}</a>
            </div>
            <div class="post-meta">
              <span class="game" :style="{ color: gameColor(post.game_id) }">{{ post.game_name }}</span>
              <span>{{ formatDateTime(tzZone, Date.parse(post.publish_time.utc) / 1000) }}（{{ tzZone }}）</span>
              <span class="faint">国服 {{ formatDateTime('Asia/Shanghai', Date.parse(post.publish_time.utc) / 1000) }}</span>
            </div>
            <p class="post-summary">{{ post.summary }}</p>
            <button class="expand" @click="open(post)">
              {{ expanded?.id === post.id ? '收起' : (post.content ? '查看全文' : '加载全文') }}
            </button>
            <pre v-if="expanded?.id === post.id && expanded.content" class="post-content">{{ expanded.content }}</pre>
          </div>
        </article>
        <Pagination :pagination="result.pagination" @go="page = $event" />
      </template>
    </div>
  </div>
</template>

<style scoped>
.posts { padding: 1.1rem 0 1.4rem; }
.toolbar { display: flex; flex-direction: column; gap: 0.6rem; margin-bottom: 1rem; }
.chips { display: flex; gap: 0.45rem; flex-wrap: wrap; }
.controls { display: flex; gap: 0.8rem; align-items: center; flex-wrap: wrap; font-size: 0.82rem; color: var(--text-dim); }
.controls label { display: inline-flex; gap: 0.35rem; align-items: center; }
.search { min-width: 200px; }
.notice-toggle { cursor: pointer; }
.error { color: var(--danger); padding: 0.5rem 0; }
.empty { padding: 0.8rem 0; }
.post-row {
  display: flex; gap: 0.9rem; padding: 0.8rem 0.6rem;
  border-left: 3px solid var(--game); border-radius: 10px; margin-bottom: 0.55rem;
}
.post-row:hover { background: rgba(255, 255, 255, 0.035); }
.cover { width: 132px; height: 78px; border-radius: 8px; object-fit: cover; flex-shrink: 0; }
.post-main { min-width: 0; flex: 1; }
.post-title { display: flex; gap: 0.5rem; align-items: center; flex-wrap: wrap; }
.title-link { color: var(--text); font-weight: 620; }
.title-link:hover { color: var(--accent); }
.kind {
  font-size: 0.68rem; padding: 0.08rem 0.45rem; border-radius: 999px;
  border: 1px solid var(--border); color: var(--text-dim); white-space: nowrap;
}
.kind.notice { color: var(--official); border-color: rgba(52, 211, 153, 0.4); }
.post-meta { display: flex; gap: 0.8rem; flex-wrap: wrap; font-size: 0.74rem; color: var(--text-dim); margin-top: 2px; }
.post-summary {
  margin: 0.35rem 0 0.3rem; font-size: 0.82rem; color: var(--text-dim);
  display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden;
}
.expand { font-size: 0.74rem; padding: 0.18rem 0.6rem; color: var(--accent); }
.post-content {
  margin: 0.6rem 0 0; padding: 0.7rem 0.8rem; max-height: 340px; overflow: auto;
  background: rgba(2, 6, 23, 0.45); border: 1px solid var(--border); border-radius: 10px;
  font-size: 0.8rem; line-height: 1.65; white-space: pre-wrap; word-break: break-word;
  font-family: inherit; color: var(--text-dim);
}
</style>
