<script setup lang="ts">
// Horizontal multi-lane timeline: one lane per game, one mark per event
// (diamond = version update, circle = preview stream). Day boundaries are
// computed in the selected display timezone.
import { computed } from 'vue'

import type { Game } from '@/lib/api'
import { gameColor } from '@/lib/games'
import { tzZone } from '@/stores/tz'
import { dayKey, formatDateShort, zoneDayStart } from '@/lib/time'

export interface TimelineEvent {
  key: string
  gameId: string
  kind: 'update' | 'preview'
  unix: number
  label: string
}

const props = defineProps<{
  games: Game[]
  events: TimelineEvent[]
  startUnix: number
  endUnix: number
}>()

const laneHeight = 44
const headerHeight = 34
const leftWidth = 118
const width = 1080
const height = computed(() => headerHeight + laneHeight * Math.max(props.games.length, 1))

const plotWidth = width - leftWidth - 16

function x(unix: number): number {
  const span = Math.max(props.endUnix - props.startUnix, 1)
  const ratio = (unix - props.startUnix) / span
  return leftWidth + Math.min(Math.max(ratio, 0), 1) * plotWidth
}

const nowUnix = Math.floor(Date.now() / 1000)

const ticks = computed(() => {
  const zone = tzZone.value
  const out: Array<{ x: number; label: string; weekend: boolean }> = []
  const startKeyDay = dayKey(zone, props.startUnix)
  let cursor = zoneDayStart(zone, startKeyDay)
  const guard = 400
  for (let i = 0; i < guard && cursor <= props.endUnix; i += 1) {
    if (cursor >= props.startUnix) {
      const date = new Date(cursor * 1000)
      const weekday = new Intl.DateTimeFormat('en-US', { timeZone: zone, weekday: 'short' }).format(date)
      out.push({
        x: x(cursor),
        label: formatDateShort(zone, cursor),
        weekend: weekday === 'Sat' || weekday === 'Sun',
      })
    }
    cursor += 86400
  }
  return out
})

const marks = computed(() =>
  props.events
    .filter((event) => event.unix >= props.startUnix - 86400 && event.unix <= props.endUnix + 86400)
    .map((event) => {
      const lane = props.games.findIndex((game) => game.id === event.gameId)
      return {
        ...event,
        x: x(event.unix),
        y: headerHeight + lane * laneHeight + laneHeight / 2,
        color: gameColor(event.gameId),
      }
    }),
)

function laneY(index: number): number {
  return headerHeight + index * laneHeight + laneHeight / 2
}
</script>

<template>
  <svg :viewBox="`0 0 ${width} ${height}`" class="timeline" role="img" aria-label="版本事件时间轴">
    <!-- day grid -->
    <g v-for="tick in ticks" :key="tick.label + tick.x">
      <line
        :x1="tick.x" :x2="tick.x" :y1="headerHeight - 6" :y2="height"
        :stroke="tick.weekend ? 'rgba(255,255,255,0.07)' : 'rgba(255,255,255,0.035)'"
      />
      <text :x="tick.x" :y="headerHeight - 12" class="tick" text-anchor="middle">{{ tick.label }}</text>
    </g>

    <!-- now line -->
    <line
      v-if="nowUnix >= startUnix && nowUnix <= endUnix"
      :x1="x(nowUnix)" :x2="x(nowUnix)" :y1="headerHeight - 6" :y2="height"
      class="now-line"
    />
    <text v-if="nowUnix >= startUnix && nowUnix <= endUnix" :x="x(nowUnix) + 4" :y="height - 4" class="now-text">现在</text>

    <!-- lanes -->
    <g v-for="(game, index) in games" :key="game.id">
      <line
        :x1="leftWidth" :x2="width - 16" :y1="laneY(index)" :y2="laneY(index)"
        stroke="rgba(255,255,255,0.06)"
      />
      <text :x="8" :y="laneY(index) + 4" class="lane-label" :fill="gameColor(game.id)">{{ game.name }}</text>
    </g>

    <!-- events -->
    <g v-for="mark in marks" :key="mark.key">
      <polygon
        v-if="mark.kind === 'update'"
        :points="`${mark.x},${mark.y - 7} ${mark.x + 7},${mark.y} ${mark.x},${mark.y + 7} ${mark.x - 7},${mark.y}`"
        :fill="mark.color"
        class="mark"
      >
        <title>{{ mark.label }}</title>
      </polygon>
      <circle v-else :cx="mark.x" :cy="mark.y" r="6.5" :fill="mark.color" fill-opacity="0.28" :stroke="mark.color" stroke-width="2" class="mark">
        <title>{{ mark.label }}</title>
      </circle>
    </g>
  </svg>
</template>

<style scoped>
.timeline { width: 100%; height: auto; }
.tick { fill: #6b7899; font-size: 15px; }
.now-line { stroke: #f87171; stroke-width: 1.5; stroke-dasharray: 5 4; }
.now-text { fill: #f87171; font-size: 13px; }
.lane-label { font-size: 15px; font-weight: 600; }
.mark { cursor: default; }
.mark:hover { filter: brightness(1.35); }
</style>
