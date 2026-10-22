<script setup lang="ts">
// The timeline window is shared with the agenda; visual highlights use the
// full schedule so a pan never hides the next upcoming event.
import { computed, onMounted, ref, watch } from 'vue'

import EventCard from '@/components/EventCard.vue'
import type { ScheduleEvent } from '@/components/EventCard.vue'
import UiIcon from '@/components/UiIcon.vue'
import FilterBar from '@/components/FilterBar.vue'
import GameCard from '@/components/GameCard.vue'
import MonthCalendar from '@/components/MonthCalendar.vue'
import TimelineChart from '@/components/TimelineChart.vue'
import type { TimelineEvent } from '@/components/TimelineChart.vue'
import type { Game, Prediction, Version } from '@/lib/api'
import { fetchGames, fetchPredictions, fetchVersions } from '@/lib/api'
import { nowUnix } from '@/stores/now'
import { tzZone } from '@/stores/tz'
import { dayKey, formatDateShort, formatWeekday } from '@/lib/time'

const games = ref<Game[]>([])
const predictions = ref<Prediction[]>([])
const versions = ref<Version[]>([])
const loadError = ref('')
const loading = ref(true)
const selectedGames = ref<string[]>([])
const rangeDays = ref(30) // 0 = custom window set by timeline gestures
const sortMode = ref<'time_asc' | 'time_desc'>('time_asc')
const selectedDay = ref<string | undefined>()
const agendaExpanded = ref(false)
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
function resetAgenda() {
  selectedDay.value = undefined
  applyPreset(90)
}

async function load() {
  loading.value = true
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
  } finally {
    loading.value = false
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

// Every event for the selected games, unwindowed: the calendar and the
// "next event" strip always work on the full picture.
const allEvents = computed<ScheduleEvent[]>(() => {
  const gameById = new Map(games.value.map((game) => [game.id, game]))
  const out: ScheduleEvent[] = []
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
      kindLabel,
      unix,
      title: `${game.name}${versionText} ${label}`,
      version,
      confidence,
      evidence,
      url,
      // update events carry the update-notice banner; preview events the
      // newest teaser visual (官宣动态/直播间横幅), falling back to the
      // game card visual when no teaser image was collected yet.
      cover: kind === 'update' ? game.cover : game.preview_cover || game.cover,
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
  const groups = new Map<string, ScheduleEvent[]>()
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
const visibleAgendaGroups = computed(() => (agendaExpanded.value ? agendaGroups.value : agendaGroups.value.slice(0, 3)))
watch(
  [selectedDay, selectedGames, sortMode, windowStart, windowEnd],
  () => {
    agendaExpanded.value = false
  },
  { deep: true }
)

const upcomingEvents = computed(() =>
  allEvents.value
    .filter((event) => event.unix >= nowUnix.value)
    .sort((a, b) => a.unix - b.unix)
    .slice(0, 3)
)
const windowLabel = computed(
  () => `${formatDateShort(tzZone.value, windowStart.value)} — ${formatDateShort(tzZone.value, windowEnd.value)}`
)
</script>

<template>
  <div class="dashboard" :aria-busy="loading">
    <div v-if="loadError" class="error-state" role="alert">
      <UiIcon name="info" /><span>日程加载失败：{{ loadError }}</span
      ><button @click="load"><UiIcon name="refresh" :size="14" />重试</button>
    </div>
    <section class="timeline-section" aria-labelledby="timeline-title">
      <div class="section-heading">
        <h1 id="timeline-title" class="section-title">版本时间轴</h1>
        <div class="timeline-legend" aria-label="事件图例">
          <span><i class="update-mark" />更新</span><span><i class="preview-mark" />前瞻</span>
        </div>
      </div>
      <div class="panel panel-pad timeline-panel">
        <FilterBar
          :games="games"
          :selected="selectedGames"
          :range="rangeDays"
          @toggle-game="toggleGame"
          @range="applyPreset"
          @all-games="selectedGames = []"
        />
        <div v-if="loading && !games.length" class="timeline-loading" role="status">正在载入日程…</div>
        <TimelineChart
          v-else
          :games="visibleGames"
          :events="timelineEvents"
          :start-unix="windowStart"
          :end-unix="windowEnd"
          @window="onWindow"
          @recenter="recenter"
        />
        <div class="timeline-caption">
          <span>{{ windowLabel }}</span
          ><span :title="tzZone">{{ tzZone === 'Asia/Shanghai' ? '国服时间' : tzZone }}</span>
        </div>
      </div>
    </section>

    <section v-if="upcomingEvents.length" aria-labelledby="upcoming-title">
      <div class="section-heading">
        <h2 id="upcoming-title" class="section-title">即将到来</h2>
        <a href="#agenda" class="section-link">查看日程<UiIcon name="chevron" :size="13" /></a>
      </div>
      <div class="upcoming-grid" :class="{ 'single-event': upcomingEvents.length === 1 }">
        <EventCard :event="upcomingEvents[0]!" featured />
        <div v-if="upcomingEvents.length > 1" class="upcoming-rail">
          <EventCard v-for="event in upcomingEvents.slice(1)" :key="event.key" :event="event" />
        </div>
      </div>
    </section>

    <section aria-labelledby="games-title">
      <div class="section-heading">
        <h2 id="games-title" class="section-title">
          游戏一览<span class="section-count">{{ visibleGames.length || '—' }}</span>
        </h2>
        <span class="section-note">{{ selectedGames.length ? '已筛选' : '全部游戏' }}</span>
      </div>
      <div v-if="loading && !games.length" class="cards" aria-label="加载游戏中">
        <div v-for="i in 6" :key="i" class="skeleton-card">
          <div />
          <span />
        </div>
      </div>
      <div v-else-if="!visibleGames.length" class="panel empty-state">
        <UiIcon name="calendar" :size="30" /><span>暂无游戏日程</span><button @click="load">重新加载</button>
      </div>
      <div v-else class="cards"><GameCard v-for="game in visibleGames" :key="game.id" :game="game" /></div>
    </section>

    <section id="agenda" class="agenda-section" aria-labelledby="agenda-title">
      <div class="section-heading">
        <h2 id="agenda-title" class="section-title">日程速览</h2>
        <span class="section-note">{{ agendaEvents.length }} 场事件</span>
      </div>
      <div class="two-col">
        <aside class="panel panel-pad calendar-panel" aria-label="月历">
          <MonthCalendar :events="allEvents" :selected-key="selectedDay" @select="selectedDay = $event" />
        </aside>
        <div class="agenda-list">
          <div class="agenda-toolbar">
            <button
              v-if="selectedDay"
              class="selected-day"
              @click="selectedDay = undefined"
              :aria-label="`清除日期筛选 ${selectedDay}`"
            >
              {{ selectedDay }}<UiIcon name="close" :size="12" />
            </button>
            <span v-else class="section-note">{{ windowLabel }}</span>
            <select v-model="sortMode" aria-label="日程排序">
              <option value="time_asc">时间从近到远</option>
              <option value="time_desc">时间从远到近</option>
            </select>
          </div>
          <div v-if="!agendaGroups.length" class="panel empty-state">
            <UiIcon name="calendar" :size="30" /><span>{{ selectedDay ? '这一天暂无日程' : '当前范围暂无日程' }}</span
            ><button @click="resetAgenda">查看三个月日程</button>
          </div>
          <div v-for="group in visibleAgendaGroups" :key="group.key" class="day-group">
            <div class="day-head">
              <time>{{ group.key.slice(5).replace('-', '.') }}</time
              ><span>{{ formatWeekday(tzZone, group.events[0]!.unix) }}</span
              ><i />
            </div>
            <div class="day-events">
              <EventCard v-for="event in group.events" :key="event.key" :event="event" compact />
            </div>
          </div>
          <button
            v-if="agendaGroups.length > 3"
            class="more-agenda"
            :aria-expanded="agendaExpanded"
            @click="agendaExpanded = !agendaExpanded"
          >
            {{ agendaExpanded ? '收起日程' : `展开其余 ${agendaGroups.length - 3} 天日程` }}
            <UiIcon name="chevron" :size="13" :class="{ expanded: agendaExpanded }" />
          </button>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.dashboard {
  display: flex;
  flex-direction: column;
  gap: 40px;
  padding: 32px 0 52px;
}
.timeline-legend {
  display: flex;
  gap: 18px;
  font-size: 11px;
  color: var(--text-dim);
}
.timeline-legend > span {
  display: flex;
  align-items: center;
  gap: 7px;
}
.timeline-legend i {
  width: 7px;
  height: 7px;
  display: inline-block;
}
.update-mark {
  background: var(--text-dim);
  border-radius: 1px;
  transform: rotate(45deg);
}
.preview-mark {
  border: 1px solid var(--text-dim);
  border-radius: 50%;
}
.timeline-panel {
  padding-bottom: 15px;
  background: linear-gradient(120deg, #202733, var(--panel) 65%);
}
.timeline-caption {
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-top: 1px solid var(--border);
  margin-top: 16px;
  padding-top: 12px;
  color: var(--text-faint);
  font-size: 10px;
  font-variant-numeric: tabular-nums;
}
.timeline-loading {
  display: grid;
  place-items: center;
  min-height: 306px;
  color: var(--text-faint);
  font-size: 12px;
}
.section-link {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 11px;
  color: var(--text-dim);
}
.upcoming-grid {
  display: grid;
  grid-template-columns: 1.85fr 1fr;
  gap: 18px;
}
.upcoming-grid.single-event {
  grid-template-columns: 1fr;
}
.upcoming-rail {
  display: grid;
  gap: 18px;
  grid-auto-rows: 1fr;
}
.cards {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 22px;
}
.section-count {
  display: inline-grid;
  place-items: center;
  width: 23px;
  height: 23px;
  border: 1px solid var(--border);
  border-radius: 6px;
  font-size: 11px;
  color: var(--text-faint);
  font-weight: 400;
}
.two-col {
  display: grid;
  grid-template-columns: 320px minmax(0, 1fr);
  gap: 30px;
  align-items: start;
}
.calendar-panel {
  position: sticky;
  top: 102px;
  padding: 20px;
}
.agenda-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 18px;
}
.agenda-toolbar select {
  font-size: 11px;
  min-height: 32px;
  padding: 4px 10px;
}
.selected-day {
  font-size: 11px;
  background: var(--accent-soft);
  color: var(--accent);
  padding: 5px 9px;
}
.day-group + .day-group {
  margin-top: 24px;
}
.more-agenda {
  width: 100%;
  margin-top: 22px;
  background: transparent;
  border: 1px solid var(--border);
  color: var(--text-dim);
  font-size: 12px;
}
.more-agenda > svg {
  transform: rotate(90deg);
}
.more-agenda > svg.expanded {
  transform: rotate(-90deg);
}
.day-head {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 13px;
}
.day-head time {
  font-size: 17px;
  font-variant-numeric: tabular-nums;
}
.day-head > span {
  color: var(--text-faint);
  font-size: 11px;
}
.day-head i {
  height: 1px;
  flex: 1;
  margin-left: 8px;
  background: var(--border);
}
.day-events {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
}
.skeleton-card {
  overflow: hidden;
  height: 360px;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 12px;
}
.skeleton-card > div {
  height: 70%;
  background: linear-gradient(110deg, #252d38 20%, #303a48 40%, #252d38 60%);
  background-size: 200% 100%;
  animation: shimmer 2s ease-in-out infinite;
}
.skeleton-card > span {
  display: block;
  height: 8px;
  margin: 25px 20px;
  width: 50%;
  background: var(--border);
  border-radius: 3px;
}
@keyframes shimmer {
  to {
    background-position: -200% 0;
  }
}
@media (max-width: 1100px) {
  .cards {
    gap: 16px;
  }
  .two-col {
    grid-template-columns: 290px minmax(0, 1fr);
    gap: 22px;
  }
}
@media (max-width: 900px) {
  .cards {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .upcoming-grid {
    grid-template-columns: 1.5fr 1fr;
    gap: 14px;
  }
  .upcoming-rail {
    gap: 14px;
  }
  .two-col {
    grid-template-columns: 270px minmax(0, 1fr);
    gap: 20px;
  }
  .day-events {
    grid-template-columns: 1fr;
  }
}
@media (max-width: 640px) {
  .dashboard {
    padding-top: 24px;
    gap: 30px;
    padding-bottom: 36px;
  }
  .upcoming-grid {
    grid-template-columns: 1fr;
    gap: 12px;
  }
  .upcoming-rail {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;
  }
  .upcoming-rail :deep(.event-card) {
    min-height: 170px;
  }
  .upcoming-rail :deep(.event-top) {
    padding: 11px;
  }
  .upcoming-rail :deep(.event-content) {
    left: 12px;
    right: 12px;
    bottom: 14px;
  }
  .upcoming-rail :deep(.event-name) {
    display: block;
  }
  .upcoming-rail :deep(.event-name h3) {
    font-size: 15px;
    margin-bottom: 3px;
  }
  .upcoming-rail :deep(.event-version) {
    display: none;
  }
  .upcoming-rail :deep(.event-datetime) {
    flex-wrap: wrap;
    gap: 5px;
  }
  .upcoming-rail :deep(.event-datetime time) {
    gap: 7px;
    font-size: 11px;
  }
  .upcoming-rail :deep(.event-weekday) {
    display: none;
  }
  .cards {
    grid-template-columns: 1fr;
    gap: 20px;
  }
  .two-col {
    grid-template-columns: 1fr;
    gap: 24px;
  }
  .calendar-panel {
    position: static;
    padding: 22px;
  }
  .timeline-legend {
    gap: 12px;
  }
  .day-events {
    grid-template-columns: 1fr;
  }
}
</style>
