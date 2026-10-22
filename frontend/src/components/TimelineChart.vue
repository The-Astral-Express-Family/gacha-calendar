<script setup lang="ts">
// Interactive multi-lane schedule diagram (运行图): one lane per game,
// diamond = version update, circle = preview stream. Drag to pan, wheel or
// pinch to zoom, day boundaries follow the selected display timezone. The
// window is owned by the parent; gestures emit it back so the agenda below
// follows whatever is on screen.
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'

import type { Game } from '@/lib/api'
import { gameColor, shortName } from '@/lib/games'
import { nowUnix } from '@/stores/now'
import { tzZone } from '@/stores/tz'
import { BUSINESS_TZ, dayKey, formatDateTime, formatTime, zoneDayStart } from '@/lib/time'

export interface TimelineEvent {
  key: string
  gameId: string
  gameName: string
  kind: 'update' | 'preview'
  unix: number
  label: string
  confidence?: string
}

const props = defineProps<{
  games: Game[]
  events: TimelineEvent[]
  startUnix: number
  endUnix: number
}>()

const emit = defineEmits<{
  (e: 'window', value: { start: number; end: number }): void
  (e: 'recenter'): void
}>()

const MIN_SPAN = 2 * 86400
const MAX_SPAN = 3 * 365 * 86400
const DAY = 86400

const wrap = ref<HTMLElement | null>(null)
const width = ref(0)
const laneHeight = 46
const headerHeight = 30
const leftWidth = computed(() => (width.value < 620 ? 62 : 118))
const height = computed(() => headerHeight + laneHeight * Math.max(props.games.length, 1))
const plotWidth = computed(() => Math.max(width.value - leftWidth.value - 12, 120))

let resizeObserver: ResizeObserver | undefined
function measure() {
  if (!wrap.value) return
  const w = Math.floor(wrap.value.getBoundingClientRect().width)
  if (w > 0) width.value = w
}
onMounted(() => {
  // measure synchronously so the svg paints immediately; ResizeObserver only
  // follows later size changes (and never fires in some embedded webviews)
  measure()
  requestAnimationFrame(measure)
  if (typeof ResizeObserver !== 'undefined') {
    resizeObserver = new ResizeObserver(measure)
    resizeObserver.observe(wrap.value!)
  }
  window.addEventListener('resize', measure)
  wrap.value?.addEventListener('wheel', onWheel, { passive: false })
})
onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  window.removeEventListener('resize', measure)
  wrap.value?.removeEventListener('wheel', onWheel)
})

// ---- time window -----------------------------------------------------------

const win = reactive({ start: props.startUnix, end: props.endUnix })
watch(
  () => [props.startUnix, props.endUnix] as const,
  ([start, end]) => {
    win.start = start
    win.end = end
  }
)

// Keep the view near the data: panning into a far empty future feels broken,
// so the window center stays within [data min - 60d, data max + 180d].
const extent = computed(() => {
  if (props.events.length === 0) {
    return { min: nowUnix.value - 30 * DAY, max: nowUnix.value + 30 * DAY }
  }
  let min = Infinity
  let max = -Infinity
  for (const event of props.events) {
    if (event.unix < min) min = event.unix
    if (event.unix > max) max = event.unix
  }
  return { min, max }
})

function clampSpan(span: number): number {
  return Math.min(Math.max(span, MIN_SPAN), MAX_SPAN)
}

function clampCenter() {
  const span = win.end - win.start
  const center = (win.start + win.end) / 2
  const lo = extent.value.min - 60 * DAY
  const hi = extent.value.max + 180 * DAY
  if (center < lo) {
    win.start = lo - span / 2
    win.end = win.start + span
  } else if (center > hi) {
    win.start = hi - span / 2
    win.end = win.start + span
  }
}

function commit() {
  hideTip()
  emit('window', { start: Math.round(win.start), end: Math.round(win.end) })
}

function setSpanAt(anchor: number, span: number) {
  const next = clampSpan(span)
  const ratio = (anchor - win.start) / Math.max(win.end - win.start, 1)
  win.start = anchor - ratio * next
  win.end = win.start + next
  clampCenter()
  commit()
}

function panBy(unixDelta: number) {
  const span = win.end - win.start
  win.start += unixDelta
  win.end = win.start + span
  clampCenter()
  commit()
}

function zoomStep(factor: number) {
  setSpanAt((win.start + win.end) / 2, (win.end - win.start) * factor)
}

// ---- coordinates -----------------------------------------------------------

function x(unix: number): number {
  const span = Math.max(win.end - win.start, 1)
  const ratio = (unix - win.start) / span
  return leftWidth.value + Math.min(Math.max(ratio, 0), 1) * plotWidth.value
}

function unixAt(px: number): number {
  return win.start + ((px - leftWidth.value) / plotWidth.value) * (win.end - win.start)
}

function laneY(index: number): number {
  return headerHeight + index * laneHeight + laneHeight / 2
}

// ---- adaptive ticks --------------------------------------------------------

const weekdayFmts = new Map<string, Intl.DateTimeFormat>()
function weekdayOf(zone: string, unix: number): string {
  let fmt = weekdayFmts.get(zone)
  if (!fmt) {
    fmt = new Intl.DateTimeFormat('en-US', { timeZone: zone, weekday: 'short' })
    weekdayFmts.set(zone, fmt)
  }
  return fmt.format(new Date(unix * 1000))
}

interface Tick {
  x: number
  label: string
  major: boolean
}
interface Band {
  x: number
  w: number
}

const dayWidth = computed(() => (plotWidth.value * DAY) / Math.max(win.end - win.start, 1))

const ticks = computed<Tick[]>(() => {
  const zone = tzZone.value
  const out: Tick[] = []
  const dw = dayWidth.value
  if (dw >= 15) {
    // day-level grid; the step keeps labels from colliding and stays aligned
    // to weekdays so gridlines don't wobble while panning. Month starts are
    // always drawn as major ticks with their own label.
    const step = dw >= 64 ? 1 : dw >= 34 ? 2 : 7
    const minor = (weekday: string) =>
      step === 1 ? true : step === 2 ? weekday === 'Mon' || weekday === 'Wed' || weekday === 'Fri' : weekday === 'Mon'
    let cursor = zoneDayStart(zone, dayKey(zone, win.start))
    let lastMajor = -Infinity
    for (let i = 0; i < 400 && cursor <= win.end; i += 1) {
      if (cursor >= win.start) {
        const key = dayKey(zone, cursor)
        const month = Number(key.slice(5, 7))
        const day = Number(key.slice(8))
        if (day === 1) {
          out.push({ x: x(cursor), label: `${month}月`, major: true })
          lastMajor = cursor
        } else if (minor(weekdayOf(zone, cursor)) && cursor - lastMajor > 2.5 * DAY) {
          out.push({
            x: x(cursor),
            label: `${String(month).padStart(2, '0')}-${String(day).padStart(2, '0')}`,
            major: false,
          })
        }
      }
      cursor += DAY
    }
  } else {
    // month-level grid
    let cursor = zoneDayStart(zone, dayKey(zone, win.start))
    for (let i = 0; i < 40 && Number(dayKey(zone, cursor).slice(8)) !== 1; i += 1) {
      cursor -= DAY
    }
    for (let i = 0; i < 90 && cursor <= win.end; i += 1) {
      if (cursor >= win.start) {
        const key = dayKey(zone, cursor)
        const year = Number(key.slice(0, 4))
        const month = Number(key.slice(5, 7))
        out.push({
          x: x(cursor),
          label: month === 1 ? `${year}年${month}月` : `${month}月`,
          major: true,
        })
      }
      const nextKey = dayKey(zone, cursor + 32 * DAY).slice(0, 7)
      cursor = zoneDayStart(zone, `${nextKey}-01`)
    }
  }
  return out
})

const weekendBands = computed<Band[]>(() => {
  if (dayWidth.value < 34) return []
  const zone = tzZone.value
  const out: Band[] = []
  let cursor = zoneDayStart(zone, dayKey(zone, win.start))
  for (let i = 0; i < 120 && cursor <= win.end; i += 1) {
    if (weekdayOf(zone, cursor) === 'Sat') out.push({ x: x(cursor), w: dayWidth.value })
    cursor += DAY
  }
  return out
})

// ---- marks + tooltip -------------------------------------------------------

const marks = computed(() =>
  props.events
    .filter((event) => event.unix >= win.start && event.unix <= win.end)
    .map((event) => {
      const lane = props.games.findIndex((game) => game.id === event.gameId)
      return {
        event,
        x: x(event.unix),
        y: laneY(Math.max(lane, 0)),
        color: gameColor(event.gameId),
      }
    })
)

const tip = ref<{ x: number; y: number; event: TimelineEvent } | null>(null)

function showTip(event: TimelineEvent, px: number, py: number) {
  tip.value = {
    x: Math.min(Math.max(px, 120), Math.max(width.value - 120, 120)),
    y: Math.max(py, 46),
    event,
  }
}

function hideTip() {
  tip.value = null
}

// ---- gestures --------------------------------------------------------------

const pointers = new Map<number, { x: number }>()
let gesture: 'none' | 'pan' | 'pinch' = 'none'
let dragTravel = 0
let pinchStartDist = 1
let pinchStartSpan = 1

function localX(clientX: number): number {
  const rect = wrap.value?.getBoundingClientRect()
  return rect ? clientX - rect.left : 0
}

function onPointerDown(event: PointerEvent) {
  if (!wrap.value) return
  try {
    wrap.value.setPointerCapture(event.pointerId)
  } catch {
    /* untrusted or already-released pointer; the gesture still works */
  }
  measure() // layout may have settled since mount without any event we saw
  pointers.set(event.pointerId, { x: event.clientX })
  dragTravel = 0
  if (pointers.size === 2) {
    const [a, b] = [...pointers.values()]
    pinchStartDist = Math.max(Math.abs(a.x - b.x), 1)
    pinchStartSpan = win.end - win.start
    gesture = 'pinch'
  } else {
    gesture = 'pan'
  }
}

function onPointerMove(event: PointerEvent) {
  const prev = pointers.get(event.pointerId)
  if (!prev) return
  const dx = event.clientX - prev.x
  prev.x = event.clientX
  dragTravel += Math.abs(dx)
  if (gesture === 'pan' && pointers.size === 1) {
    panBy(-dx * ((win.end - win.start) / plotWidth.value))
  } else if (gesture === 'pinch' && pointers.size === 2) {
    const [a, b] = [...pointers.values()]
    const dist = Math.max(Math.abs(a.x - b.x), 1)
    const anchor = unixAt(localX((a.x + b.x) / 2))
    // fingers apart (dist grows) zoom in: content scale follows the finger
    // distance, so the time span shrinks with startDist/dist
    setSpanAt(anchor, pinchStartSpan * (pinchStartDist / dist))
  }
}

function onPointerUp(event: PointerEvent) {
  const wasTap = dragTravel < 6 && gesture === 'pan'
  pointers.delete(event.pointerId)
  gesture = pointers.size === 0 ? 'none' : 'pan'
  if (wasTap) {
    // tap on/near a mark opens its tooltip; tap on empty space closes it
    const rect = wrap.value?.getBoundingClientRect()
    const px = localX(event.clientX)
    const py = rect ? event.clientY - rect.top : 0
    let best: (typeof marks.value)[number] | null = null
    let bestDist = 18
    for (const mark of marks.value) {
      const dist = Math.hypot(mark.x - px, mark.y - py)
      if (dist < bestDist) {
        best = mark
        bestDist = dist
      }
    }
    if (best) showTip(best.event, best.x, best.y)
    else hideTip()
  }
}

function onPointerCancel(event: PointerEvent) {
  pointers.delete(event.pointerId)
  gesture = pointers.size === 0 ? 'none' : 'pan'
}

function onWheel(event: WheelEvent) {
  event.preventDefault()
  if (Math.abs(event.deltaX) > Math.abs(event.deltaY)) {
    panBy(event.deltaX * ((win.end - win.start) / plotWidth.value))
  } else {
    setSpanAt(unixAt(localX(event.clientX)), (win.end - win.start) * Math.exp(event.deltaY * 0.0016))
  }
}

function onKeydown(event: KeyboardEvent) {
  const span = win.end - win.start
  switch (event.key) {
    case 'ArrowLeft':
      panBy(-span * 0.12)
      event.preventDefault()
      break
    case 'ArrowRight':
      panBy(span * 0.12)
      event.preventDefault()
      break
    case '+':
    case '=':
      zoomStep(0.75)
      event.preventDefault()
      break
    case '-':
    case '_':
      zoomStep(1.33)
      event.preventDefault()
      break
    case 'Home':
    case '0':
      emit('recenter')
      event.preventDefault()
      break
    default:
      break
  }
}

const nowVisible = computed(() => nowUnix.value >= win.start && nowUnix.value <= win.end)
const coarse = typeof matchMedia !== 'undefined' && matchMedia('(pointer: coarse)').matches
</script>

<template>
  <div class="timeline-wrap" :class="{ dragging: gesture !== 'none' }">
    <div
      ref="wrap"
      class="stage"
      role="application"
      aria-label="版本事件时间轴：拖动平移，滚轮或双指缩放，方向键平移，加减号缩放"
      tabindex="0"
      @pointerdown="onPointerDown"
      @pointermove="onPointerMove"
      @pointerup="onPointerUp"
      @pointercancel="onPointerCancel"
      @keydown="onKeydown"
    >
      <svg v-if="width > 0" :width="width" :height="height" class="timeline">
        <!-- weekend shading -->
        <rect
          v-for="(band, index) in weekendBands"
          :key="`we-${index}`"
          :x="band.x"
          :y="headerHeight - 4"
          :width="band.w"
          :height="height - headerHeight + 4"
          fill="rgba(139, 160, 205, 0.05)"
        />

        <!-- grid + date ticks -->
        <g v-for="(tick, index) in ticks" :key="`t-${index}`">
          <line
            :x1="tick.x"
            :x2="tick.x"
            :y1="headerHeight - 4"
            :y2="height"
            :stroke="tick.major ? 'rgba(151, 163, 189, 0.22)' : 'rgba(151, 163, 189, 0.08)'"
          />
          <text :x="tick.x" :y="16" class="tick" :class="{ major: tick.major }" text-anchor="middle">
            {{ tick.label }}
          </text>
        </g>

        <!-- lanes -->
        <g v-for="(game, index) in games" :key="game.id">
          <line
            :x1="leftWidth"
            :x2="width - 12"
            :y1="laneY(index)"
            :y2="laneY(index)"
            stroke="rgba(151, 163, 189, 0.1)"
          />
          <text :x="8" :y="laneY(index) + 4" class="lane-label" :fill="gameColor(game.id)">
            {{ shortName(game) }}
          </text>
        </g>

        <!-- now line -->
        <g v-if="nowVisible">
          <line :x1="x(nowUnix)" :x2="x(nowUnix)" :y1="headerHeight - 4" :y2="height" class="now-line" />
          <text :x="x(nowUnix)" :y="height - 5" class="now-text" text-anchor="middle">现在</text>
        </g>

        <!-- events -->
        <g v-for="mark in marks" :key="mark.event.key">
          <circle
            :cx="mark.x"
            :cy="mark.y"
            r="14"
            fill="transparent"
            @mouseenter="showTip(mark.event, mark.x, mark.y)"
            @mouseleave="hideTip"
          />
          <polygon
            v-if="mark.event.kind === 'update'"
            :points="`${mark.x},${mark.y - 6.5} ${mark.x + 6.5},${mark.y} ${mark.x},${mark.y + 6.5} ${mark.x - 6.5},${mark.y}`"
            :fill="mark.color"
            class="mark"
            :class="{ hot: tip?.event.key === mark.event.key }"
          />
          <circle
            v-else
            :cx="mark.x"
            :cy="mark.y"
            r="5.5"
            :fill="mark.color"
            fill-opacity="0.3"
            :stroke="mark.color"
            stroke-width="2"
            class="mark"
            :class="{ hot: tip?.event.key === mark.event.key }"
          />
        </g>
      </svg>

      <!-- edge chip: jump back when "now" has been panned off screen -->
      <button v-if="!nowVisible" class="edge" :class="nowUnix < win.start ? 'left' : 'right'" @click="emit('recenter')">
        {{ nowUnix < win.start ? '‹ 现在' : '现在 ›' }}
      </button>

      <!-- tooltip -->
      <div v-if="tip" class="tip" :style="{ transform: `translate(${tip.x}px, ${tip.y}px)` }">
        <div class="tip-game" :style="{ color: gameColor(tip.event.gameId) }">
          {{ tip.event.gameName }}
          <span class="tip-kind">{{ tip.event.kind === 'preview' ? '前瞻直播' : '版本更新' }}</span>
        </div>
        <div class="tip-time">{{ formatDateTime(tzZone, tip.event.unix) }}（{{ tzZone }}）</div>
        <div class="tip-time faint">国服 {{ formatTime(BUSINESS_TZ, tip.event.unix) }}</div>
      </div>
    </div>

    <div class="stage-tools">
      <button class="tool" title="缩小（-）" aria-label="缩小" @click="zoomStep(1.33)">−</button>
      <button class="tool" title="放大（+）" aria-label="放大" @click="zoomStep(0.75)">＋</button>
      <button class="tool" title="回到当前范围" aria-label="回到当前范围" @click="emit('recenter')">⌂</button>
    </div>

    <div class="gesture-hint faint">
      {{ coarse ? '拖动平移 · 双指缩放 · 点标记看详情' : '拖动平移 · 滚轮缩放 · 悬停标记看详情' }}
    </div>
  </div>
</template>

<style scoped>
.timeline-wrap {
  position: relative;
}
.stage {
  position: relative;
  overflow: hidden;
  touch-action: pan-y;
  user-select: none;
  -webkit-user-select: none;
  cursor: grab;
  outline: none;
}
.stage:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: -2px;
}
.timeline-wrap.dragging .stage {
  cursor: grabbing;
}
.timeline {
  display: block;
}
.tick {
  fill: var(--text-faint);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
.tick.major {
  fill: var(--text-dim);
  font-weight: 600;
}
.lane-label {
  font-size: 14px;
  font-weight: 650;
}
.now-line {
  stroke: var(--now);
  stroke-width: 1.5;
  stroke-dasharray: 5 4;
}
.now-text {
  fill: var(--now);
  font-size: 12px;
  font-weight: 600;
  paint-order: stroke;
  stroke: var(--bg);
  stroke-width: 3px;
}
.mark {
  pointer-events: none;
}
.mark.hot {
  filter: brightness(1.45);
}
.edge {
  position: absolute;
  top: 26px;
  padding: 0.1rem 0.5rem;
  font-size: 0.74rem;
  color: var(--now);
  border-color: color-mix(in srgb, var(--now) 45%, transparent);
  background: color-mix(in srgb, var(--now) 12%, var(--bg));
}
.edge.left {
  left: 8px;
}
.edge.right {
  right: 8px;
}
.stage-tools {
  position: absolute;
  top: 26px;
  right: 10px;
  display: flex;
  gap: 4px;
}
.tool {
  width: 26px;
  height: 26px;
  padding: 0;
  line-height: 1;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: rgba(11, 17, 30, 0.72);
  backdrop-filter: blur(6px);
}
.tip {
  position: absolute;
  left: 0;
  top: 0;
  pointer-events: none;
  background: rgba(9, 14, 26, 0.95);
  border: 1px solid var(--border);
  border-radius: var(--radius-tag);
  padding: 0.5rem 0.7rem;
  min-width: 190px;
  z-index: 5;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.45);
}
.tip-game {
  font-weight: 650;
  font-size: 0.88rem;
  display: flex;
  gap: 0.45rem;
  align-items: baseline;
}
.tip-kind {
  color: var(--text-dim);
  font-size: 0.72rem;
  font-weight: 400;
}
.tip-time {
  font-size: 0.78rem;
  color: var(--text-dim);
  margin-top: 2px;
  font-variant-numeric: tabular-nums;
}
.gesture-hint {
  margin-top: 0.35rem;
  text-align: right;
}
@media (max-width: 620px) {
  .lane-label {
    font-size: 12px;
  }
  .gesture-hint {
    text-align: left;
  }
}
</style>
