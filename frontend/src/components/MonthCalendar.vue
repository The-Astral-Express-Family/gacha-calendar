<script setup lang="ts">
// Month grid with event dots; day boundaries follow the selected display
// timezone. The displayed month is internal state (‹ › shift it by exactly
// one month) and no longer derives from the selected day — that coupling made
// the grid jump months when leading/trailing days were clicked. Clicking a
// day only emits a selection for the agenda filter; picking a day from an
// adjacent month also flips the view there.
import { computed, ref, watch } from 'vue'

import UiIcon from '@/components/UiIcon.vue'
import { gameColor } from '@/lib/games'
import { nowUnix } from '@/stores/now'
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

const todayKey = computed(() => dayKey(tzZone.value, nowUnix.value))

// Displayed month as "YYYY-MM", decoupled from the selected day.
const month = ref(todayKey.value.slice(0, 7))

watch(tzZone, () => {
  // keep "today" coherent when a zone switch moves the calendar day
  if (!props.selectedKey) month.value = todayKey.value.slice(0, 7)
})

watch(
  () => props.selectedKey,
  (key) => {
    if (key && key.slice(0, 7) !== month.value) month.value = key.slice(0, 7)
  }
)

interface Cell {
  key: string
  day: number
  inMonth: boolean
  events: CalendarEvent[]
  more: number
}

const cells = computed<Cell[]>(() => {
  const zone = tzZone.value
  const [y, m] = month.value.split('-').map(Number)
  const offset = zoneOffsetSeconds(zone, new Date())
  const firstUnix = Math.floor(Date.UTC(y, m - 1, 1) / 1000) - offset
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
    const all = byKey.get(key) ?? []
    out.push({
      key,
      day: Number(key.slice(8)),
      inMonth: key.startsWith(month.value),
      events: all.slice(0, 4),
      more: Math.max(all.length - 4, 0),
    })
  }
  return out
})

const monthTitle = computed(() => {
  const [y, m] = month.value.split('-').map(Number)
  return `${y} / ${String(m).padStart(2, '0')}`
})

function shiftMonth(delta: number) {
  const [y, m] = month.value.split('-').map(Number)
  const total = y * 12 + (m - 1) + delta
  const nextY = Math.floor(total / 12)
  const nextM = (total % 12) + 1
  month.value = `${nextY}-${String(nextM).padStart(2, '0')}`
}

function goToday() {
  month.value = todayKey.value.slice(0, 7)
}

function pick(cell: Cell) {
  if (!cell.inMonth) month.value = cell.key.slice(0, 7)
  emit('select', cell.key === props.selectedKey ? undefined : cell.key)
}
</script>

<template>
  <div class="calendar">
    <div class="cal-head">
      <span class="cal-title">{{ monthTitle }}</span>
      <span class="cal-nav">
        <button class="mini previous" title="上个月" aria-label="上个月" @click="shiftMonth(-1)">
          <UiIcon name="chevron" :size="13" />
        </button>
        <button class="mini" title="回到当前月份" aria-label="回到当前月份" @click="goToday">今</button>
        <button class="mini" title="下个月" aria-label="下个月" @click="shiftMonth(1)">
          <UiIcon name="chevron" :size="13" />
        </button>
      </span>
    </div>
    <div class="cal-grid">
      <div v-for="name in ['日', '一', '二', '三', '四', '五', '六']" :key="name" class="cal-weekday">{{ name }}</div>
      <button
        v-for="cell in cells"
        :key="cell.key"
        class="cal-cell"
        :class="{
          dim: !cell.inMonth,
          today: cell.key === todayKey,
          selected: cell.key === selectedKey,
        }"
        :aria-label="`${cell.key}${cell.key === todayKey ? ' 今天' : ''}，${cell.events.length + cell.more} 场事件`"
        :aria-pressed="cell.key === selectedKey"
        @click="pick(cell)"
      >
        <span class="cal-day">{{ cell.day }}</span>
        <span class="cal-dots">
          <i
            v-for="(event, index) in cell.events"
            :key="index"
            :class="event.kind"
            :style="{ background: gameColor(event.gameId) }"
          />
          <span v-if="cell.more" class="cal-more">+{{ cell.more }}</span>
        </span>
      </button>
    </div>
    <div class="cal-legend faint">
      <span><i class="dot-preview" /> 前瞻</span>
      <span><i class="dot-update" /> 更新</span>
    </div>
  </div>
</template>

<style scoped>
.calendar {
  display: flex;
  flex-direction: column;
  gap: 20px;
}
.cal-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.4rem;
}
.cal-title {
  font-weight: 500;
  font-size: 19px;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.cal-nav {
  display: inline-flex;
  gap: 0.35rem;
}
button.mini {
  padding: 0;
  width: 28px;
  height: 28px;
  background: transparent;
  color: var(--text-dim);
  font-size: 11px;
}
.previous svg {
  transform: rotate(180deg);
}
.cal-grid {
  display: grid;
  grid-template-columns: repeat(7, 1fr);
  gap: 3px;
}
.cal-weekday {
  text-align: center;
  font-size: 0.7rem;
  color: var(--text-faint);
  padding: 0.15rem 0;
}
.cal-cell {
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: flex-start;
  gap: 3px;
  padding: 0.28rem 0 0.24rem;
  min-height: 44px;
  border-radius: 5px;
  background: transparent;
  border: 1px solid transparent;
}
.cal-cell:hover {
  background: rgba(151, 163, 189, 0.09);
}
.cal-cell.dim .cal-day {
  color: var(--text-faint);
  opacity: 0.5;
}
.cal-cell.today {
  background: #ff8d880c;
}
.cal-cell.today .cal-day {
  color: var(--now);
  font-weight: 650;
}
.cal-cell.selected {
  background: var(--accent-soft);
  border-color: var(--accent);
}
.cal-day {
  font-size: 0.8rem;
  font-variant-numeric: tabular-nums;
}
.cal-dots {
  display: flex;
  gap: 3px;
  height: 7px;
}
.cal-dots i {
  width: 4px;
  height: 4px;
  border-radius: 50%;
  display: inline-block;
}
.cal-more {
  font-size: 8px;
  line-height: 1;
  color: var(--text-dim);
}
.cal-dots i.update {
  border-radius: 1.5px;
  transform: rotate(45deg);
}
.cal-legend {
  display: flex;
  gap: 0.9rem;
  align-items: center;
  flex-wrap: wrap;
  padding-top: 14px;
  border-top: 1px solid var(--border);
  font-size: 10px;
}
.cal-legend i {
  width: 8px;
  height: 8px;
  display: inline-block;
  border-radius: 50%;
  margin-right: 4px;
  vertical-align: -1px;
  background: var(--text-dim);
}
.cal-legend i.dot-update {
  border-radius: 1.5px;
  transform: rotate(45deg);
}
</style>
