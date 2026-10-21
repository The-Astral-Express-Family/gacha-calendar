<script setup lang="ts">
// Month grid with event dots; day boundaries follow the selected display
// timezone. Clicking a day emits a selection for the agenda filter.
import { computed } from 'vue'

import { gameColor } from '@/lib/games'
import { tzZone } from '@/stores/tz'
import { dayKey, zoneDayStart, zoneOffsetSeconds } from '@/lib/time'

export interface CalendarEvent {
  gameId: string
  kind: 'update' | 'preview'
  unix: number
}

const props = defineProps<{
  events: CalendarEvent[]
  selectedKey?: string
}>()

const emit = defineEmits<{ (e: 'select', key: string | undefined): void }>()

const nowKey = computed(() => dayKey(tzZone.value, Math.floor(Date.now() / 1000)))

interface Cell {
  key: string
  day: number
  inMonth: boolean
  events: CalendarEvent[]
}

const cells = computed<Cell[]>(() => {
  const zone = tzZone.value
  // Anchor on today (or the selected day's month) in the display zone.
  const anchorUnix = props.selectedKey
    ? zoneDayStart(zone, props.selectedKey)
    : Math.floor(Date.now() / 1000)
  const anchorKey = dayKey(zone, anchorUnix || Math.floor(Date.now() / 1000))
  const [y, m] = anchorKey.split('-').map(Number)
  const offset = zoneOffsetSeconds(zone, new Date())
  const firstUnix = Math.floor(Date.UTC(y, m - 1, 1) / 1000) - offset
  // Weekday of the first of month in this zone.
  const firstKey = dayKey(zone, firstUnix)
  const firstDayUnix = zoneDayStart(zone, firstKey)
  const firstWeekday = new Date((firstDayUnix + offset) * 1000).getUTCDay()
  const gridStart = firstDayUnix - firstWeekday * 86400

  const byKey = new Map<string, CalendarEvent[]>()
  for (const event of props.events) {
    const key = dayKey(zone, event.unix)
    const bucket = byKey.get(key) ?? []
    bucket.push(event)
    byKey.set(key, bucket)
  }

  const out: Cell[] = []
  for (let i = 0; i < 42; i += 1) {
    const unix = gridStart + i * 86400
    const key = dayKey(zone, unix)
    const day = Number(key.slice(8))
    out.push({
      key,
      day,
      inMonth: key.startsWith(`${y}-${String(m).padStart(2, '0')}`),
      events: (byKey.get(key) ?? []).slice(0, 4),
    })
  }
  return out
})

const monthTitle = computed(() => {
  const zone = tzZone.value
  const anchorUnix = props.selectedKey
    ? zoneDayStart(zone, props.selectedKey)
    : Math.floor(Date.now() / 1000)
  return new Intl.DateTimeFormat('zh-CN', { timeZone: zone, year: 'numeric', month: 'long' })
    .format(new Date((anchorUnix || Math.floor(Date.now() / 1000)) * 1000))
})

function shiftMonth(delta: number) {
  const zone = tzZone.value
  const anchorKey = props.selectedKey ?? nowKey.value
  const anchorUnix = zoneDayStart(zone, anchorKey) || Math.floor(Date.now() / 1000)
  const shifted = new Date((anchorUnix + delta * 31 * 86400) * 1000)
  const key = dayKey(zone, Math.floor(shifted.getTime() / 1000))
  const [y, m] = key.split('-').map(Number)
  const target = new Date(Date.UTC(y, m - 1 + delta, 1))
  const offset = zoneOffsetSeconds(zone, target)
  const unix = Math.floor(target.getTime() / 1000) - offset
  emit('select', dayKey(zone, unix))
}
</script>

<template>
  <div class="calendar">
    <div class="cal-head">
      <button class="mini" @click="shiftMonth(-1)">‹</button>
      <span class="cal-title">{{ monthTitle }}<span class="faint">（{{ tzZone }}）</span></span>
      <button class="mini" @click="shiftMonth(1)">›</button>
    </div>
    <div class="cal-grid">
      <div v-for="name in ['日', '一', '二', '三', '四', '五', '六']" :key="name" class="cal-weekday">{{ name }}</div>
      <button
        v-for="cell in cells"
        :key="cell.key"
        class="cal-cell"
        :class="{
          dim: !cell.inMonth,
          today: cell.key === nowKey,
          selected: cell.key === selectedKey,
        }"
        @click="emit('select', cell.key === selectedKey ? undefined : cell.key)"
      >
        <span class="cal-day">{{ cell.day }}</span>
        <span class="cal-dots">
          <i
            v-for="(event, index) in cell.events"
            :key="index"
            :class="event.kind"
            :style="{ background: gameColor(event.gameId) }"
          />
        </span>
      </button>
    </div>
    <div class="cal-legend faint">
      <span><i class="dot-preview" /> 前瞻</span>
      <span><i class="dot-update" /> 更新</span>
      <span>点击日期筛选下方列表</span>
    </div>
  </div>
</template>

<style scoped>
.calendar { display: flex; flex-direction: column; gap: 0.6rem; }
.cal-head { display: flex; align-items: center; justify-content: space-between; }
.cal-title { font-weight: 650; font-size: 0.95rem; }
button.mini { padding: 0.15rem 0.55rem; }
.cal-grid { display: grid; grid-template-columns: repeat(7, 1fr); gap: 3px; }
.cal-weekday { text-align: center; font-size: 0.7rem; color: var(--text-faint); padding: 0.15rem 0; }
.cal-cell {
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: flex-start;
  gap: 2px;
  padding: 0.3rem 0 0.25rem;
  min-height: 44px;
  border-radius: 9px;
  background: transparent;
  border: 1px solid transparent;
}
.cal-cell:hover { background: rgba(255, 255, 255, 0.06); }
.cal-cell.dim .cal-day { color: var(--text-faint); opacity: 0.55; }
.cal-cell.today { border-color: rgba(248, 113, 113, 0.55); }
.cal-cell.selected { background: var(--accent-soft); border-color: var(--accent); }
.cal-day { font-size: 0.8rem; }
.cal-dots { display: flex; gap: 3px; height: 7px; }
.cal-dots i { width: 7px; height: 7px; border-radius: 50%; display: inline-block; }
.cal-dots i.update { border-radius: 1.5px; transform: rotate(45deg); }
.cal-legend { display: flex; gap: 0.9rem; align-items: center; }
.cal-legend i { width: 8px; height: 8px; display: inline-block; border-radius: 50%; margin-right: 4px; vertical-align: -1px; background: var(--text-dim); }
.cal-legend i.dot-update { border-radius: 1.5px; transform: rotate(45deg); }
</style>
