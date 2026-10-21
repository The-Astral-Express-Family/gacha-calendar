<script setup lang="ts">
import type { Game } from '@/lib/api'
import { gameColor } from '@/lib/games'

withDefaults(
  defineProps<{
    games: Game[]
    selected: string[]
    range: number
    sort: 'time_asc' | 'time_desc'
    showSort?: boolean
  }>(),
  { showSort: true },
)

const emit = defineEmits<{
  (e: 'toggle-game', id: string): void
  (e: 'range', days: number): void
  (e: 'sort', value: 'time_asc' | 'time_desc'): void
  (e: 'all-games'): void
}>()

const RANGES: Array<{ value: number; label: string }> = [
  { value: 14, label: '14 天' },
  { value: 30, label: '30 天' },
  { value: 90, label: '90 天' },
  { value: 3650, label: '全部' },
]
</script>

<template>
  <div class="filter-bar">
    <div class="chips">
      <button class="chip" :class="{ active: selected.length === 0 }" @click="emit('all-games')">全部游戏</button>
      <button
        v-for="game in games"
        :key="game.id"
        class="chip"
        :class="{ active: selected.includes(game.id) }"
        :style="{ '--chip-color': gameColor(game.id) }"
        @click="emit('toggle-game', game.id)"
      >
        <span class="dot" :style="{ background: gameColor(game.id) }" />
        {{ game.name }}
      </button>
    </div>
    <div class="controls">
      <label class="control">
        范围
        <select :value="range" @change="emit('range', Number(($event.target as HTMLSelectElement).value))">
          <option v-for="option in RANGES" :key="option.value" :value="option.value">{{ option.label }}</option>
        </select>
      </label>
      <label v-if="showSort" class="control">
        排序
        <select :value="sort" @change="emit('sort', ($event.target as HTMLSelectElement).value as 'time_asc' | 'time_desc')">
          <option value="time_asc">时间 ↑</option>
          <option value="time_desc">时间 ↓</option>
        </select>
      </label>
    </div>
  </div>
</template>

<style scoped>
.filter-bar {
  display: flex;
  gap: 0.8rem;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  margin-bottom: 1rem;
}
.chips { display: flex; gap: 0.45rem; flex-wrap: wrap; }
.controls { display: flex; gap: 0.7rem; flex-wrap: wrap; }
.control { display: inline-flex; gap: 0.4rem; align-items: center; font-size: 0.8rem; color: var(--text-dim); }
</style>
