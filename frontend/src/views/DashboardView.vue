<script setup lang="ts">
// Dashboard: interactive timeline first (the schedule board), then the next
// event, per-game cards, and calendar + agenda. The timeline window is shared
// state — pan/zoom gestures re-filter the agenda so the list follows the view.
import { computed, onMounted, ref } from 'vue'

import ConfidenceBadge from '@/components/ConfidenceBadge.vue'
import FilterBar from '@/components/FilterBar.vue'
import GameCard from '@/components/GameCard.vue'
import MonthCalendar from '@/components/MonthCalendar.vue'
import TimelineChart from '@/components/TimelineChart.vue'
import type { TimelineEvent } from '@/components/TimelineChart.vue'
import type { Game, Meta, Prediction, Version } from '@/lib/api'
import { fetchGames, fetchPredictions, fetchVersions } from '@/lib/api'
import { gameColor } from '@/lib/games'
import { nowUnix } from '@/stores/now'
import { tzZone } from '@/stores/tz'
import { BUSINESS_TZ, dayKey, formatDateTime, formatTime, formatWeekday, offsetLabel, relativeTime } from '@/lib/time'

defineProps<{ meta: Meta | null; lastCycleText: string }>()

const games = ref<Game[]>([])
const predictions = ref<Prediction[]>([])
const versions = ref<Version[]>([])
const loadError = ref('')
const selectedGames = ref<string[]>([])
const rangeDays = ref(30) // 0 = custom window set by timeline gestures
const sortMode = ref<'time_asc' | 'time_desc'>('time_asc')
const selectedDay = ref<string | undefined>()
const windowStart = ref(0)
const windowEnd = ref(0)

const DAY = 86400

function applyPreset(days: number) {
  rangeDays.value = days
  windowStart.value = nowUnix.value - 3 * DAY
  windowEnd.value = nowUnix.value + (days === 3650 ? 120 : days) * DAY
}
applyPreset(30)

function onWindow(value: { start: number; end: number }) {
  windowStart.value = value.start
  windowEnd.value = value.end
  rangeDays.value = 0
}

function recenter() {
  applyPreset(rangeDays.value || 30)
}

async function load() {
  try {
    const [gamesRes, predictionRes] = await Promise.all([
      fetchGames(),
      fetchPredictions({ active: true, pageSize: 100, order: 'asc' }),
    ])
    games.value = gamesRes.data
    predictions.value = predictionRes.data
    const versionRes = await fetchVersions({ pageSize: 100, order: 'desc' })
    versions.value = versionRes.data
    loadError.value = ''
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : String(error)
  }
}

onMounted(() => {
  void load()
})

function toggleGame(id: string) {
  const index = selectedGames.value.indexOf(id)
  if (index >= 0) selectedGames.value.splice(index, 1)
  else selectedGames.value.push(id)
}

const visibleGames = computed(() =>
  selectedGames.value.length === 0 ? games.value : games.value.filter((game) => selectedGames.value.includes(game.id))
)

interface AgendaEvent {
  key: string
  gameId: string
  gameName: string
  kind: 'update' | 'preview'
  unix: number
  title: string
  confidence?: string
  evidence?: string
  url?: string
  cover?: string
  fallback: string
}

// Every event for the selected games, unwindowed: the calendar and the
// "next event" strip always work on the full picture.
const allEvents = computed<AgendaEvent[]>(() => {
  const gameById = new Map(games.value.map((game) => [game.id, game]))
  const out: AgendaEvent[] = []
  const push = (
    gameId: string,
    kind: 'update' | 'preview',
    unix: number,
    version: string,
    confidence: string | undefined,
    evidence: string | undefined,
    url: string | undefined,
    kindLabel?: string
  ) => {
    const game = gameById.get(gameId)
    if (!game) return
    if (selectedGames.value.length > 0 && !selectedGames.value.includes(gameId)) return
    const versionText = version ? ` ${version}` : ''
    const label = kindLabel ?? (kind === 'preview' ? '版本前瞻直播' : '版本更新')
    out.push({
      key: `${gameId}-${kind}-${unix}`,
      gameId,
      gameName: game.name,
      kind,
      unix,
      title: `${game.name}${versionText} ${label}`,
      confidence,
      evidence,
      url,
      cover: kind === 'update' ? game.cover : undefined,
      fallback: game.fallback,
    })
  }
  for (const prediction of predictions.value) {
    if (prediction.update) {
      push(
        prediction.game_id,
        'update',
        prediction.update.unix_seconds,
        prediction.version,
        prediction.update_confidence,
        prediction.update_evidence,
        prediction.announcement_url,
        prediction.is_phase_update ? '阶段内容更新' : undefined
      )
    }
    if (prediction.preview) {
      push(
        prediction.game_id,
        'preview',
        prediction.preview.unix_seconds,
        prediction.version,
        prediction.preview_confidence,
        prediction.preview_evidence,
        undefined
      )
    }
  }
  // Historical versions add past context to the timeline and agenda.
  for (const version of versions.value) {
    if (version.update) {
      push(
        version.game_id,
        'update',
        version.update.unix_seconds,
        version.version,
        'official',
        '历史版本（公告解析）',
        undefined
      )
    }
  }
  // de-duplicate: a prediction may repeat a version already in history
  const seen = new Set<string>()
  return out.filter((event) => {
    const key = `${event.gameId}-${event.kind}-${dayKey(tzZone.value, event.unix)}`
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
})

const timelineEvents = computed<TimelineEvent[]>(() =>
  allEvents.value
    .filter((event) => event.unix >= windowStart.value - DAY && event.unix <= windowEnd.value + DAY)
    .map((event) => ({
      key: event.key,
      gameId: event.gameId,
      gameName: event.gameName,
      kind: event.kind,
      unix: event.unix,
      label: event.title,
      confidence: event.confidence,
    }))
)

// The agenda list follows the timeline window, or a picked calendar day.
const agendaEvents = computed(() => {
  const zone = tzZone.value
  const filtered = allEvents.value.filter((event) => {
    if (selectedDay.value) return dayKey(zone, event.unix) === selectedDay.value
    return event.unix >= windowStart.value - DAY && event.unix <= windowEnd.value + DAY
  })
  return filtered.sort((a, b) => (sortMode.value === 'time_asc' ? a.unix - b.unix : b.unix - a.unix))
})

const agendaGroups = computed(() => {
  const zone = tzZone.value
  const groups = new Map<string, AgendaEvent[]>()
  for (const event of agendaEvents.value) {
    const key = dayKey(zone, event.unix)
    const bucket = groups.get(key) ?? []
    bucket.push(event)
    groups.set(key, bucket)
  }
  return [...groups.entries()].map(([key, events]) => ({
    key,
    events: events.sort((a, b) => a.unix - b.unix),
  }))
})

const nextEvent = computed(() => {
  const upcoming = allEvents.value.filter((event) => event.unix >= nowUnix.value).sort((a, b) => a.unix - b.unix)
  return upcoming[0]
})
</script>

<template>
  <div class="dashboard">
    <section v-if="loadError" class="panel panel-pad error">加载失败：{{ loadError }}（稍后自动重试）</section>

    <!-- timeline: the schedule board, first thing on the page -->
    <section class="panel panel-pad block timeline-block">
      <h2 class="section-title">
        版本时间轴
        <span class="hint">◆ 更新 · ○ 前瞻 · 按 {{ tzZone }}（{{ offsetLabel(tzZone) }}）切日</span>
        <span v-if="lastCycleText" class="hint data-hint">数据截至 {{ lastCycleText }}</span>
      </h2>
      <FilterBar
        :games="games"
        :selected="selectedGames"
        :range="rangeDays"
        @toggle-game="toggleGame"
        @range="applyPreset"
        @all-games="selectedGames = []"
      />
      <TimelineChart
        :games="visibleGames"
        :events="timelineEvents"
        :start-unix="windowStart"
        :end-unix="windowEnd"
        @window="onWindow"
        @recenter="recenter"
      />
    </section>

    <!-- next event countdown -->
    <section v-if="nextEvent" class="panel hero" :style="{ '--hero-color': gameColor(nextEvent.gameId) }">
      <div class="hero-main">
        <div class="hero-kicker faint">下一场 · {{ nextEvent.kind === 'preview' ? '前瞻直播' : '版本更新' }}</div>
        <h1 class="hero-title">{{ nextEvent.title }}</h1>
        <div class="hero-times">
          <span>{{ formatDateTime(tzZone, nextEvent.unix) }}（{{ tzZone }}）</span>
          <span class="faint">国服 {{ formatTime(BUSINESS_TZ, nextEvent.unix) }}</span>
        </div>
      </div>
      <div class="hero-count">
        <div class="hero-countdown">{{ relativeTime(nextEvent.unix, nowUnix) }}</div>
      </div>
      <img
        v-if="nextEvent.cover || nextEvent.fallback"
        :src="nextEvent.cover || nextEvent.fallback"
        alt=""
        class="hero-cover"
        referrerpolicy="no-referrer"
        @error="($event.target as HTMLImageElement).src = nextEvent.fallback"
      />
    </section>

    <!-- game cards -->
    <section class="cards">
      <GameCard v-for="game in visibleGames" :key="game.id" :game="game" />
    </section>

    <!-- calendar + agenda -->
    <section class="two-col">
      <div class="panel panel-pad block">
        <h2 class="section-title">
          月历 <span class="hint">按 {{ tzZone }} 切日</span>
        </h2>
        <MonthCalendar :events="allEvents" :selected-key="selectedDay" @select="selectedDay = $event" />
      </div>
      <div class="panel panel-pad block">
        <h2 class="section-title">
          事件列表
          <span class="hint">
            <template v-if="selectedDay"
              >{{ selectedDay }}（{{ tzZone }}）<button class="link" @click="selectedDay = undefined">
                清除
              </button></template
            >
            <template v-else>{{ sortMode === 'time_asc' ? '由近到远' : '由远到近' }}</template>
          </span>
          <label class="sort">
            排序
            <select
              :value="sortMode"
              @change="sortMode = ($event.target as HTMLSelectElement).value as typeof sortMode"
            >
              <option value="time_asc">近 → 远</option>
              <option value="time_desc">远 → 近</option>
            </select>
          </label>
        </h2>
        <div v-if="agendaGroups.length === 0" class="faint empty">所选范围内没有事件，试试扩大范围或选择全部游戏。</div>
        <div v-for="group in agendaGroups" :key="group.key" class="day-group">
          <div class="day-head">
            <span class="day-date">{{ group.key }}</span>
            <span class="faint">{{ formatWeekday(tzZone, group.events[0].unix) }}</span>
          </div>
          <div
            v-for="event in group.events"
            :key="event.key"
            class="event-row"
            :style="{ '--game': gameColor(event.gameId) }"
          >
            <img
              class="thumb"
              :src="event.cover || event.fallback"
              alt=""
              loading="lazy"
              referrerpolicy="no-referrer"
              @error="($event.target as HTMLImageElement).src = event.fallback"
            />
            <div class="event-main">
              <div class="event-title">
                <span class="event-time">{{ formatTime(tzZone, event.unix) }}</span>
                {{ event.title }}
                <ConfidenceBadge :value="event.confidence" />
              </div>
              <div class="event-meta">
                <span>国服 {{ formatTime(BUSINESS_TZ, event.unix) }}</span>
                <span>{{ relativeTime(event.unix, nowUnix) }}</span>
                <span v-if="event.evidence" class="faint">依据：{{ event.evidence }}</span>
                <a v-if="event.url" :href="event.url" target="_blank" rel="noreferrer">官方公告 ↗</a>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.dashboard {
  display: flex;
  flex-direction: column;
  gap: 1.1rem;
  padding: 1.1rem 0 1.4rem;
}
.error {
  color: var(--danger);
}
.timeline-block {
  /* the board gets a touch more presence than the quiet panels below */
  background: linear-gradient(180deg, rgba(151, 173, 214, 0.07), rgba(151, 173, 214, 0.04));
}
.data-hint {
  margin-left: auto;
}
.hero {
  display: flex;
  align-items: stretch;
  overflow: hidden;
  min-height: 120px;
  border-left: 3px solid var(--hero-color, var(--accent));
}
.hero-main {
  flex: 1;
  padding: 1rem 1.25rem;
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
  justify-content: center;
  min-width: 0;
}
.hero-title {
  margin: 0;
  font-size: 1.35rem;
  overflow-wrap: anywhere;
}
.hero-times {
  display: flex;
  gap: 1rem;
  flex-wrap: wrap;
  color: var(--text-dim);
  font-size: 0.9rem;
  font-variant-numeric: tabular-nums;
}
.hero-count {
  display: flex;
  align-items: center;
  padding: 0 1.4rem;
}
.hero-countdown {
  font-size: 1.9rem;
  font-weight: 750;
  color: var(--hero-color, var(--accent));
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}
.hero-cover {
  width: 280px;
  object-fit: cover;
  clip-path: polygon(18% 0, 100% 0, 100% 100%, 0 100%);
}
.cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 0.9rem;
}
.block {
  margin: 0.2rem 0;
}
.two-col {
  display: grid;
  grid-template-columns: 340px 1fr;
  gap: 0.9rem;
  align-items: start;
}
.sort {
  margin-left: auto;
  display: inline-flex;
  gap: 0.4rem;
  align-items: center;
  font-size: 0.78rem;
  color: var(--text-dim);
  font-weight: 400;
}
.sort select {
  font-size: 0.8rem;
  padding: 0.24rem 0.5rem;
}
.day-group {
  margin-bottom: 0.8rem;
}
.day-head {
  display: flex;
  gap: 0.55rem;
  align-items: baseline;
  border-bottom: 1px dashed var(--border);
  padding-bottom: 0.25rem;
  margin-bottom: 0.45rem;
}
.day-date {
  font-weight: 650;
  font-variant-numeric: tabular-nums;
}
.event-row {
  display: flex;
  gap: 0.75rem;
  align-items: center;
  padding: 0.45rem 0.55rem;
  border-radius: var(--radius);
  border-left: 3px solid var(--game);
}
.event-row:hover {
  background: rgba(151, 173, 214, 0.06);
}
.thumb {
  width: 76px;
  height: 44px;
  border-radius: var(--radius-tag);
  object-fit: cover;
  flex-shrink: 0;
}
.event-main {
  min-width: 0;
  flex: 1;
}
.event-title {
  display: flex;
  gap: 0.5rem;
  align-items: center;
  flex-wrap: wrap;
  font-weight: 600;
  font-size: 0.92rem;
}
.event-time {
  font-variant-numeric: tabular-nums;
  color: var(--text-dim);
  font-weight: 500;
}
.event-meta {
  display: flex;
  gap: 0.8rem;
  flex-wrap: wrap;
  font-size: 0.74rem;
  color: var(--text-dim);
  margin-top: 2px;
}
.empty {
  padding: 1rem 0;
}
button.link {
  background: none;
  border: none;
  color: var(--accent);
  padding: 0;
  font-size: 0.74rem;
}
@media (max-width: 900px) {
  .two-col {
    grid-template-columns: 1fr;
  }
  .hero-cover {
    display: none;
  }
  .hero-count {
    padding: 0 0.95rem 0 0;
  }
  .hero-countdown {
    font-size: 1.5rem;
  }
}
</style>
